// Package cli implements the global `vukem` command: project scaffolding,
// generators, the dev server and delegation of app commands to the
// project's own binary (cmd/app). It works on files and never imports user
// code.
package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"strings"
	"time"
)

// Env is everything the CLI touches outside its arguments, so tests can run
// it in a temp directory with captured output.
type Env struct {
	// Dir is the project directory (the working directory).
	Dir    string
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
	// Interactive reports that Stdin is a terminal, so commands may prompt.
	Interactive bool
	// Getenv reads the process environment.
	Getenv func(string) (string, bool)
	// Now is the clock used for migration timestamps.
	Now func() time.Time
	// Version overrides the build-info version (tests).
	Version string
}

// ErrUsage marks invalid invocations; Run exits with code 2.
var ErrUsage = errors.New("usage error")

func usagef(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrUsage, fmt.Sprintf(format, args...))
}

type command struct {
	name, args, desc string
	flags            func(fs *flag.FlagSet) func(ctx context.Context, env *Env, args []string) error
}

func commands() []command {
	return []command{
		{"new", "<name>", "Create a project from the HTMX + SQLite starter", newProject},
		{"serve", "", "Run the app (HTTP, scheduler) and restart it when Go files change", serveCmd},
		{"build", "", "Build assets (Vite) and a production binary in bin/", buildCmd},
		{"version", "", "Print the vukem version", nil},
		{"install:auth", "", "Add login, registration, password reset and email verification", installAuth},
		{"install:api", "", "Add API tokens, /api/v1 routes, CORS and rate limiting", installAPI},
		{"install:billing", "", "Add Stripe subscriptions: plans, Checkout, Billing Portal, webhooks", installBilling},
		{"install:admin", "", "Add the admin panel at /admin (needs install:auth)", installAdmin},
		{
			"install:inertia",
			"",
			"Turn the HTMX starter into Inertia (--react, --vue or --svelte) with --force",
			installInertia,
		},
		{"boost:install", "", "Add agent guidelines, skills and the vukem-boost MCP server", boostInstall},
		{"key:generate", "", "Write a new APP_KEY to .env", keyGenerate},
		{"key:rotate", "", "Replace APP_KEY in .env and keep the old one in APP_PREVIOUS_KEYS", keyRotate},
		{"down", "", "Put the app in maintenance mode (503; --secret or --with-secret to bypass)", downCmd},
		{"up", "", "Bring the app out of maintenance mode", upCmd},
		{"make:controller", "<Name>", "Create a controller", makeController},
		{"make:model", "<Name>", "Create a model (with -m, -f, -c)", makeModel},
		{"make:migration", "<name>", "Create a migration and register it", makeMigration},
		{"make:middleware", "<Name>", "Create an HTTP middleware", makeMiddleware},
		{"make:request", "<Name>", "Create a validated request struct", makeRequest},
		{"make:command", "<Name>", "Create an app command and register it", makeCommand},
		{"make:provider", "<Name>", "Create a provider and register it", makeProvider},
		{"make:resource", "<Name>", "Create an API resource (JSON transformer) for a model", makeResource},
		{"make:factory", "<Name>", "Create a model factory (fake data for tests and seeders)", makeFactory},
		{"make:seeder", "<Name>", "Create a seeder and register it", makeSeeder},
		{"make:event", "<Name>", "Create an event type", makeEvent},
		{
			"make:listener",
			"<Name>",
			"Create an event listener and register it (--event Name [--async|--queued])",
			makeListener,
		},
		{"make:job", "<Name>", "Create a queued job and register its handler", makeJob},
		{"make:mail", "<Name>", "Create a mailable and its view", makeMail},
		{"make:policy", "<Name>", "Create an authorization policy and register it", makePolicy},
		{
			"make:admin-resource",
			"<Model>",
			"Create an admin panel resource and policy for a model and register them",
			makeAdminResource,
		},
		{"make:test", "<Name>", "Create a test using the fluent client", makeTest},
		{"make:proto", "<Name>", "Create a gRPC service .proto (and buf.yaml, buf.gen.yaml)", makeProto},
		{
			"codegen:rpc",
			"",
			"Generate Go and connect-go code from proto/ (buf or protoc, plugins via go tool)",
			codegenRPC,
		},
	}
}

// Run executes the command line and returns the process exit code:
// 0 success, 1 failure, 2 usage error.
func Run(ctx context.Context, args []string, env Env) int {
	if env.Getenv == nil {
		env.Getenv = func(string) (string, bool) { return "", false }
	}

	if env.Now == nil {
		env.Now = time.Now
	}

	err := run(ctx, args, &env)

	switch {
	case err == nil:
		return 0
	case errors.Is(err, ErrUsage):
		fmt.Fprintln(env.Stderr, err)
		fmt.Fprintln(env.Stderr, "Run 'vukem list' for the available commands.")

		return 2
	default:
		if code, ok := exitCode(err); ok {
			return code
		}

		fmt.Fprintln(env.Stderr, "error:", err)

		return 1
	}
}

