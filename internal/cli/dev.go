package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"time"
	"unicode"
)

// cacheDir holds binaries built by delegation and serve; generated projects
// ignore it in .gitignore.
const cacheDir = ".vukem"

func exe(name string) string {
	if runtime.GOOS == "windows" {
		return name + ".exe"
	}

	return name
}

// skipDirs are never watched or considered app sources.
var skipDirs = []string{".git", cacheDir, "node_modules", "vendor", "storage", "bin", "dist", "public", "tmp"}

// sourcesModTime returns the newest modification time of the Go and templ
// sources (and go.mod, go.sum and .buildtags) of the project.
func sourcesModTime(dir string) (time.Time, error) {
	var newest time.Time

	err := walkSources(dir, func(_ string, info fs.FileInfo) {
		if info.ModTime().After(newest) {
			newest = info.ModTime()
		}
	})

	return newest, err
}

func walkSources(dir string, fn func(path string, info fs.FileInfo)) error {
	return filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if d.IsDir() {
			if p != dir && (slices.Contains(skipDirs, d.Name()) || strings.HasPrefix(d.Name(), ".")) {
				return filepath.SkipDir
			}

			return nil
		}

		name := d.Name()
		// .buildtags changes what go build compiles, so it is a source too.
		if strings.HasSuffix(name, ".go") || strings.HasSuffix(name, ".templ") || name == "go.mod" ||
			name == "go.sum" || p == filepath.Join(dir, buildTagsFile) {
			info, err := d.Info()
			if err != nil {
				return err
			}

			fn(p, info)
		}

		return nil
	})
}

// appBinary builds cmd/app into .vukem/ when the sources changed since the
// last build, and returns the binary path.
func appBinary(ctx context.Context, env *Env) (string, error) {
	if !exists(env, filepath.Join("cmd", "app")) {
		return "", errors.New("no cmd/app in this directory: run vukem inside a Vukem project")
	}

	bin := env.path(filepath.Join(cacheDir, "bin", exe("app")))

	newest, err := sourcesModTime(env.dirOrDot())
	if err != nil {
		return "", err
	}

	if info, err := os.Stat(bin); err == nil && info.ModTime().After(newest) {
		return bin, nil
	}

	// Same steps as vukem dev: stale *_templ.go files would compile the
	// previous components.
	if hasTempl(env) {
		if err := templGenerate(ctx, env); err != nil {
			return "", err
		}
	}

	if err := goBuild(ctx, env, bin); err != nil {
		return "", err
	}

	return bin, nil
}

func goBuild(ctx context.Context, env *Env, out string, extra ...string) error {
	return goBuildEnv(ctx, env, out, nil, extra...)
}

// goBuildEnv is goBuild with extra environment variables for go build.
func goBuildEnv(ctx context.Context, env *Env, out string, environ []string, extra ...string) error {
	if !filepath.IsAbs(out) {
		out = env.path(out)
	}

	if err := os.MkdirAll(filepath.Dir(out), 0o750); err != nil {
		return err
	}

	tags, err := projectBuildTags(env)
	if err != nil {
		return err
	}

	args := append([]string{"build"}, extra...)
	if tags != "" {
		args = append(args, "-tags", tags)
	}

	args = append(args, "-o", out, "./cmd/app")

	return toolEnv(ctx, env, environ, "go", args...)
}

// releaseBuildEnv is the environment of vukem build: CGO_ENABLED=0, like
// the generated Dockerfile, so a local build links the same way as the
// image; a CGO_ENABLED the developer set explicitly is kept.
func releaseBuildEnv(env *Env) []string {
	if _, ok := env.Getenv("CGO_ENABLED"); ok {
		return nil
	}

	return []string{"CGO_ENABLED=0"}
}

// buildTagsFile lists Go build tags (one per line or comma-separated, "#"
// comments) that every vukem build of the app passes to go build; the
// minimal starter writes vukem_nosqlite there.
const buildTagsFile = ".buildtags"

var buildTag = regexp.MustCompile(`^[A-Za-z0-9_.]+$`)

// projectBuildTags reads buildTagsFile and returns the tags joined with
// commas, or "" when the file does not exist.
func projectBuildTags(env *Env) (string, error) {
	b, err := os.ReadFile(env.path(buildTagsFile))
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}

	if err != nil {
		return "", err
	}

	var tags []string

	for line := range strings.Lines(string(b)) {
		line, _, _ = strings.Cut(line, "#")

		for _, tag := range strings.FieldsFunc(line, func(r rune) bool { return r == ',' || unicode.IsSpace(r) }) {
			if !buildTag.MatchString(tag) {
				return "", fmt.Errorf("%s: invalid build tag %q", buildTagsFile, tag)
			}

			tags = append(tags, tag)
		}
	}

	return strings.Join(tags, ","), nil
}

func (env *Env) dirOrDot() string {
	if env.Dir == "" {
		return "."
	}

	return env.Dir
}

// delegate runs an app command (migrate, route:list, user commands) with
// the project's binary, rebuilding it only when sources changed.
func delegate(ctx context.Context, env *Env, args []string) error {
	if !exists(env, filepath.Join("cmd", "app")) {
		return usagef("unknown command %q (app commands need a Vukem project with cmd/app)", args[0])
	}

	bin, err := appBinary(ctx, env)
	if err != nil {
		return err
	}

	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = env.Dir
	cmd.Stdin = env.Stdin
	cmd.Stdout = env.Stdout
	cmd.Stderr = env.Stderr

	return cmd.Run()
}

func buildCmd(set *flag.FlagSet) func(context.Context, *Env, []string) error {
	out := set.String("o", filepath.Join("bin", exe("app")), "output binary")

	return func(ctx context.Context, env *Env, _ []string) error {
		if !exists(env, filepath.Join("cmd", "app")) {
			return errors.New("no cmd/app in this directory: run vukem inside a Vukem project")
		}

		if hasTempl(env) {
			if err := templGenerate(ctx, env); err != nil {
				return err
			}
		}

		if hasVite(env) {
			pm, err := packageManager(env.Dir, "")
			if err != nil {
				return err
			}

			if !exists(env, "node_modules") {
				return fmt.Errorf("frontend dependencies missing: run %s first", pm.install())
			}

			if err := tool(ctx, env, pm.binary(), append(slices.Clone(pm.exec), "vite", "build")...); err != nil {
				return err
			}
		}

		if err := goBuildEnv(ctx, env, *out, releaseBuildEnv(env), "-trimpath", "-ldflags=-s -w"); err != nil {
			return err
		}

		fmt.Fprintln(env.Stdout, "built", *out)

		return nil
	}
}

func hasVite(env *Env) bool {
	for _, f := range []string{"vite.config.ts", "vite.config.js", "vite.config.mjs"} {
		if exists(env, f) {
			return true
		}
	}

	return false
}

func npm() string { return npmTool("npm") }

func npmTool(name string) string {
	if runtime.GOOS == "windows" {
		return name + ".cmd"
	}

	return name
}
