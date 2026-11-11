// Command vukem-studio is the read-only data browser of a Vukem project. Run
// it from the project (or with --dir); `vukem studio` installs and starts it.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strconv"
	"time"

	"github.com/AlexGalhardo/vukem/contrib/studio"
)

// version is set at release time with -ldflags "-X main.version=v0.1.0".
var version = "v0.0.0-dev" //nolint:gochecknoglobals // set by the linker

// DefaultPort is Studio's port; Drizzle Studio uses 4983, so this avoids a clash.
const defaultPort = 4984

// Exit codes: 0 stopped by Ctrl+C, 1 an error, 2 bad usage or a security refusal.
const (
	exitOK       = 0
	exitError    = 1
	exitRefusal  = 2
	readTimeout  = 10 * time.Second
	shutdownWait = 5 * time.Second
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	code := run(ctx, os.Args[1:], os.Stdout, os.Stderr, os.LookupEnv)

	stop()
	os.Exit(code)
}

type options struct {
	dir             string
	port            int
	noOpen          bool
	allowProduction bool
	databaseURL     string
	showVersion     bool
	write           bool
	refresh         time.Duration
	mongoURL        string
	mongoDatabase   string
	redisURL        string
	mongoWatch      bool
	redisNotify     bool
}

func parseFlags(args []string, stderr io.Writer) (options, error) {
	var o options

	fs := flag.NewFlagSet("vukem-studio", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&o.dir, "dir", ".", "project directory")
	fs.IntVar(&o.port, "port", defaultPort, "port on 127.0.0.1 (0 picks a free one)")
	fs.BoolVar(&o.noOpen, "no-open", false, "do not open the browser")
	fs.BoolVar(&o.allowProduction, "allow-production", false, "allow APP_ENV=production")
	fs.StringVar(&o.databaseURL, "database-url", "", "override DATABASE_URL")
	fs.BoolVar(&o.showVersion, "version", false, "print the version and exit")
	fs.BoolVar(&o.write, "write", false, "enable editing (default: read-only)")
	fs.DurationVar(&o.refresh, "refresh", 0, "live refresh interval (default 2s; negative turns it off; minimum 500ms)")
	fs.StringVar(&o.mongoURL, "mongo-url", "", "override MONGO_URL")
	fs.StringVar(&o.mongoDatabase, "mongo-database", "", "override MONGO_DATABASE")
	fs.StringVar(&o.redisURL, "redis-url", "", "override REDIS_URL")
	fs.BoolVar(&o.mongoWatch, "mongo-watch", false, "use change streams instead of polling (not implemented yet)")
	fs.BoolVar(
		&o.redisNotify,
		"redis-notify",
		false,
		"use keyspace notifications instead of polling (not implemented yet)",
	)

	if err := fs.Parse(args); err != nil {
		return o, err
	}

	if fs.NArg() > 0 {
		return o, fmt.Errorf("unexpected argument %q", fs.Arg(0))
	}

	if o.port < 0 || o.port > 65535 {
		return o, fmt.Errorf("--port must be between 0 and 65535, not %d", o.port)
	}

	return o, nil
}

// run is the testable body of main.
func run(
	ctx context.Context, args []string, stdout, stderr io.Writer, getenv func(string) (string, bool),
) int {
	o, err := parseFlags(args, stderr)
	if err != nil {
		if !errors.Is(err, flag.ErrHelp) {
			fmt.Fprintln(stderr, "vukem-studio:", err)
		}

		return exitRefusal
	}

	if o.showVersion {
		fmt.Fprintln(stdout, "vukem-studio", version)
		return exitOK
	}

	env, err := studio.LoadEnv(o.dir, getenv)
	if err != nil {
		fmt.Fprintln(stderr, "vukem-studio:", err)
		return exitError
	}

	// Before any connection is opened.
	if env.Production && !o.allowProduction {
		fmt.Fprintln(stderr,
			"vukem-studio: refusing to start with APP_ENV=production (pass --allow-production to override)")

		return exitRefusal
	}

	if o.databaseURL != "" {
		env.DatabaseURL, env.DatabaseSet = o.databaseURL, true
	}

	env.MongoURL, env.MongoDatabase = override(env.MongoURL, o.mongoURL), override(env.MongoDatabase, o.mongoDatabase)
	env.RedisURL = override(env.RedisURL, o.redisURL)

	if o.mongoWatch || o.redisNotify {
		fmt.Fprintln(stderr, "vukem-studio: --mongo-watch and --redis-notify are not implemented yet: using polling")
	}

	sources, err := openSources(ctx, o, env)
	if err != nil {
		fmt.Fprintln(stderr, "vukem-studio:", err)
		return exitError
	}

	if len(sources) == 0 {
		fmt.Fprintln(stderr, "vukem-studio: no data source is configured (set DATABASE_URL, MONGO_URL or REDIS_URL)")
		return exitRefusal
	}

	s, err := studio.New(studio.Config{
		Sources: sources, Version: version, Write: o.write, Refresh: o.refresh,
		Log: slog.New(slog.NewTextHandler(stderr, nil)),
	})
	if err != nil {
		closeAll(sources)
		fmt.Fprintln(stderr, "vukem-studio:", err)

		return exitError
	}

	defer s.Close()

	return serve(ctx, s, sources, o, stdout, stderr)
}

