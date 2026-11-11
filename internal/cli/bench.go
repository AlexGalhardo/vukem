package cli

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/AlexGalhardo/vukem/loadtest"
	"github.com/AlexGalhardo/vukem/loadtest/liveui"
)

// repeatFlag collects every occurrence of a repeatable flag, in order.
type repeatFlag []string

func (r *repeatFlag) String() string { return strings.Join(*r, ", ") }

func (r *repeatFlag) Set(v string) error {
	*r = append(*r, v)

	return nil
}

// benchOptions are the flags of bench that outlive flag parsing.
type benchOptions struct {
	concurrency, connections, maxWorkers int
	rate, method, body, bodyFile         string
	duration, timeout                    time.Duration
	requests                             uint64
	headers, allowRemote                 repeatFlag
	noKeepAlive, http2, asJSON, quiet    bool
	out                                  string

	// Gates, for a URL and for the project's scenarios.
	threshold            repeatFlag
	save, compare, toler string

	// The live dashboard and the HTML report.
	ui, noOpen bool
	uiPort     int
	report     string

	// Flags that only the project's scenarios (bench:run in the app binary) understand: the CLI forwards them.
	scenario                     repeatFlag
	model, stages, url, database string
	seed, list, yes              bool
}

// benchCmd load tests a URL with the loadtest engine.
func benchCmd(set *flag.FlagSet) func(context.Context, *Env, []string) error {
	var o benchOptions

	set.IntVar(&o.concurrency, "c", 10, "closed model: concurrent workers")
	set.StringVar(&o.rate, "rate", "", "open model: arrival rate, e.g. 200/s or 3000/1m (disables -c)")
	set.DurationVar(&o.duration, "d", 10*time.Second, "test duration (max 1h)")
	set.Uint64Var(&o.requests, "n", 0, "stop after n requests (0 = no limit)")
	set.StringVar(&o.method, "m", http.MethodGet, "HTTP method")
	set.Var(&o.headers, "H", "request header \"Name: value\" (repeatable)")
	set.StringVar(&o.body, "body", "", "request body")
	set.StringVar(&o.bodyFile, "body-file", "", "read the request body from a file")
	set.DurationVar(&o.timeout, "timeout", 30*time.Second, "per-request timeout")
	set.IntVar(&o.connections, "connections", 0, "idle connections kept per host (default: at least 100)")
	set.IntVar(&o.maxWorkers, "max-workers", 1000, "open model worker cap")
	set.BoolVar(&o.noKeepAlive, "no-keepalive", false, "open a new connection per request")
	set.BoolVar(&o.http2, "http2", false, "negotiate HTTP/2 over TLS")
	set.Var(&o.allowRemote, "allow-remote", "allow a non-loopback host (repeatable)")
	set.BoolVar(&o.asJSON, "json", false, "print the summary as JSON instead of text")
	set.StringVar(&o.out, "o", "", "also write the JSON summary to file")
	set.BoolVar(&o.quiet, "quiet", false, "no progress line while running")
	set.Var(&o.threshold, "threshold", "fail (exit 3) unless the expression holds, such as p95<200ms (repeatable)")
	set.StringVar(&o.save, "save", "", "write the JSON summary to a file")
	set.StringVar(&o.compare, "compare", "", "fail (exit 4) on regression against a saved summary")
	set.StringVar(&o.toler, "tolerance", "10%", "allowed regression for -compare")
	set.Var(&o.scenario, "scenario", "project scenarios: run only this scenario (repeatable)")
	set.StringVar(&o.model, "model", "closed", "project scenarios: open or closed")
	set.StringVar(&o.stages, "stages", "", "project scenarios: ramp, such as \"30s:50,2m:50,30s:0\"")
	set.StringVar(&o.url, "url", "", "project scenarios: attack an already running app instead of booting one")
	set.StringVar(&o.database, "database-url", "", "project scenarios: database for the in-process app")
	set.BoolVar(&o.seed, "seed", false, "project scenarios: run the registered seeders after migrating")
	set.BoolVar(&o.list, "list", false, "project scenarios: list them and exit")
	set.BoolVar(&o.ui, "ui", false, "serve a read-only live dashboard on 127.0.0.1 and keep running until Ctrl+C")
	set.IntVar(&o.uiPort, "ui-port", 0, "dashboard port (0 = a free port)")
	set.BoolVar(&o.noOpen, "no-open", false, "do not try to open the browser")
	set.StringVar(&o.report, "report", "", "write a self-contained HTML report when the run ends")
	set.BoolVar(&o.yes, "yes", false, "project scenarios: confirm a -database-url that is not a temporary SQLite file")

	return func(ctx context.Context, env *Env, args []string) error {
		explicit := map[string]bool{}
		set.Visit(func(f *flag.Flag) { explicit[f.Name] = true })

		return o.run(ctx, env, set, args, explicit)
	}
}

