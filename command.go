package vukem

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/AlexGalhardo/vukem/auth/hash"
	"github.com/AlexGalhardo/vukem/config"
	"github.com/AlexGalhardo/vukem/database"
	"github.com/AlexGalhardo/vukem/inertia"
	"github.com/AlexGalhardo/vukem/openapi"
	"github.com/AlexGalhardo/vukem/router"
	"github.com/AlexGalhardo/vukem/ssg"
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
			hashMakeCommand(),
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
	var (
		asJSON               *bool
		method, path, naming *string
	)

	return CommandFunc{
		CommandName: "route:list",
		Desc:        "List the registered routes",
		DefineFlags: func(fs *flag.FlagSet) {
			asJSON = fs.Bool("json", false, "print JSON")
			method = fs.String("method", "", "only routes with this HTTP method (ANY matches Any routes)")
			path = fs.String("path", "", "only routes whose host and path contain this text")
			naming = fs.String("name", "", "only routes whose name contains this text")
		},
		Fn: func(_ context.Context, app *App, _ []string) error {
			if _, err := app.Router.Handler(); err != nil {
				return err
			}

			routes := filterRoutes(app.Router.Routes(), *method, *path, *naming)
			if *asJSON {
				enc := json.NewEncoder(app.stdout)
				enc.SetIndent("", "  ")

				return enc.Encode(routes)
			}

			if len(routes) == 0 {
				fmt.Fprintln(app.stdout, "No routes match.")
				return nil
			}

			summaries := make([]string, len(routes))
			hasSummary := false

			for i, r := range routes {
				if s, ok := r.Doc.(router.Summarizer); ok {
					summaries[i] = s.RouteSummary()
					hasSummary = hasSummary || summaries[i] != ""
				}
			}

			tw := tabwriter.NewWriter(app.stdout, 0, 4, 2, ' ', 0)
			if hasSummary {
				fmt.Fprintln(tw, "METHOD\tPATH\tNAME\tSUMMARY\tMIDDLEWARE")
			} else {
				fmt.Fprintln(tw, "METHOD\tPATH\tNAME\tMIDDLEWARE")
			}

			for i, r := range routes {
				if hasSummary {
					fmt.Fprintf(
						tw,
						"%s\t%s\t%s\t%s\t%s\n",
						r.Method,
						r.Host+r.Path,
						r.Name,
						summaries[i],
						strings.Join(r.Middleware, ", "),
					)
				} else {
					fmt.Fprintf(
						tw,
						"%s\t%s\t%s\t%s\n",
						r.Method,
						r.Host+r.Path,
						r.Name,
						strings.Join(r.Middleware, ", "),
					)
				}
			}

			return tw.Flush()
		},
	}
}

// filterRoutes keeps the routes matching every non-empty filter: method is
// compared without case, path and name by substring.
func filterRoutes(routes []router.RouteInfo, method, path, name string) []router.RouteInfo {
	out := make([]router.RouteInfo, 0, len(routes))

	for _, r := range routes {
		if (method == "" || strings.EqualFold(r.Method, method)) &&
			strings.Contains(r.Host+r.Path, path) && strings.Contains(r.Name, name) {
			out = append(out, r)
		}
	}

	return out
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
	var ifMissing *bool

	return CommandFunc{
		CommandName: keyGenerateName,
		Desc:        "Print a new random APP_KEY, or write .env with --if-missing",
		DefineFlags: func(fs *flag.FlagSet) {
			ifMissing = fs.Bool("if-missing", false, "write APP_KEY into .env when it is empty; never print the key")
		},
		Fn: func(_ context.Context, app *App, args []string) error {
			return dispatchKeyGenerate(app.Stdout(), app.Stderr(), args, boolOf(ifMissing))
		},
	}
}

func boolOf(v *bool) bool { return v != nil && *v }

var errHashMakeArgs = fmt.Errorf(
	"hash:make takes no arguments: the password is read from the environment or stdin, never from argv: %w", errUsage)

