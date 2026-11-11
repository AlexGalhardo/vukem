# Performance

Baseline for PLAN.md task B.5.3: how Vukem compares with plain `net/http`, chi and echo on routing, middleware, a full request, rendering and the ORM. These numbers are a starting point, not a leaderboard. Vukem buys security defaults and a `*web.Ctx` with every request, and this page shows what that costs.

## How to reproduce

```sh
task bench                    # core micro-benchmarks + internal/bench, -count 6
task bench COUNT=10           # more runs, tighter intervals

# or by hand, in internal/bench:
go test -run '^$' -bench . -benchmem -count 6 . > new.txt
go install golang.org/x/perf/cmd/benchstat@v0.0.0-20261009192801-be2c69fb417e
benchstat -col /impl new.txt  # one column per implementation
benchstat old.txt new.txt     # before/after a change
```

On Git Bash for Windows, prefix `benchstat -col /impl` with `MSYS_NO_PATHCONV=1`, or `/impl` is rewritten to a path. The manual **Bench** workflow (Actions → Bench → Run workflow) runs the same commands on `ubuntu-latest` and uploads the raw output and the benchstat summaries. It never runs on push: timings on shared runners vary too much to gate merges.

The comparisons live in their own module, `internal/bench` (in `go.work`, outside the `MODULES` test loop), so chi and echo never reach the core's `go.mod`. See the [dependency policy](architecture.md#dependency-policy).

## What is measured

| Benchmark | Request | Notes |
| --- | --- | --- |
| `RouterStatic` | `GET /hello` | handler writes `ok` (text/plain) |
| `RouterParam` | `GET /users/42` from `/users/{id}` | handler echoes the parameter |
| `RouterTable20` | `GET /api/v1/resource19/42` | 20 parameterised routes, the last one matches |
| `Middleware5` | `GET /hello` | 5 no-op middleware on the router |
| `FullRequest` | `GET /users/42`, JSON body | production-style stack, below |
| `RenderJSON` | 20 users | `c.JSON` vs `json.Marshal` + write |
| `RenderHTML` | layout + partial + 20-row table | `c.Render` (view engine) vs one pre-parsed `html/template` set; both buffer the page |
| `ORMInsert` / `ORMFind` / `ORMSelect100` | in-memory SQLite (`database.Open(":memory:")`) | `orm.Create`, `Query.Find`, `Query.OrderBy.Limit(100).Get` vs raw `database/sql` with a manual `Scan` |

`FullRequest` stacks:

| Impl | Stack |
| --- | --- |
| `nethttp_bare` | `http.ServeMux`, no middleware |
| `vukem_stack` | `router` with `Recover`, `RequestID`, `Logger`, `SecureHeaders`: the part of `DefaultMiddleware` chi and echo can match |
| `vukem_default` | a booted `vukem.App` (`APP_ENV=production`) with the full `DefaultMiddleware`: the stack above plus maintenance mode, method override, body limit, sessions and cross-origin (CSRF) protection |
| `chi` | `Recoverer`, `RequestID`, an slog logger over `WrapResponseWriter`, and static security headers |
| `echo` | `Recover`, `RequestID`, `RequestLoggerWithConfig` (slog), `SecureWithConfig` with a static CSP |

Every logger formats one JSON line per request with `slog.NewJSONHandler(io.Discard, nil)`, so the formatting cost is counted. Requests are replayed against a reusable `ResponseWriter` that drops the body, which keeps a growing recorder buffer out of the allocation counts.

## Results

Machine: AMD Ryzen 7 5700X3D (8 cores, 16 threads), 32 GB RAM, Windows 11 Pro 10.0.26300, Go 1.27.2 windows/amd64, chi v5.5.0, echo v4.16.0, modernc.org/sqlite v1.60.1. Each value is the median of 6 runs (`-count 6`), and ratios are against the first row of each group. The machine was running other builds at the same time, so absolute timings are noisy (±10–50 %) and ran up to 2–3× slower than in a quiet run. Read the ratios and the allocation counts, which are exact, rather than the nanoseconds. The `RenderJSON` row was measured again on 2026-11-11 with Go 1.27.4 on the same machine (`-count 6`, median), after `c.JSON` moved to `encoding/json/v2`; the other rows keep their original run.

### Routing and middleware