// run is bench: a URL is load tested here, no URL hands the scenarios of the project to the app binary.
func (o *benchOptions) run(
	ctx context.Context, env *Env, set *flag.FlagSet, args []string, explicit map[string]bool,
) error {
	if err := o.checkUI(env, explicit); err != nil {
		return err
	}

	// No URL: run the scenarios of the project, which only its app binary can load.
	if len(args) == 0 {
		urlOnly := []string{
			"n",
			"m",
			"H",
			"body",
			"body-file",
			"connections",
			"no-keepalive",
			"http2",
			"o",
			"quiet",
		}
		if name := firstSet(explicit, urlOnly...); name != "" {
			return usagef("-%s needs a URL: bench:run in the project does not take it", name)
		}

		return delegate(ctx, env, append([]string{"bench:run"}, forwardedFlags(set)...))
	}

	if name := firstSet(
		explicit,
		"scenario",
		"model",
		"stages",
		"url",
		"database-url",
		"seed",
		"list",
		"yes",
	); name != "" {
		return usagef("-%s is for the project's scenarios: run vukem bench without a URL", name)
	}

	gate, err := o.gate(env)
	if err != nil {
		return err
	}

	cfg, req, err := o.build(env, args, explicit)
	if err != nil {
		return err
	}

	r, err := loadtest.Start(ctx, cfg, loadtest.Static(req))
	if err != nil {
		return usagef("%v", err)
	}

	watch, err := o.watch(ctx, env, r, req)
	if err != nil {
		r.Stop()

		return err
	}

	sum := collectBench(env, r, o.interactive(env), watch)

	var reportErr error

	if watch != nil {
		defer watch.Close()

		reportErr = watch.Finish(sum)
	}

	if o.out != "" {
		var b bytes.Buffer
		if err := loadtest.WriteJSON(&b, sum); err != nil {
			return err
		}

		if err := os.WriteFile(env.path(o.out), b.Bytes(), 0o600); err != nil {
			return err
		}
	}

	report := loadtest.WriteText
	if o.asJSON {
		report = loadtest.WriteJSON
	}

	if err := report(env.Stdout, sum); err != nil {
		return err
	}

	gateErr := gateError(gate.Apply(env.Stderr, sum))

	// With a dashboard the process stays up, after the report and the gates, until the user quits.
	if watch != nil {
		watch.Hold(ctx)
	}

	if reportErr != nil {
		return reportErr
	}

	return gateErr
}

// checkUI validates the dashboard flags: -ui is for a person at a terminal, not for a pipe or CI.
func (o *benchOptions) checkUI(env *Env, explicit map[string]bool) error {
	if !o.ui {
		if name := firstSet(explicit, "ui-port", "no-open"); name != "" {
			return usagef("-%s needs -ui", name)
		}

		return nil
	}

	if o.asJSON {
		return usagef("-ui is interactive and -json is for machines: pick one")
	}

	if !env.Interactive {
		return usagef("--ui needs an interactive terminal; use --report in CI")
	}

	return nil
}