// hashMakeCommand is hash:make: it prints the hash.Default (argon2id) hash of a password, in the format the session
// guard checks, for scripts that set or reset a password with SQL. The password comes from the environment variable
// named by -env (default PASSWORD) or, with -stdin, from the first line of stdin, so it stays out of argv, the shell
// history and the process list. Only the hash is printed.
func hashMakeCommand() Command {
	var (
		envName   *string
		fromStdin *bool
	)

	return CommandFunc{
		CommandName: "hash:make",
		Desc:        "Print the argon2id hash of $PASSWORD (or of stdin with -stdin)",
		DefineFlags: func(fs *flag.FlagSet) {
			envName = fs.String("env", "PASSWORD", "environment variable that holds the password")
			fromStdin = fs.Bool("stdin", false, "read the password from the first line of stdin instead")
		},
		Fn: func(_ context.Context, app *App, args []string) error {
			if len(args) > 0 {
				return errHashMakeArgs
			}

			password := os.Getenv(*envName)

			if *fromStdin {
				line, err := bufio.NewReader(app.stdin).ReadString('\n')
				if err != nil && !errors.Is(err, io.EOF) {
					return fmt.Errorf("hash:make: read stdin: %w", err)
				}

				password = strings.TrimRight(line, "\r\n")
			}

			if password == "" {
				source := "$" + *envName
				if *fromStdin {
					source = "stdin"
				}

				return fmt.Errorf("hash:make: no password in %s", source)
			}

			out, err := hash.Default().Make(password)
			if err != nil {
				return fmt.Errorf("hash:make: %w", err)
			}

			_, err = fmt.Fprintln(app.stdout, out)

			return err
		},
	}
}

// dispatchKeyGenerate prints a new key, or with ifMissing writes .env and
// prints nothing. --if-missing is not rotation: a key that is already set
// stays the key that sealed current sessions.
func dispatchKeyGenerate(stdout, stderr io.Writer, args []string, ifMissing bool) error {
	if len(args) > 0 {
		return errKeyGenerateArgs
	}

	if ifMissing {
		dir, err := os.Getwd()
		if err != nil {
			return err
		}

		return ensureAppKeyFile(dir, stderr)
	}

	_, err := fmt.Fprintln(stdout, config.GenerateKey())

	return err
}

// ensureAppKeyFile writes a new APP_KEY into dir/.env when the key is
// missing or empty. An existing key and every other variable stay as they
// are. The key is never written to errw; that writer only gets a status
// line, so a CI log and a captured stdout stay free of the secret.
func ensureAppKeyFile(dir string, errw io.Writer) error {
	if errw == nil {
		errw = io.Discard
	}

	p := filepath.Join(dir, ".env")

	src, err := os.ReadFile(p)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}

	text := string(src)

	entries, err := config.ParseEnv(strings.NewReader(text))
	if err != nil {
		return err
	}

	for _, e := range slices.Backward(entries) {
		if e.Key == "APP_KEY" {
			if strings.TrimSpace(e.Value) != "" {
				fmt.Fprintln(errw, "APP_KEY already set")
				return nil
			}

			break
		}
	}

	text = putAppKey(text, config.GenerateKey())
	if err := writeDotEnv(p, []byte(text)); err != nil {
		return err
	}

	fmt.Fprintln(errw, "APP_KEY written to .env")

	return nil
}

// appKeyLine mirrors the lines config.ParseEnv accepts (indent, optional
// export, spaces around "="). Groups: lead, name and "=", value.
var appKeyLine = regexp.MustCompile(`(?m)^([ \t]*(?:export[ \t]+)?)(APP_KEY[ \t]*=[ \t]*)([^\r\n]*)`)

// putAppKey replaces the value of every APP_KEY line, or appends one.
func putAppKey(text, value string) string {
	if appKeyLine.MatchString(text) {
		return appKeyLine.ReplaceAllString(text, "${1}${2}"+value)
	}

	if text != "" && !strings.HasSuffix(text, "\n") {
		text += "\n"
	}

	return text + "APP_KEY=" + value + "\n"
}