| Benchmark | net/http | Vukem | chi | echo |
| --- | --- | --- | --- | --- |
| `RouterStatic` | 368 ns · 16 B · 1 alloc | 1.15 µs (3.1×) · 464 B · 4 | 854 ns (2.3×) · 384 B · 3 | 244 ns (0.66×) · 24 B · 2 |
| `RouterParam` | 372 ns · 32 B · 2 | 1.23 µs (3.3×) · 496 B · 6 | 1.44 µs (3.9×) · 720 B · 5 | 295 ns (0.79×) · 24 B · 2 |
| `RouterTable20` | 623 ns · 32 B · 2 | 1.69 µs (2.7×) · 496 B · 6 | 1.66 µs (2.7×) · 720 B · 5 | 362 ns (0.58×) · 24 B · 2 |
| `Middleware5` | 309 ns · 16 B · 1 | 1.00 µs (3.2×) · 464 B · 4 | 830 ns (2.7×) · 384 B · 3 | 351 ns (1.1×) · 104 B · 7 |

### Full request

| Impl | Time | Ratio | B/op | allocs/op |
| --- | --- | --- | --- | --- |
| `nethttp_bare` | 951 ns | 1.0× | 192 | 5 |
| `echo` | 3.77 µs | 4.0× | 824 | 16 |
| `chi` | 4.82 µs | 5.1× | 1537 | 22 |
| `vukem_stack` | 6.01 µs | 6.3× | 1983 | 29 |
| `vukem_default` | 124 µs (70–181 µs) | 131× | 3147 | 43 |

### Rendering

| Benchmark | Baseline | Vukem |
| --- | --- | --- |
| `RenderJSON` (20 users) | `encoding/json`: 7.06 µs · 1217 B · 4 | `c.JSON`: 7.78 µs (1.10×) · 1313 B · 5 |
| `RenderHTML` (layout, partial, 20 rows) | `html/template`: 112 µs · 12.6 KB · 404 | `c.Render`: 213 µs (1.9×) · 48.4 KB · 710 |

### ORM (in-memory SQLite)

| Benchmark | `database/sql` | `orm` |
| --- | --- | --- |
| `ORMInsert` | 48.7 µs · 1529 B · 37 | 59.2 µs (1.22×) · 2230 B · 58 |
| `ORMFind` (by id) | 31.3 µs · 1595 B · 41 | 53.4 µs (1.70×) · 2964 B · 75 |
| `ORMSelect100` | 332 µs · 49.1 KB · 1031 | 377 µs (1.13×, not significant: p=0.18) · 42.1 KB · 954 |

## Where Vukem is slower, and why

- **Routing (about 3× net/http, on par with chi).** Vukem routes with `http.ServeMux`, so matching costs the same as net/http. The overhead is per request: `req.WithContext` plus a context value expose `web.Services` to middleware (2 allocations), each matched route allocates a `*web.Ctx` (1), and `dispatch` matches the request twice. It calls `mux.Handler` to tell a match from a 404/405 rendered by the error handler, then `mux.ServeHTTP` matches again. That second lookup is why `RouterParam` and `RouterTable20` cost more than `RouterStatic`. echo is faster than bare net/http here because its radix tree beats ServeMux's precedence rules and it pools its context.
- **Middleware chains** cost the same as net/http's: they are plain `func(http.Handler) http.Handler`. The gap in `Middleware5` is the per-request routing overhead above.
- **Full request (`vukem_stack`, 1.25× chi, 1.6× echo).** `SecureHeaders` generates a 128-bit CSP nonce from `crypto/rand` for every request, rewrites the policy and stores the nonce in the context. chi and echo send a static CSP without a nonce, which strict `script-src` cannot use. `RequestID` uses `crypto/rand` too, where chi uses a counter. The logger wraps the writer in a `web.ResponseWriter`, which the `Ctx` then reuses.
- **Default stack (`vukem_default`).** On Windows, about 70 % of the time is `middleware.Maintenance` calling `os.ReadFile("storage/framework/down")` on every request: a failed `CreateFile` costs about 40 µs here. On Linux, a failing `open` (`ENOENT`) is usually a few microseconds or less, though this page has no Linux run yet to confirm it. Sessions, CSRF (`http.CrossOriginProtection`), the body limit and method override add the remaining 14 allocations. See follow-ups.
- **HTML rendering (1.9×, 3.8× the bytes).** `view.Engine.execute` calls `template.Clone()` for every render and rebuilds the `FuncMap`, so that `csrfToken`, `cspNonce`, `t` and `vite` can close over the request context. Cloning copies the whole template set (layout, partials, page) on every request.
- **JSON** is about 10 % slower than the baseline, within the noise of the run (the 6 runs of each side spread by 23–35 %): `c.JSON` encodes with `encoding/json/v2` using the v1 default options (`web/jsoncodec.go`), against `encoding/json`'s `json.Marshal` plus a plain write in the baseline (`internal/bench/render_test.go`). The extra allocation is the `Ctx`, and the extra bytes are its fields.
- **ORM.** `Find` (1.7×) goes through `First`: it builds and renumbers the SQL, validates the columns against the model, maps columns to fields with reflection once per query and allocates a destination per field. `Create` adds hooks, timestamps and `RETURNING`. On 100 rows the per-query cost is amortised: `Get` is within noise of hand-written `Scan` code and allocates less, because the result slice is presized from `Limit`.

