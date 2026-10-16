# Subsystems

Mail, cache, storage, events, queue, scheduler, encryption, rate limiting, i18n, logging and observability. All follow the **manager/driver** pattern: one typed config section, an interface, several drivers selected by config, a fake for tests. Drivers with heavy dependencies live in `drivers/*` modules.

| Subsystem | Phase | Core drivers | Driver modules | Fake |
| --- | --- | --- | --- | --- |
| Logging (`log/slog`) | Prototype | text (dev), JSON (prod), multi | — | capture handler |
| Health | Prototype | `/up` + per-service checks | — | — |
| Mail | ALPHA | SMTP (`net/smtp`), log, array | — | ✅ |
| Cache | ALPHA | memory (LRU + TTL), database | redis | ✅ |
| Storage | ALPHA | local (`os.Root`), memory | s3 (post-1.0) | ✅ |
| Events | ALPHA | in-process sync/async | — | ✅ |
| Queue | BETA | sync, database (SQLite/Postgres, `SELECT … FOR UPDATE SKIP LOCKED` on PG) | redis | ✅ |
| Scheduler | BETA | in-process cron | — | clock |
| Encryption | BETA | AES-256-GCM with key rotation | — | — |
| Rate limiting | ALPHA (memory) / BETA | memory, database | redis | clock |
| i18n | BETA | JSON files, CLDR plural rules subset | — | — |
| SSE | BETA | in-process hub | redis pub/sub | — |

## Logging & observability

- `app.Log` is a `*slog.Logger`; request logger adds `request_id`, method, route name, status, duration.
- Context-aware: `lumek.Logger(ctx)` returns the request-scoped logger.
- `Secret` values are redacted via `LogValuer`.
- OpenTelemetry: a `telemetry` provider (BETA) wraps the router with `otelhttp`-style spans **in a separate module** to keep OTel out of core.
- `/up` returns 200 when the app booted; `/up?checks=1` runs registered health checks (DB ping, Redis ping) with timeouts.

## Mail

```go
type Welcome struct{ User models.User }

func (m Welcome) Envelope() mail.Envelope { return mail.Envelope{To: []string{m.User.Email}, Subject: "Welcome"} }
func (m Welcome) Content() mail.Content   { return mail.View("mail/welcome", m) } // html + text templates

err := app.Mail.Send(ctx, Welcome{User: u})
err := app.Queue.Dispatch(ctx, mail.Queued(Welcome{User: u})) // BETA
```

Attachments, inline images, `mail:preview` route in development.

## Cache

`Get[T]`, `Put`, `Remember[T](ctx, key, ttl, fn)` with stampede protection (singleflight), `Forget`, `Flush`, atomic `Increment`, `Lock(key, ttl)`. Values encoded with `encoding/json`.

## Storage

`Disk` interface: `Put`, `Get`, `Open`, `Exists`, `Delete`, `URL`, `TemporaryURL` (signed, local disk served by a route). `c.File("avatar")` + `storage.PutUploaded(ctx, disk, file)` with randomised names and size/type checks.

## Events

```go
events.Listen[events.UserRegistered](app.Events, sendWelcomeEmail)
app.Events.Dispatch(ctx, events.UserRegistered{User: u})
```

Typed by generics; listeners registered explicitly in `bootstrap/events.go` (edited by `make:listener`). Async listeners run on the queue (BETA) or a bounded goroutine pool tracked for shutdown.

## Queue

```go
type SendInvoice struct{ OrderID int64 }
func (SendInvoice) Kind() string { return "send_invoice" }

queue.Register(app.Queue, func(ctx context.Context, j SendInvoice) error { ... },
	queue.Retries(5), queue.Backoff(queue.Exponential(time.Second)), queue.Timeout(time.Minute))

err := app.Queue.Dispatch(ctx, SendInvoice{OrderID: 1}, queue.OnQueue("billing"), queue.Delay(5*time.Minute))
err := app.Queue.DispatchTx(ctx, tx, SendInvoice{OrderID: 1}) // enqueued only if tx commits (database driver)
```

- Payloads are JSON; `Kind()` maps to the registered handler (no reflection-based class names).
- `failed_jobs` table, `queue:retry`, `queue:failed`; unique jobs; graceful drain on shutdown.
- Inspired by River (typed workers, transactional enqueue) and goqite (SQLite).

## Scheduler

```go
s.Command("reports:send").DailyAt("08:00").WithoutOverlapping()
s.Job(jobs.PruneSessions{}).Hourly()
s.Func("ping", func(ctx context.Context) error { ... }).Every(5 * time.Minute)
```

Runs in-process as a runner (`schedule:work`) or via a single cron entry calling `schedule:run`. Overlap prevention uses cache locks.

## Encryption

`crypt.Encrypt/Decrypt` (AES-256-GCM, key from HKDF over `APP_KEY`), `APP_PREVIOUS_KEYS` for rotation, encrypted cookies, `orm.Encrypted[T]` column type.

## Rate limiting

```go
app.Limiter.Define("login", func(c *ctx.Ctx) ratelimit.Limit {
	return ratelimit.PerMinute(5).By(c.FormValue("email") + "|" + c.IP())
})
r.Post("/login", auth.Login).Use(ratelimit.Middleware("login"))
```

Responses include `RateLimit-*` headers and `Retry-After`.

## i18n

`t(ctx, "posts.created", "title", p.Title)`, pluralisation, locale from `Accept-Language`/session/route, JSON files in `resources/lang/<locale>.json`, validation messages translated.