func run(ctx context.Context, args []string, env *Env) error {
	if len(args) == 0 {
		return list(env)
	}

	name, rest := args[0], args[1:]

	switch name {
	case "list", "help", "-h", "--help":
		if len(rest) > 0 && name == "help" {
			return help(env, rest[0])
		}

		return list(env)
	case "version", "--version", "-v":
		fmt.Fprintln(env.Stdout, "vukem", version(env))
		return nil
	}

	for _, c := range commands() {
		if c.name != name || c.flags == nil {
			continue
		}

		fs := flag.NewFlagSet("vukem "+name, flag.ContinueOnError)
		fs.SetOutput(env.Stdout)
		fs.Usage = func() { printUsage(env, c, fs) }
		runFn := c.flags(fs)

		if err := fs.Parse(interleave(fs, rest)); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return nil
			}

			return usagef("%v", err)
		}

		return runFn(ctx, env, fs.Args())
	}

	// Not a vukem command: hand it to the project's app binary.
	return delegate(ctx, env, args)
}

// interleave moves flags (and the values of non-boolean flags) in front of
// positional arguments, so "make:model Post -m" works like
// "make:model -m Post" and "new blog --module x" like "new --module x blog".
func interleave(fs *flag.FlagSet, args []string) []string {
	var flags, pos []string

	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			pos = append(pos, args[i+1:]...)
			break
		}

		if !strings.HasPrefix(a, "-") || a == "-" {
			pos = append(pos, a)
			continue
		}

		flags = append(flags, a)

		name := strings.TrimLeft(a, "-")
		if strings.Contains(name, "=") {
			continue
		}

		if f := fs.Lookup(name); f != nil && !isBool(f) && i+1 < len(args) {
			i++
			flags = append(flags, args[i])
		}
	}

	return append(flags, pos...)
}

func isBool(f *flag.Flag) bool {
	b, ok := f.Value.(interface{ IsBoolFlag() bool })
	return ok && b.IsBoolFlag()
}

func list(env *Env) error {
	fmt.Fprintf(env.Stdout, "Vukem %s\n\nUsage:\n  vukem <command> [flags] [args]\n\n", version(env))

	width := 0
	for _, c := range commands() {
		width = max(width, len(strings.TrimSpace(c.name+" "+c.args)))
	}

	group := ""

	for _, c := range commands() {
		g, _, ok := strings.Cut(c.name, ":")
		if !ok {
			g = ""
		}

		if g != group {
			fmt.Fprintf(env.Stdout, " %s\n", g)
			group = g
		}

		fmt.Fprintf(env.Stdout, "  %-*s  %s\n", width, strings.TrimSpace(c.name+" "+c.args), c.desc)
	}

	fmt.Fprint(env.Stdout, "\nOther commands (migrate, route:list, about, your own) run in the app: vukem <command>.\n")

	return nil
}

func help(env *Env, name string) error {
	for _, c := range commands() {
		if c.name != name {
			continue
		}

		if c.flags == nil {
			fmt.Fprintf(env.Stdout, "Usage: vukem %s\n\n%s\n", c.name, c.desc)
			return nil
		}

		fs := flag.NewFlagSet("vukem "+name, flag.ContinueOnError)
		fs.SetOutput(env.Stdout)
		c.flags(fs)
		printUsage(env, c, fs)

		return nil
	}

	return usagef("unknown command %q", name)
}

func printUsage(env *Env, c command, fs *flag.FlagSet) {
	fmt.Fprintf(env.Stdout, "Usage: vukem %s\n\n%s\n", strings.TrimSpace(c.name+" [flags] "+c.args), c.desc)

	hasFlags := false

	fs.VisitAll(func(*flag.Flag) { hasFlags = true })

	if hasFlags {
		fmt.Fprintln(env.Stdout, "\nFlags:")
		fs.PrintDefaults()
	}
}

func version(env *Env) string {
	if env.Version != "" {
		return env.Version
	}

	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		return bi.Main.Version
	}

	return "devel"
}

func exitCode(err error) (int, bool) {
	if ee, ok := errors.AsType[*exec.ExitError](err); ok && ee.ExitCode() > 0 {
		return ee.ExitCode(), true
	}

	return 0, false
}

// exists reports whether a project-relative path exists.
func exists(env *Env, rel string) bool {
	_, err := os.Stat(env.path(rel))
	return err == nil
}

func (env *Env) path(rel string) string { return filepath.Join(env.Dir, rel) }

// tool runs an external program in the project directory with the CLI's
// output streams.
func tool(ctx context.Context, env *Env, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = env.Dir
	cmd.Stdin = env.Stdin
	cmd.Stdout = env.Stdout
	cmd.Stderr = env.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}

	return nil
}

func oneArg(args []string, what string) (string, error) {
	if len(args) != 1 || args[0] == "" {
		return "", usagef("expected exactly one %s", what)
	}

	return args[0], nil
}
