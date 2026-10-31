package vukem

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"text/tabwriter"

	"github.com/AlexGalhardo/vukem/config"
	"github.com/AlexGalhardo/vukem/database"
	"github.com/AlexGalhardo/vukem/openapi"
)

// Command is an application command compiled into the app binary
// (migrate, queue:work, user commands). The global `vukem` CLI delegates
// unknown commands to the app binary.
type Command interface {
	// Name is "namespace:action", e.g. "reports:send".
	Name() string
	// Description is shown by "list".
	Description() string
	// Flags defines flags on fs; it may be a no-op.
	Flags(fs *flag.FlagSet)
	// Run executes the command with the remaining positional args.
	Run(ctx context.Context, app *App, args []string) error
}

var errUsage = errors.New("usage error")

// rawArgsCommand is a built-in command that parses its own arguments
// (db:seed: the seeder flags depend on the seeders its names pick).
type rawArgsCommand interface{ rawArgs() }

// AddCommand registers commands. Call it from a provider's Register.
func (a *App) AddCommand(cmds ...Command) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.commands == nil {
		a.commands = map[string]Command{}
	}

	for _, c := range cmds {
		a.commands[c.Name()] = c
	}
}

// Commands returns registered commands sorted by name, built-ins included.
func (a *App) Commands() []Command {
	a.mu.Lock()
	all := maps.Clone(a.commands)
	a.mu.Unlock()

	if all == nil {
		all = map[string]Command{}
	}

	for _, c := range builtinCommands() {
		if _, overridden := all[c.Name()]; !overridden {
			all[c.Name()] = c
		}
	}

	return slices.SortedFunc(maps.Values(all), func(x, y Command) int { return strings.Compare(x.Name(), y.Name()) })
}

func runCommand(ctx context.Context, app *App, name string, args []string) error {
	if name == "help" || name == "list" || name == "-h" || name == "--help" {
		printCommands(app)
		return nil
	}

	var cmd Command

	for _, c := range app.Commands() {
		if c.Name() == name {
			cmd = c
			break
		}
	}

	if cmd == nil {
		fmt.Fprintf(app.stdout, "unknown command %q\n\n", name)
		printCommands(app)

		return errUsage
	}

	fset := flag.NewFlagSet(name, flag.ContinueOnError)
	fset.SetOutput(app.stdout)
	cmd.Flags(fset)

	if _, raw := cmd.(rawArgsCommand); raw {
		// "--" ends flag parsing, so every argument reaches Run as is.
		args = append([]string{"--"}, args...)
	}

	if err := fset.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}

		return errUsage
	}

	if app.Kind != KindWarmup {
		if err := app.Boot(ctx); err != nil {
			return err
		}
	}

	runErr := cmd.Run(ctx, app, fset.Args())

	return errors.Join(runErr, app.Shutdown(context.WithoutCancel(ctx)))
}

// isWarmupCommand reports whether a command runs in [KindWarmup]: it needs
// only the registered routes, never live connections.
func isWarmupCommand(name string) bool {
	switch name {
	case "codegen:routes", "codegen:client", "codegen:types", "openapi:export":
		return true
	default:
		return false
	}
}

func printCommands(app *App) {
	tw := tabwriter.NewWriter(app.stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "Available commands:")

	for _, c := range app.Commands() {
		fmt.Fprintf(tw, "  %s\t%s\n", c.Name(), c.Description())
	}

	_ = tw.Flush()
}

// CommandFunc builds a Command from a function.
type CommandFunc struct {
	CommandName string
	Desc        string
	DefineFlags func(fs *flag.FlagSet)
	Fn          func(ctx context.Context, app *App, args []string) error
}

// Name implements Command.
func (c CommandFunc) Name() string { return c.CommandName }

// Description implements Command.
func (c CommandFunc) Description() string { return c.Desc }

// Flags implements Command.
func (c CommandFunc) Flags(set *flag.FlagSet) {
	if c.DefineFlags != nil {
		c.DefineFlags(set)
	}
}

// Run implements Command.
func (c CommandFunc) Run(ctx context.Context, app *App, args []string) error {
	return c.Fn(ctx, app, args)
}

func builtinCommands() []Command {
	return slices.Concat(
		[]Command{
			routeListCommand(),
			aboutCommand(),
			keyGenerateCommand(),
			openAPIExportCommand(),
			codegenTypesCommand(),
			codegenRoutesCommand(),
			codegenClientCommand(),
			boostMCPCommand(),
			ssgGenerateCommand(),
			fuzzAPICommand(),
		},
		migrateCommands(),
		seedCommands(),
		queueCommands(),
		scheduleCommands(),
		ssrCommands(),
	)
}

