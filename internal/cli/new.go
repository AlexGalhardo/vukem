package cli

import (
	"bytes"
	"context"
	"embed"
	"errors"
	"flag"
	"fmt"
	"go/format"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"text/template"

	"github.com/AlexGalhardo/golang-framework/config"
)

// starterFS is the HTMX + SQLite starter. Files ending in .tmpl are
// text/template stubs (the suffix is dropped); everything else, including
// html/template views, is copied verbatim.
//
//go:embed all:starter
var starterFS embed.FS

// The starter vendors htmx so apps work offline and under a strict CSP.
// TestVendoredHTMX fails if the file changes without updating the hash.
const (
	htmxVersion = "4.0.0"
	htmxSHA256  = "e484d9171a9db30a39c8f16e3d709d4137f3211c659f8e6125816635033d593f"
	htmxPath    = "resources/public/js/htmx.min.js"
)

const frameworkModule = "github.com/AlexGalhardo/golang-framework"

var projectName = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_.-]*$`)

type starterData struct {
	Name   string // directory and APP_NAME
	Module string // Go module path
}

func newProject(set *flag.FlagSet) func(context.Context, *Env, []string) error {
	module := set.String("module", "", "Go module path (default: the project name)")
	framework := set.String("framework", "", "path to a local Lumek checkout to use instead of a release")

	return func(ctx context.Context, env *Env, args []string) error {
		name, err := oneArg(args, "project name")
		if err != nil {
			return err
		}

		if !projectName.MatchString(name) {
			return usagef("invalid project name %q (letters, digits, '-', '_' and '.')", name)
		}

		dir := env.path(name)
		if _, err := os.Stat(dir); err == nil {
			return fmt.Errorf("%s already exists", name)
		}

		data := starterData{Name: name, Module: *module}
		if data.Module == "" {
			data.Module = name
		}

		if err := writeStarter(dir, data); err != nil {
			return err
		}

		project := *env
		project.Dir = dir

		if err := requireFramework(ctx, &project, *framework); err != nil {
			return err
		}

		if err := tool(ctx, &project, "go", "mod", "tidy"); err != nil {
			return err
		}

		if err := writeAppKey(&project, config.GenerateKey(), false); err != nil {
			return err
		}

		if _, err := exec.LookPath("git"); err == nil {
			cmd := exec.CommandContext(ctx, "git", "init", "--quiet")
			cmd.Dir = dir

			if out, err := cmd.CombinedOutput(); err != nil {
				fmt.Fprintf(env.Stderr, "git init failed (continuing): %v\n%s", err, out)
			}
		}

		fmt.Fprintf(env.Stdout, "\nCreated %s (htmx %s, SQLite). Next:\n\n  cd %s\n  lumek migrate\n  lumek serve\n\n",
			name, htmxVersion, name)

		return nil
	}
}

// writeStarter renders the embedded starter into dir.
func writeStarter(dir string, data starterData) error {
	return fs.WalkDir(starterFS, "starter", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}

		rel := strings.TrimPrefix(p, "starter/")
		if rel == "gitignore" {
			rel = ".gitignore" // stored without the dot so it does not apply to this repository
		}

		content, err := starterFS.ReadFile(p)
		if err != nil {
			return err
		}

		if stub, ok := strings.CutSuffix(rel, ".tmpl"); ok {
			rel = stub

			if content, err = renderStarterFile(rel, content, data); err != nil {
				return err
			}
		}

		dst := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(dst), 0o750); err != nil {
			return err
		}

		return writeFile(dst, content)
	})
}

func renderStarterFile(rel string, src []byte, data starterData) ([]byte, error) {
	t, err := template.New(rel).Option("missingkey=error").Parse(string(src))
	if err != nil {
		return nil, fmt.Errorf("starter %s: %w", rel, err)
	}

	var b bytes.Buffer
	if err := t.Execute(&b, data); err != nil {
		return nil, fmt.Errorf("starter %s: %w", rel, err)
	}

	if !strings.HasSuffix(rel, ".go") {
		return b.Bytes(), nil
	}

	out, err := format.Source(b.Bytes())
	if err != nil {
		return nil, fmt.Errorf("starter %s: %w", rel, err)
	}

	return out, nil
}

// requireFramework points the new project at Lumek: a local checkout via a
// replace directive, or the release matching this CLI (latest for dev
// builds).
func requireFramework(ctx context.Context, env *Env, local string) error {
	if local != "" {
		abs, err := filepath.Abs(local)
		if err != nil {
			return err
		}

		f, err := os.OpenFile(env.path("go.mod"), os.O_APPEND|os.O_WRONLY, 0)
		if err != nil {
			return err
		}

		_, err = fmt.Fprintf(f, "\nrequire %s v0.0.0\n\nreplace %s => %s\n", frameworkModule, frameworkModule,
			filepath.ToSlash(abs))

		return errors.Join(err, f.Close())
	}

	return tool(ctx, env, "go", "get", frameworkModule+"@"+releaseVersion(version(env)))
}

// releaseVersion returns v when it is a tagged release, "latest" for
// development builds (devel, pseudo-versions, +dirty).
func releaseVersion(v string) string {
	if !strings.HasPrefix(v, "v") || strings.Contains(v, "-0.") || strings.Contains(v, "+") {
		return "latest"
	}

	return v
}