// writeDotEnv replaces path through a temp file in the same directory.
// On POSIX the file mode is 0600. Windows has no equivalent ACL here:
// os.Chmod only toggles the read-only bit, so the mode is not a secret
// guarantee on that system.
func writeDotEnv(path string, data []byte) error {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}

	f, err := os.CreateTemp(filepath.Dir(path), ".env.tmp-*")
	if err != nil {
		return err
	}

	tmp := f.Name()

	_, werr := f.Write(data)
	cerr := f.Chmod(0o600)
	serr := f.Sync()
	clerr := f.Close()

	err = errors.Join(werr, cerr, serr, clerr)
	if err == nil {
		err = os.Rename(tmp, path)
	}

	if err != nil {
		_ = os.Remove(tmp)
	}

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

	cmd := keyGenerateCommand()
	cmd.Flags(fset)

	if err := fset.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}

		return errUsage
	}

	err := cmd.Run(context.Background(), &App{stdout: o.stdout, stderr: o.stderr}, fset.Args())
	if errors.Is(err, errUsage) {
		fmt.Fprintln(o.stderr, err)
	}

	return err
}

func ssgGenerateCommand() Command {
	var (
		out     string
		paths   []string
		noClean bool
	)

	return CommandFunc{
		CommandName: "ssg:generate",
		Desc:        "Prerender handler paths to static HTML (not Inertia SSR)",
		DefineFlags: func(fs *flag.FlagSet) {
			paths = nil
			noClean = false
			fs.StringVar(&out, "out", "dist", "directory to write static files into")
			fs.BoolVar(&noClean, "no-clean", false, "write in place and keep files from earlier runs")
			fs.Func("path", "absolute URL path to prerender (repeatable)", func(s string) error {
				paths = append(paths, s)

				return nil
			})
		},
		Fn: func(ctx context.Context, app *App, args []string) error {
			if len(args) > 0 {
				return fmt.Errorf("ssg:generate: unexpected argument %q: %w", args[0], errUsage)
			}

			if len(paths) == 0 {
				return fmt.Errorf("ssg:generate: at least one -path is required: %w", errUsage)
			}

			h, err := app.Handler()
			if err != nil {
				return err
			}

			pages := make([]ssg.Page, len(paths))
			for i, p := range paths {
				pages[i] = ssg.Page{Path: p}
			}

			report, err := ssg.Generate(ctx, h, pages, ssg.Options{OutDir: out, NoClean: noClean})
			if err != nil {
				return fmt.Errorf("ssg:generate: %w", err)
			}

			for _, f := range report.Files {
				if _, err := fmt.Fprintf(app.Stdout(), "wrote %s (%d bytes)\n", f.Path, f.Bytes); err != nil {
					return err
				}
			}

			return nil
		},
	}
}

func ssrCommands() []Command { return []Command{ssrStartCommand(), ssrCheckCommand()} }

// ssrStartCommand supervises the Inertia SSR server: it runs the bundle with
// node and restarts it when it crashes, until the command is stopped.
func ssrStartCommand() Command {
	var (
		bundle, jsRuntime *string
		delay             *time.Duration
	)

	return CommandFunc{
		CommandName: "ssr:start",
		Desc:        "Run the Inertia SSR server (node bootstrap/ssr/ssr.js) and restart it if it crashes",
		DefineFlags: func(fs *flag.FlagSet) {
			bundle = fs.String("bundle", "bootstrap/ssr/ssr.js", "SSR bundle built by vite build --ssr")
			jsRuntime = fs.String("runtime", "node", "JavaScript runtime that runs the bundle")
			delay = fs.Duration("restart-delay", time.Second, "wait before restarting a crashed server")
		},
		Fn: func(ctx context.Context, app *App, _ []string) error {
			if err := checkSSRBundle("ssr:start", *bundle); err != nil {
				return err
			}

			return app.superviseSSR(ctx, ssrProcess{*bundle, *jsRuntime, *delay, false, ssrMinUptime})
		},
	}
}

// ssrProcess is how ssr:start and `serve -with-ssr` run the SSR server.
type ssrProcess struct {
	bundle, runtime string
	delay           time.Duration
	// always restarts after a clean exit too: under serve the app keeps
	// rendering pages, so the server must come back.
	always bool
	// minUptime is ssrMinUptime outside tests.
	minUptime time.Duration
}