// watch attaches the dashboard and the report file to a started run; nil when neither was asked for.
func (o *benchOptions) watch(
	ctx context.Context,
	env *Env,
	r *loadtest.Runner,
	req loadtest.Request,
) (*liveui.Watch, error) {
	if !o.ui && o.report == "" {
		return nil, nil //nolint:nilnil // no watch is a valid answer
	}

	opts := liveui.Options{
		UI:      o.ui,
		Port:    o.uiPort,
		NoOpen:  o.noOpen,
		Title:   req.Method + " " + liveurl(req.URL),
		Model:   "closed",
		Out:     env.Stderr,
		Active:  r.InFlight,
		Target:  r.Target,
		Dropped: r.Dropped,
		Version: version(env),
	}

	if o.rate != "" {
		opts.Model = "open"
	}

	if o.report != "" {
		opts.Report = projectPath(env, o.report)
	}

	return liveui.Begin(ctx, opts)
}

// liveurl is the target without credentials, query or fragment, for titles.
func liveurl(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}

	u.User, u.RawQuery, u.Fragment = nil, "", ""

	return u.String()
}

// codeError is an error that makes the CLI exit with a code of its own, after having printed its own message.
type codeError struct {
	code int
	err  error
}

func (e *codeError) Error() string { return e.err.Error() }

func (e *codeError) Unwrap() error { return e.err }

// gateError maps a failed gate to the exit codes of bench: 3 for a threshold, 4 for a regression.
func gateError(err error) error {
	switch {
	case errors.Is(err, loadtest.ErrThresholds):
		return &codeError{code: 3, err: err}
	case errors.Is(err, loadtest.ErrRegression):
		return &codeError{code: 4, err: err}
	}

	return err
}

// gate builds the thresholds, the baseline file and the tolerance from the flags.
func (o *benchOptions) gate(env *Env) (loadtest.Gate, error) {
	g := loadtest.Gate{Tolerance: loadtest.DefaultTolerance()}

	for _, e := range o.threshold {
		t, err := loadtest.ParseThreshold(e)
		if err != nil {
			return g, usagef("%v", err)
		}

		g.Thresholds = append(g.Thresholds, t)
	}

	tol, err := loadtest.ParseTolerance(o.toler)
	if err != nil {
		return g, usagef("-tolerance: %v", err)
	}

	g.Tolerance = tol

	if o.save != "" {
		g.Save = projectPath(env, o.save)
	}

	if o.compare != "" {
		g.Compare = projectPath(env, o.compare)
	}

	return g, nil
}

// projectPath resolves a path relative to the project directory; an absolute path stays as it is.
func projectPath(env *Env, p string) string {
	if filepath.IsAbs(p) {
		return p
	}

	return env.path(p)
}

// firstSet returns the first of names that was set explicitly.
func firstSet(explicit map[string]bool, names ...string) string {
	for _, n := range names {
		if explicit[n] {
			return n
		}
	}

	return ""
}

// forwardedFlags rebuilds the flags the user typed, to hand them to bench:run in the app binary. A repeated flag is
// forwarded once per value.
func forwardedFlags(set *flag.FlagSet) []string {
	var out []string

	set.Visit(func(f *flag.Flag) {
		if r, ok := f.Value.(*repeatFlag); ok {
			for _, v := range *r {
				out = append(out, "-"+f.Name+"="+v)
			}

			return
		}

		out = append(out, "-"+f.Name+"="+f.Value.String())
	})

	return out
}

func (o *benchOptions) interactive(env *Env) bool { return env.Interactive && !o.quiet }