func override(current, fromFlag string) string {
	if fromFlag != "" {
		return fromFlag
	}

	return current
}

func closeAll(sources []studio.Source) {
	for _, src := range sources {
		_ = src.Close()
	}
}

// openSources opens what is configured. The SQL source is skipped, quietly,
// when DATABASE_URL was never set, its default SQLite file does not exist and
// another source is configured (an app that only uses MongoDB or Redis).
func openSources(ctx context.Context, o options, env studio.Env) ([]studio.Source, error) {
	var sources []studio.Source

	fail := func(err error) ([]studio.Source, error) {
		closeAll(sources)
		return nil, err
	}

	otherSource := env.MongoURL != "" || env.RedisURL != ""

	sql, err := studio.OpenSQL(ctx, "db", o.dir, env.DatabaseURL)

	switch {
	case err == nil:
		sources = append(sources, sql)
	case env.DatabaseSet || !otherSource:
		return fail(err)
	}

	if env.MongoURL != "" {
		m, err := studio.OpenMongo(ctx, "mongo", env.MongoURL, env.MongoDatabase, o.write)
		if err != nil {
			return fail(err)
		}

		sources = append(sources, m)
	}

	if env.RedisURL != "" {
		r, err := studio.OpenRedis(ctx, "redis", env.RedisURL)
		if err != nil {
			return fail(err)
		}

		sources = append(sources, r)
	}

	return sources, nil
}

func serve(ctx context.Context, s *studio.Studio, sources []studio.Source, o options, stdout, stderr io.Writer) int {
	var lc net.ListenConfig

	ln, err := lc.Listen(ctx, "tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(o.port)))
	if err != nil {
		fmt.Fprintf(stderr, "vukem-studio: cannot listen on port %d: %v (try --port 0 for a free one)\n", o.port, err)
		return exitError
	}

	startURL := fmt.Sprintf("http://%s/?token=%s", ln.Addr(), s.Token())

	mode := "(read-only)"
	if o.write {
		mode = "(WRITE ENABLED)"
	}

	fmt.Fprintf(stdout, "vukem-studio %s %s\n", version, mode)

	for _, src := range sources {
		fmt.Fprintf(stdout, "  %-5s %s\n", src.Kind(), src.Label())
	}

	fmt.Fprintf(stdout, "  open  %s\nPress Ctrl+C to stop.\n", startURL)

	if !o.noOpen {
		openBrowser(ctx, startURL)
	}

	srv := &http.Server{Handler: s.Handler(), ReadHeaderTimeout: readTimeout}
	done := make(chan error, 1)

	go func() { done <- srv.Serve(ln) }()

	select {
	case err := <-done:
		fmt.Fprintln(stderr, "vukem-studio:", err)
		return exitError
	case <-ctx.Done():
	}

	shutCtx, cancel := context.WithTimeout(context.Background(), shutdownWait)
	defer cancel()

	_ = srv.Shutdown(shutCtx)

	return exitOK
}

// openBrowser starts the system browser; a failure is ignored.
func openBrowser(ctx context.Context, u string) {
	var cmd *exec.Cmd

	switch runtime.GOOS {
	case "windows":
		cmd = exec.CommandContext(ctx, "cmd", "/c", "start", "", u)
	case "darwin":
		cmd = exec.CommandContext(ctx, "open", u)
	default:
		cmd = exec.CommandContext(ctx, "xdg-open", u)
	}

	_ = cmd.Start()
}