## Rate limit prune

`ratelimit.Memory` used to sweep the whole key map on every 1024th call while holding the mutex every request waits for. With 500,000 expired keys the slowest `Allow` of 4096 calls took 118.9 ms. It now sweeps at most 256 keys every 64 calls, walking a FIFO queue of keys as a cursor, so the slowest call took 1.07 ms (`BenchmarkAllowPruneWorstCase`, `-count 6`, median of the per-run maximum). The queue costs one string header per key: 116 B to 144 B of heap per live key at 500,000 keys (+24 %, append growth included). The maintainer accepted that cost for the bounded pause.

## Fixes made in B.5.3

| Change | Effect |
| --- | --- |
| `web.NewCtx` allocates its `ResponseWriter` inside the `Ctx`, and `web.ResponseWriter` implements `io.StringWriter` (`c.String`, `c.HTML` no longer copy the body into a `[]byte`) | matched route: 6 → 4 allocs (static), 8 → 6 (param); pinned by `TestRouteAllocations` and `TestCtxStringAllocations` |
| ORM row scanner reuses its destination and ciphertext buffers across the rows of a query (the ciphertext buffer exists only when a column is encrypted); `Get` presizes its result from `Limit` (capped at 256) | `Get` of 100 rows: 1470 → 1272 allocs, 110 KB → 78 KB (`orm.BenchmarkQueryGet100`); covered by `TestEncryptedManyRows` |

## Load testing

`task bench` runs Go micro-benchmarks (`testing.B`: router, render, ORM, and the comparison with net/http, chi and echo in `internal/bench`). `vukem bench <url>` is different: it sends real HTTP requests to an app that is running and reports throughput and latency percentiles. The engine is the stdlib-only package `loadtest`, usable from a Go test as `loadtest.Run`.

```sh
vukem serve &
vukem bench http://localhost:3000/                      # 10 workers for 10 s
vukem bench -rate 500/s -d 30s http://localhost:3000/posts
vukem bench -json -o result.json http://localhost:3000/
```

**Open and closed model.** `-c` is the closed model: N workers send one request after another, so when the server slows down they send less and the slowness hides itself. `-rate` is the open model: requests arrive at a constant rate whether or not earlier ones finished, and each latency is measured from the instant the request was *due*, not from when it was sent. If the generator falls behind or the server saturates, the delay counts in the latency (this avoids coordinated omission), and an arrival that finds `-max-workers` requests in flight is dropped and counted, not queued. The average rate is what is guaranteed; the exact instant of each arrival is not.

**Metrics.** Names follow k6.

| Metric | Definition here |
| --- | --- |
| `http_reqs` | requests that received a result, successful or not |
| `http_req_duration` | from the **scheduled** instant until the response body was fully read; in the closed model, scheduled equals sent. Unlike k6, the open model includes the generator's lateness on purpose |
| `http_req_waiting` | from the actual send until the first response byte (TTFB) |
| `http_req_connecting` | TCP connect time; only requests that opened a connection are in the series, so a reused connection adds no zero |
| `http_req_tls_handshaking` | TLS handshake time; only requests that did a handshake are in the series |
| `http_req_failed` | fraction of requests with a transport error or a status of 400 or more |
| `data_sent` / `data_received` | request and response body bytes |
| `dropped_iterations` | open-model arrivals that found no free worker with `-max-workers` reached. They are in no latency figure, so with drops the latency is understated and the report warns (`arrivals` is `http_reqs` plus the drops) |
| `rps` | `http_reqs` over the time the run spent sending, without the drain that waits for the last requests: the throughput reached, not the rate asked for |