// build turns the flags and the URL into a run configuration; every mistake is a usage error.
func (o *benchOptions) build(
	env *Env,
	args []string,
	explicit map[string]bool,
) (loadtest.Config, loadtest.Request, error) {
	if len(args) != 1 {
		return loadtest.Config{}, loadtest.Request{}, usagef("expected exactly one URL")
	}

	if !strings.HasPrefix(args[0], "http://") && !strings.HasPrefix(args[0], "https://") {
		return loadtest.Config{}, loadtest.Request{}, usagef(
			"bench needs an http:// or https:// URL, or no argument to run the project's scenarios",
		)
	}

	if explicit["c"] && o.rate != "" {
		return loadtest.Config{}, loadtest.Request{}, usagef("-c (closed model) and -rate (open model) are exclusive")
	}

	if explicit["body"] && o.bodyFile != "" {
		return loadtest.Config{}, loadtest.Request{}, usagef("-body and -body-file are exclusive")
	}

	header, err := parseHeaders(o.headers)
	if err != nil {
		return loadtest.Config{}, loadtest.Request{}, err
	}

	body := []byte(o.body)

	if o.bodyFile != "" {
		if body, err = readBodyFile(env.path(o.bodyFile)); err != nil {
			return loadtest.Config{}, loadtest.Request{}, usagef("-body-file: %v", err)
		}
	}

	cfg := loadtest.Config{
		Concurrency: o.concurrency, Duration: o.duration, Requests: o.requests, MaxWorkers: o.maxWorkers,
		Timeout: o.timeout, Connections: o.connections, NoKeepAlive: o.noKeepAlive, HTTP2: o.http2,
		AllowRemote: o.allowRemote, UserAgent: "vukem-bench/" + version(env),
	}

	if o.rate != "" {
		if cfg.Rate, err = loadtest.ParseRate(o.rate); err != nil {
			return loadtest.Config{}, loadtest.Request{}, usagef("%v", err)
		}

		cfg.Concurrency = 0
	}

	return cfg, loadtest.Request{Method: strings.ToUpper(o.method), URL: args[0], Header: header, Body: body}, nil
}

// maxBodyFile bounds -body-file: the body is held in memory and sent on every request.
const maxBodyFile = 10 << 20

func readBodyFile(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}

	defer f.Close()

	b, err := io.ReadAll(io.LimitReader(f, maxBodyFile+1))
	if err != nil {
		return nil, err
	}

	if len(b) > maxBodyFile {
		return nil, fmt.Errorf("%s is larger than %d MiB", path, maxBodyFile>>20)
	}

	return b, nil
}

func parseHeaders(raw []string) (http.Header, error) {
	h := http.Header{}

	for _, line := range raw {
		name, value, ok := strings.Cut(line, ":")
		name = strings.TrimSpace(name)

		if !ok || name == "" {
			return nil, usagef("-H %q: want \"Name: value\"", line)
		}

		h.Add(name, strings.TrimSpace(value))
	}

	return h, nil
}

// collectBench reads the results of r until it ends and, on a terminal, prints a progress line every second.
func collectBench(env *Env, r *loadtest.Runner, progress bool, watch *liveui.Watch) loadtest.Summary {
	var m loadtest.Metrics

	tick := time.NewTicker(time.Second)
	defer tick.Stop()

	for {
		select {
		case res, ok := <-r.Results():
			if !ok {
				return r.Summarize(&m)
			}

			m.Add(res)

			if watch != nil {
				watch.Observe(res)
			}
		case <-tick.C:
			if progress {
				s := r.Summarize(&m)
				failed := uint64(s.HTTPReqFailed*float64(s.HTTPReqs) + 0.5)

				fmt.Fprintf(
					env.Stderr,
					"%ds  %d req  %.0f rps  p95 %s  %d failed\n",
					int(
						s.Elapsed.Seconds(),
					),
					s.HTTPReqs,
					s.RPS,
					s.HTTPReqDuration.P95.Round(100*time.Microsecond),
					failed,
				)
			}
		}
	}
}