// ssrMinUptime separates a bundle that never served (exits at once) from a
// server stopped with POST /shutdown.
const ssrMinUptime = 2 * time.Second

// errSSRNoServer reports an SSR bundle that exited without serving.
var errSSRNoServer = errors.New("ssr:start: the SSR bundle did not start a server " +
	"(call createInertiaApp at the top level of resources/js/ssr.ts(x), do not export it, and rebuild)")

func checkSSRBundle(cmd, bundle string) error {
	if _, err := os.Stat(bundle); err != nil {
		return fmt.Errorf("%s: SSR bundle %s not found (run npm run build): %w", cmd, bundle, err)
	}

	return nil
}

// superviseSSR runs the SSR bundle and restarts it when it exits, until ctx
// is done.
func (a *App) superviseSSR(ctx context.Context, p ssrProcess) error {
	for {
		cmd := exec.CommandContext(ctx, p.runtime, p.bundle) //nolint:gosec // G204: developer-supplied flags
		cmd.Stdout, cmd.Stderr = a.stdout, os.Stderr
		cmd.WaitDelay = 5 * time.Second

		start := time.Now()
		err := cmd.Run()

		// A bundle that exits cleanly at once never started a server (an
		// entry that exports createInertiaApp instead of calling it): fail
		// loudly rather than exit 0 with no output.
		if ctx.Err() == nil && err == nil && !p.always && time.Since(start) < p.minUptime {
			return fmt.Errorf("%w: %s exited at once", errSSRNoServer, p.bundle)
		}

		// Stopped (Ctrl+C), or a clean exit (POST /shutdown) under ssr:start.
		if ctx.Err() != nil || err == nil && !p.always {
			return nil //nolint:nilerr // the process was killed on purpose
		}

		a.Log.WarnContext(ctx, "ssr: server exited, restarting", "err", err, "delay", p.delay)

		select {
		case <-ctx.Done():
			return nil
		case <-time.After(p.delay):
		}
	}
}

// serveFlags parses the flags of the serve command: -with-ssr adds the
// "ssr" runner, which supervises the SSR server like ssr:start.
func serveFlags(app *App, args []string) error {
	flags := flag.NewFlagSet("serve", flag.ContinueOnError)
	flags.SetOutput(app.stderr)

	withSSR := flags.Bool("with-ssr", false, "also run and supervise the Inertia SSR server (like ssr:start)")
	bundle := flags.String("ssr-bundle", "bootstrap/ssr/ssr.js", "SSR bundle built by vite build --ssr")
	jsRuntime := flags.String("ssr-runtime", "node", "JavaScript runtime that runs the SSR bundle")
	delay := flags.Duration("ssr-restart-delay", time.Second, "wait before restarting the SSR server")

	if err := flags.Parse(args); err != nil {
		return errUsage
	}

	if !*withSSR {
		return nil
	}

	if err := checkSSRBundle("serve -with-ssr", *bundle); err != nil {
		return err
	}

	p := ssrProcess{*bundle, *jsRuntime, *delay, true, ssrMinUptime}
	app.AddRunner("ssr", RunnerFunc(func(ctx context.Context) error { return app.superviseSSR(ctx, p) }))

	return nil
}

func ssrCheckCommand() Command {
	var url *string

	return CommandFunc{
		CommandName: "ssr:check",
		Desc:        "Check that the Inertia SSR server answers /health (exit 1 otherwise)",
		DefineFlags: func(fs *flag.FlagSet) {
			url = fs.String("url", inertia.DefaultSSRURL, "SSR server URL")
		},
		Fn: func(ctx context.Context, app *App, _ []string) error {
			ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()

			req, err := http.NewRequestWithContext(
				ctx,
				http.MethodGet,
				strings.TrimSuffix(*url, "/")+"/health",
				http.NoBody,
			)
			if err != nil {
				return err
			}

			res, err := http.DefaultClient.Do(req)
			if err != nil {
				return fmt.Errorf("ssr:check: %w", err)
			}
			defer res.Body.Close()

			if res.StatusCode != http.StatusOK {
				return errors.New("ssr:check: " + res.Status)
			}

			_, err = fmt.Fprintln(app.stdout, "SSR server is healthy")

			return err
		},
	}
}