Latencies come from a fixed-size log-linear histogram, so a percentile is exact to within about 3%. The percentiles include failed requests, as in k6; read them next to `http_req_failed`.

**Targets.** Only loopback hosts (`localhost`, `*.localhost`, `127.0.0.0/8`, `::1`) are accepted. Another host needs `-allow-remote <host>`, which states that you own it; it is not a verification, because a name can resolve to any address. The check runs for every request, and the connection is pinned to a loopback address, so a name such as `x.localhost` that resolves to an internal address, or a DNS answer that changes between the check and the connect, is refused. Redirects are never followed, the URL cannot carry credentials, and a run is capped (1 hour, 360 million requests, 100,000 requests per second, 10,000 workers, 5 minute timeout). The generated client does not use a proxy or decompress responses.

**Reading the numbers.** They depend on the machine, and a generator on the same host as the app competes with it for CPU, so use them to compare runs on one machine, not as absolute capacity. A `connection refused` for `localhost` can mean the app listens on IPv4 only; use `127.0.0.1`. `-no-keepalive` at a high rate can exhaust ephemeral ports, which appears as `other` errors.

### Scenarios, in-process mode, thresholds and baselines

`vukem bench` without a URL runs the Go scenarios of the project: `vukem make:bench <Name>` creates `app/benchmarks/<name>.go` (a `vukem.BenchScenario` with a `loadtest.Scenario`, whose `Do` sends one iteration through a `loadtest.Client`, and a `Setup` that creates the data), `bootstrap/bench.go` lists them and `vukem.WithBench(Bench()...)` registers them. `bench:run` boots a copy of the app on a free loopback port with `APP_ENV=test` and a new temporary SQLite database, migrates, runs `Setup`, applies the load and deletes the database; the configured database is never used, and `--database-url` (refused in production) points it at a disposable one instead.

`--stages "30s:50,2m:50,30s:0"` ramps virtual users (closed) or iterations per second (open, with `--model open`), linearly, from 0. `--threshold 'p95<200ms'` fails the run with exit code 3 when it does not hold (metrics: `p50` to `max`, `mean`, `min`, `ttfb_p95`, `ttfb_p99`, `error_rate`, `rps`, `dropped`, `iteration_error_rate`; a `"name":` prefix restricts latency and `error_rate` to one request name, and an unknown name fails). `--save` writes the JSON summary and `--compare` fails with exit code 4 when p95, p99 or `rps` is more than `--tolerance` (10%) worse, or `http_req_failed` is more than 0.5 percentage points higher. Compare runs of the same setup on the same machine. In CI:

```yaml
- run: go run ./cmd/app bench:run -d 20s --threshold 'p95<250ms' --threshold 'error_rate<1%'
```

The in-process app is not production: `APP_ENV=test`, SQLite and a mailer in memory change the numbers, and a self rate-limiting app answers `429`, which counts as failed.

## Follow-ups

1. **Maintenance check per request.** Cache `readMaintenance` for a short interval (for example, stat at most once a second, or watch the file). This changes how fast `vukem down` takes effect, so it needs a decision in [brainstorming.md](brainstorming.md) and an updated maintenance test. It is the largest single cost of `DefaultMiddleware` on Windows.
2. **Template clone per render.** Clone once per page at parse time, then pass request-scoped helpers through a per-request value. One option is a `FuncMap` whose functions read the context from the data the engine wraps. Another is `template.Funcs` on a pooled clone. This removes about 35 KB and 300 allocations per page.
3. **Double route match in `router.dispatch`.** Serve through the mux once and detect "no route" without a second lookup, for example with a marker wrapped around registered handlers. Keep the 404/405 problem responses, the `Allow` header and the ServeMux redirects.
4. **Services context.** Cache the `context.WithValue(services)` request per router, or let `web.ServicesFrom` fall back to the router's services, to save 2 allocations per request.
5. **`ORMFind`.** Cache the generated SQL for primary-key lookups per model and dialect.
6. **Linux numbers.** Record a quiet Linux run of the Bench workflow here once it has been dispatched on `main`.
