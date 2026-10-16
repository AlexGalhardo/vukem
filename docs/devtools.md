# Devtools

The `devtools` package is a development dashboard served at `/_vukem`: recent requests with timings, SQL queries with N+1 detection, jobs, events, sent mail previews, log records, a per-request trace and the route table. It is core and stdlib-only; everything lives in bounded in-memory ring buffers (200 records per kind).

## Enabling

```go
app, err := vukem.New(vukem.WithDevtools(), /* … */)
```

Or set `DEVTOOLS=true` in `.env`. Either way:

- `APP_ENV=production` makes `New` fail (`TestSecurity_A02_DevtoolsRefusedInProduction`). Use the env variable rather than the option in code that also runs in production.
- The dashboard answers only requests whose direct peer is a loopback address, whose `Host` is `localhost`, `*.localhost` or a loopback IP, and that carry no `Forwarded` / `X-Forwarded-*` / `X-Real-Ip` header. Anything else gets a plain 404 (`TestSecurity_A01_DevtoolsLoopbackOnly`). The `Host` check stops DNS-rebinding pages from reading the dashboard through the developer's browser.
- `DEVTOOLS_INSECURE_ALLOW_REMOTE=true` drops those checks (an app in a container or a remote VM). Anyone who reaches the app then sees its requests, queries, logs and mail.

`app.Devtools` is the collector (nil when off).

## Pages

| Path | Shows |
| --- | --- |
| `/_vukem/` | counts per kind, N+1 alerts, recent requests |
| `/_vukem/requests` | method, path, route name, status, duration, query count |
| `/_vukem/requests/{id}` | the trace: request summary, repeated statements and a timeline of its queries, logs, jobs, events and mail |
| `/_vukem/queries` | normalized SQL, redacted arguments, duration, caller frame, N+1 badge |
| `/_vukem/jobs` | dispatched and attempted jobs (kind, queue, status, attempt, error) |
| `/_vukem/events` | dispatched event types, listener count, duration, error |
| `/_vukem/mails`, `/_vukem/mails/{id}` | sent messages and their HTML/text preview |
| `/_vukem/logs` | log records at `LOG_LEVEL` or above |
| `/_vukem/routes` | the route table with middleware |

Pages are server-rendered `html/template` with one nonce'd `<style>` and no script, so they work under the default CSP (without `SecureHeaders` they set their own strict policy). `?refresh=5` adds a `<meta http-equiv="refresh">` (2–60 s). The dashboard's own requests are never recorded.

## Collection

| Kind | Hook |
| --- | --- |
| Requests | `Devtools.Middleware()` is the outermost server middleware (timings cover the whole stack); `RouteMiddleware()` captures the matched pattern; the route name comes from `Router.Routes()` |
| SQL | `database.QueryObserver`, set with `database.WithQueryObserver` by `App.DB()`; transactions inherit it. Statements run on `DB.SQL()` directly (sqlc) bypass it |
| Jobs | `queue.Observer` (`Queue.SetObserver`): dispatched, processed, failed |
| Events | `events.Observer` (`Bus.SetObserver`) |
| Mail | `Devtools.MailTransport` wraps the transport chosen by `MAIL_MAILER` |
| Logs | `Devtools.LogHandler()` is teed next to the app handler with `slog.NewMultiHandler` |

The observer interfaces live in the observed packages, so `database`, `queue` and `events` never import `devtools`. With devtools off no observer is set and each hook costs one nil check. Records made during a request carry its trace id (from the request context); jobs run by a worker have none.

## Redaction and N+1

- SQL is shown as a normalized shape: string and number literals and `$n` placeholders become `?`, placeholder lists collapse to `(?...)`, whitespace is collapsed. Bound arguments are shown as types and lengths (`string(len=16)`, `int`, `NULL`), never values (`TestSecurity_A09_DevtoolsRedactsQueryArgs`). Job and event records carry kinds and Go types, not payloads.
- A `SELECT`/`WITH` shape that runs at least 3 times in one request (`devtools.Config.NPlusOneThreshold`) is an N+1: the request lists it and the repeated queries get a badge.
- The caller frame is the first stack frame outside the framework, `database/sql` and the runtime.

## Mail previews

The HTML part renders in `<iframe sandbox="" srcdoc="…">` with the HTML escaped by `html/template`; it never appears inline. The preview page's CSP has no script source at all, allows inline styles (so messages look right inside the iframe, which inherits the policy) and blocks remote images, which also stops tracking pixels (`TestSecurity_A05_DevtoolsMailPreviewIsSandboxed`). Stored parts are capped at 512 KiB.