func routeListCommand() Command {
	var asJSON *bool

	return CommandFunc{
		CommandName: "route:list",
		Desc:        "List the registered routes",
		DefineFlags: func(fs *flag.FlagSet) { asJSON = fs.Bool("json", false, "print JSON") },
		Fn: func(_ context.Context, app *App, _ []string) error {
			if _, err := app.Router.Handler(); err != nil {
				return err
			}

			routes := app.Router.Routes()
			if *asJSON {
				enc := json.NewEncoder(app.stdout)
				enc.SetIndent("", "  ")

				return enc.Encode(routes)
			}

			tw := tabwriter.NewWriter(app.stdout, 0, 4, 2, ' ', 0)
			fmt.Fprintln(tw, "METHOD\tPATH\tNAME\tMIDDLEWARE")

			for _, r := range routes {
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", r.Method, r.Host+r.Path, r.Name, strings.Join(r.Middleware, ", "))
			}

			return tw.Flush()
		},
	}
}

func openAPIExportCommand() Command {
	var prefix string

	return CommandFunc{
		CommandName: "openapi:export",
		Desc:        "Print the OpenAPI 3.1 document (vukem openapi:export > openapi.json)",
		DefineFlags: func(fs *flag.FlagSet) { openAPIPrefixFlag(fs, &prefix) },
		Fn: func(_ context.Context, app *App, _ []string) error {
			doc, err := openAPIDocument(app, prefix)
			if err != nil {
				return err
			}

			enc := json.NewEncoder(app.stdout)
			enc.SetIndent("", "  ")

			return enc.Encode(doc)
		},
	}
}

func codegenTypesCommand() Command {
	var zod *bool

	var prefix string

	var out *string

	return CommandFunc{
		CommandName: "codegen:types",
		Desc:        "Generate TypeScript types (and Zod schemas with -zod) from the OpenAPI document",
		DefineFlags: func(fs *flag.FlagSet) {
			zod = fs.Bool("zod", false, "also emit Zod schemas")
			out = fs.String("o", "", `write to this file ("-" or empty for stdout)`)
			openAPIPrefixFlag(fs, &prefix)
		},
		Fn: func(_ context.Context, app *App, _ []string) error {
			doc, err := openAPIDocument(app, prefix)
			if err != nil {
				return err
			}

			ts, err := openapi.TypeScript(doc, *zod)
			if err != nil {
				return err
			}

			if *out == "" || *out == "-" {
				_, err = io.WriteString(app.stdout, ts)
				return err
			}

			if err := os.MkdirAll(filepath.Dir(*out), 0o750); err != nil {
				return fmt.Errorf("codegen:types: %w", err)
			}

			return os.WriteFile(*out, []byte(ts), 0o600) //nolint:gosec // G703: path from the developer's own -o flag
		},
	}
}

func aboutCommand() Command {
	var asJSON *bool

	return CommandFunc{
		CommandName: "about",
		Desc:        "Show application, environment and security settings",
		DefineFlags: func(fs *flag.FlagSet) { asJSON = fs.Bool("json", false, "print JSON") },
		Fn: func(_ context.Context, app *App, _ []string) error {
			info := [][2]string{
				{"Name", app.Config.Name},
				{"Environment", string(app.Config.Env)},
				{"Debug", strconv.FormatBool(app.Config.Debug)},
				{"URL", app.Config.URL},
				{"Address", app.Addr()},
				{"Go", runtime.Version()},
				{"APP_KEY", app.Config.Key.String()},
				{"Trusted proxies", trustedProxiesStatus(app)},
				{"Telegram", telegramStatus(app)},
				{"Providers", strconv.Itoa(len(app.providers))},
				{"Routes", strconv.Itoa(len(app.Router.Routes()))},
			}

			if *asJSON {
				m := map[string]string{}
				for _, kv := range info {
					m[kv[0]] = kv[1]
				}

				return json.NewEncoder(app.stdout).Encode(m)
			}

			tw := tabwriter.NewWriter(app.stdout, 0, 4, 2, ' ', 0)
			for _, kv := range info {
				fmt.Fprintf(tw, "%s\t%s\n", kv[0], kv[1])
			}

			return tw.Flush()
		},
	}
}

// Process-level helpers, kept tiny so RunMain stays testable.

