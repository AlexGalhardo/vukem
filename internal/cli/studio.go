package cli

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
)

const (
	studioModule = frameworkModule + "/contrib/studio"
	// studioVersion is the contrib/studio release vukem studio installs: the
	// latest contrib/studio tag (TestStudioVersionIsLatestTag). Bump it after
	// tagging the module.
	studioVersion = "v0.1.0"
)

// studioFlags are the flags of vukem-studio that vukem studio passes on.
var studioFlags = []string{
	"dir",
	"port",
	"no-open",
	"allow-production",
	"database-url",
	"version",
} //nolint:gochecknoglobals // read-only

// studioCmd installs a pinned vukem-studio under .vukem/bin and runs it. The
// binary is its own module (contrib/studio), so the core never imports it.
func studioCmd(set *flag.FlagSet) func(context.Context, *Env, []string) error {
	bin := set.String("bin", "", "use this vukem-studio binary and skip the install")
	set.String("dir", "", "project directory (default: the current one)")
	set.Int("port", 0, "port on 127.0.0.1 (default 4984; 0 picks a free one)")
	set.Bool("no-open", false, "do not open the browser")
	set.Bool("allow-production", false, "allow APP_ENV=production")
	set.String("database-url", "", "override DATABASE_URL")
	set.Bool("version", false, "print the vukem-studio version and exit")

	return func(ctx context.Context, env *Env, _ []string) error {
		path := *bin

		if path == "" {
			var err error
			if path, err = ensureStudio(ctx, env); err != nil {
				return err
			}
		}

		cmd := exec.CommandContext(
			ctx,
			path,
			studioArgs(set, env)...)
		cmd.Dir = env.Dir
		cmd.Stdin = env.Stdin
		cmd.Stdout = env.Stdout
		cmd.Stderr = env.Stderr

		return cmd.Run()
	}
}

// studioArgs rebuilds the vukem-studio command line from the flags that were
// set, with --dir defaulting to the project directory.
func studioArgs(set *flag.FlagSet, env *Env) []string {
	var args []string

	hasDir := false

	set.Visit(func(f *flag.Flag) {
		if !slices.Contains(studioFlags, f.Name) {
			return
		}

		if f.Name == "dir" {
			hasDir = true
		}

		args = append(args, "--"+f.Name+"="+f.Value.String())
	})

	if !hasDir {
		args = append(args, "--dir="+env.dirOrDot())
	}

	return args
}

// ensureStudio returns the path of the pinned vukem-studio, installing it
// with go install into .vukem/bin when it is missing or another version.
func ensureStudio(ctx context.Context, env *Env) (string, error) {
	binDir, err := filepath.Abs(env.path(filepath.Join(cacheDir, "bin")))
	if err != nil {
		return "", err
	}

	bin := filepath.Join(binDir, exe("vukem-studio"))

	if studioIsCurrent(ctx, bin) {
		return bin, nil
	}

	// go install pkg@version ignores the go.mod of the current directory;
	// GOBIN is absolute so the binary lands in the project's cache.
	args := studioInstallArgs()
	if err := toolEnv(ctx, env, []string{"GOBIN=" + binDir}, "go", args...); err != nil {
		return "", fmt.Errorf("install vukem-studio %s: %w (use --bin to run a binary you built)", studioVersion, err)
	}

	return bin, nil
}

// studioInstallArgs is the go command line that installs the pinned version.
func studioInstallArgs() []string {
	return []string{"install", studioModule + "/cmd/vukem-studio@" + studioVersion}
}

// studioIsCurrent reports whether bin exists and says it is studioVersion.
func studioIsCurrent(ctx context.Context, bin string) bool {
	var out bytes.Buffer

	cmd := exec.CommandContext(ctx, bin, "--version")
	cmd.Stdout = &out

	if err := cmd.Run(); err != nil {
		return false
	}

	return strings.Contains(out.String(), studioVersion)
}
