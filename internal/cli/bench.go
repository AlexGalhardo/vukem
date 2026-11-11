package cli

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/AlexGalhardo/vukem/loadtest"
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

	return func(ctx context.Context, env *Env, args []string) error {
		explicit := map[string]bool{}
		set.Visit(func(f *flag.Flag) { explicit[f.Name] = true })

		cfg, req, err := o.build(env, args, explicit)
		if err != nil {
			return err
		}

		r, err := loadtest.Start(ctx, cfg, loadtest.Static(req))
		if err != nil {
			return usagef("%v", err)
		}

		sum := collectBench(env, r, o.interactive(env))

		if o.out != "" {
			var b bytes.Buffer
			if err := loadtest.WriteJSON(&b, sum); err != nil {
				return err
			}

			if err := os.WriteFile(env.path(o.out), b.Bytes(), 0o600); err != nil {
				return err
			}
		}

		if o.asJSON {
			return loadtest.WriteJSON(env.Stdout, sum)
		}

		return loadtest.WriteText(env.Stdout, sum)
	}
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
		return loadtest.Config{}, loadtest.Request{}, usagef("bench needs a URL (scenarios arrive with plans/079)")
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
func collectBench(env *Env, r *loadtest.Runner, progress bool) loadtest.Summary {
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