func osDirFS(dir string) fs.FS { return os.DirFS(dir) }

func osArgs() []string { return os.Args }

func lookupEnv(k string) (string, bool) { return os.LookupEnv(k) }

func stderr() io.Writer { return os.Stderr }

func signalContext() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
}

func trustedProxiesStatus(app *App) string {
	if len(app.proxies) == 0 {
		return "none"
	}

	nets := make([]string, len(app.proxies))
	for i, n := range app.proxies {
		nets[i] = n.String()
	}

	s := strings.Join(nets, ", ")
	if app.Config.ClientIPHeader != "" {
		s += " (client IP from " + app.Config.ClientIPHeader + ")"
	}

	if app.Config.TrustForwardedProto {
		s += " (X-Forwarded-Proto trusted)"
	}

	return s
}

func telegramStatus(app *App) string {
	if app.Telegram == nil || !app.Telegram.Configured() {
		return "off"
	}

	if !app.telegramLogs {
		return "on (token [redacted], log forwarding off)"
	}

	return "on (token [redacted])"
}

const keyGenerateName = "key:generate"

var errKeyGenerateArgs = fmt.Errorf("%s takes no arguments: %w", keyGenerateName, errUsage)

// ResealFunc re-encrypts the encrypted columns of one model after an
// APP_KEY rotation and reports how many values it rewrote; orm.Reseal[T]
// is one.
type ResealFunc func(ctx context.Context, db database.Executor, batch int) (int, error)

// ResealCommand returns the "crypt:reseal" command: after an APP_KEY
// rotation it re-encrypts, under the new key, the orm.Encrypted columns of
// each model whose ResealFunc it is given, so the old key can leave
// APP_PREVIOUS_KEYS. Core cannot discover models, so the app lists them:
//
//	app.AddCommand(vukem.ResealCommand(orm.Reseal[models.User], orm.Reseal[models.OAuthToken]))
//
// Flag -batch sets the rows per query (default orm.DefaultResealBatch).
// It is safe to run while the app serves traffic and to run again.
func ResealCommand(models ...ResealFunc) Command {
	var batch *int

	return CommandFunc{
		CommandName: "crypt:reseal",
		Desc:        "Re-encrypt encrypted columns under the current APP_KEY",
		DefineFlags: func(fs *flag.FlagSet) { batch = fs.Int("batch", 0, "rows per query (0: default)") },
		Fn: func(ctx context.Context, app *App, _ []string) error {
			db, err := app.DB()
			if err != nil {
				return err
			}

			total := 0

			for _, reseal := range models {
				n, err := reseal(ctx, db, *batch)
				total += n

				if err != nil {
					_, _ = fmt.Fprintf(app.stdout, "re-sealed %d values before the error\n", total)
					return err
				}
			}

			_, err = fmt.Fprintf(app.stdout, "re-sealed %d values\n", total)

			return err
		},
	}
}

// keyGenerateCommand lists key:generate and serves App.Call. RunMain runs
// it through runKeyGenerate before building the app, which fails closed
// without a valid APP_KEY, so it takes precedence over an app command with
// the same name.
func keyGenerateCommand() Command {
	return CommandFunc{
		CommandName: keyGenerateName,
		Desc:        "Print a new random APP_KEY (never writes .env)",
		Fn: func(_ context.Context, app *App, args []string) error {
			return writeNewKey(app.stdout, args)
		},
	}
}

// writeNewKey prints only the key, so APP_KEY=$(./app key:generate) is safe.
func writeNewKey(w io.Writer, args []string) error {
	if len(args) > 0 {
		return errKeyGenerateArgs
	}

	_, err := fmt.Fprintln(w, config.GenerateKey())

	return err
}

// runKeyGenerate is key:generate without an app: the key goes to the
// WithStdout writer in opts (os.Stdout by default) and every diagnostic to
// stderr, so a typo never ends up inside a captured APP_KEY.
func runKeyGenerate(args []string, opts []Option) error {
	o := options{stdout: os.Stdout, stderr: os.Stderr}
	for _, opt := range opts {
		if err := opt(&o); err != nil {
			return err
		}
	}

	fset := flag.NewFlagSet(keyGenerateName, flag.ContinueOnError)
	fset.SetOutput(o.stderr)
	keyGenerateCommand().Flags(fset)

	if err := fset.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}

		return errUsage
	}

	err := writeNewKey(o.stdout, fset.Args())
	if errors.Is(err, errUsage) {
		fmt.Fprintln(o.stderr, err)
	}

	return err
}
