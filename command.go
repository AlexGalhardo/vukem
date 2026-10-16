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
	"runtime"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"text/tabwriter"

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

	if err := fset.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}

		return errUsage
	}

	if err := app.Boot(ctx); err != nil {
		return err
	}

	runErr := cmd.Run(ctx, app, fset.Args())

	return errors.Join(runErr, app.Shutdown(context.WithoutCancel(ctx)))
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
		[]Command{routeListCommand(), aboutCommand(), openAPIExportCommand(), codegenTypesCommand(), boostMCPCommand()},
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
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", r.Method, r.Path, r.Name, strings.Join(r.Middleware, ", "))
			}

			return tw.Flush()
		},
	}
}

func openAPIExportCommand() Command {
	return CommandFunc{
		CommandName: "openapi:export",
		Desc:        "Print the OpenAPI 3.1 document (vukem openapi:export > openapi.json)",
		Fn: func(_ context.Context, app *App, _ []string) error {
			doc, err := app.OpenAPI()
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

	var out *string

	return CommandFunc{
		CommandName: "codegen:types",
		Desc:        "Generate TypeScript types (and Zod schemas with -zod) from the OpenAPI document",
		DefineFlags: func(fs *flag.FlagSet) {
			zod = fs.Bool("zod", false, "also emit Zod schemas")
			out = fs.String("o", "", "write to this file instead of stdout")
		},
		Fn: func(_ context.Context, app *App, _ []string) error {
			doc, err := app.OpenAPI()
			if err != nil {
				return err
			}

			ts, err := openapi.TypeScript(doc, *zod)
			if err != nil {
				return err
			}

			if *out == "" {
				_, err = io.WriteString(app.stdout, ts)
				return err
			}

			return os.WriteFile(*out, []byte(ts), 0o600)
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

func telegramStatus(app *App) string {
	if app.Telegram == nil {
		return "off"
	}

	return "on (token [redacted])"
}
