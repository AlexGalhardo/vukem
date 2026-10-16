# Subsystems

Mail, cache, storage, events, queue, scheduler, encryption, rate limiting, i18n, logging and observability. All follow the **manager/driver** pattern: one typed config section, an interface, several drivers selected by config, a fake for tests. Drivers with heavy dependencies live in `drivers/*` modules.

| Subsystem | Phase | Core drivers | Driver modules | Fake |
| --- | --- | --- | --- | --- |
| Logging (`log/slog`) | Prototype / ALPHA | text (dev), JSON (prod), multi; **Telegram** handler (ALPHA) | — | capture handler |
| Health | Prototype | `/up` + per-service checks | — | — |
| Mail | ALPHA | SMTP (`net/smtp`), **Resend** (HTTP API), log, array | — | ✅ |
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

## Telegram logging (ALPHA)

A `slog.Handler` (package `logging/telegram`) that sends selected records to Telegram chats, ported from the maintainer's TypeScript [telegram-logger](https://github.com/AlexGalhardo/telegram-logger). Stdlib only (`net/http`).

```go
tg := telegram.NewHandler(telegram.Config{
	BotToken: cfg.Telegram.BotToken, // TELEGRAM_BOT_TOKEN (config.Secret)
	ChatID:   cfg.Telegram.ChatID,   // TELEGRAM_CHAT_ID (default chat)
	ChatIDs:  map[slog.Level]string{slog.LevelError: "-100123"}, // optional per-level routing
	MinLevel: slog.LevelError,
	AppName:  app.Config.Name,
	Env:      string(app.Config.Env),
})
app.Log = slog.New(slog.NewMultiHandler(app.Log.Handler(), tg)) // Go 1.26 fan-out
```

| Feature | Detail |
| --- | --- |
| Levels | debug, info, warn, error (+ `success` and `fatal` helpers via custom levels); `MinLevel` filter |
| Routing | per-level chat ids, forum topic (`MessageThreadID`), `DisableNotification` |
| Format | Telegram HTML: emoji per level, app name, environment, timestamp, attributes, error chain and stack; all user content HTML-escaped; truncated to 4096 chars |
| Reliability | asynchronous bounded queue (never blocks or fails the request), per-request timeout, one retry honouring `retry_after` on HTTP 429 (capped), `OnError` callback, drained on shutdown |
| Extras | `telegram.Contact(ctx, form)` for contact-form notifications (never filtered by level); `Send(ctx, html)` for pre-formatted messages |
| Security | token held as `config.Secret`, never logged; disabled automatically when token/chat are missing (boot warning, not error) |

## Mail

```go
type Welcome struct{ User models.User }

func (m Welcome) Envelope() mail.Envelope { return mail.Envelope{To: []string{m.User.Email}, Subject: "Welcome"} }
func (m Welcome) Content() mail.Content   { return mail.View("mail/welcome", m) } // html + text templates

err := app.Mail.Send(ctx, Welcome{User: u})
err := app.Queue.Dispatch(ctx, mail.Queued(Welcome{User: u})) // BETA
```

Attachments, inline images, `mail:preview` route in development.

Drivers: `smtp` (`net/smtp` + STARTTLS), **`resend`** (Resend HTTP API `POST https://api.resend.com/emails`, `RESEND_API_KEY` as `config.Secret`, idempotency key per message, tags, reply-to, attachments; errors mapped to typed errors with retry on 429/5xx), `log` (development) and `array` (tests). Select with `MAIL_MAILER=resend`.

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
app.Limiter.Define("login", func(c *web.Ctx) ratelimit.Limit {
	return ratelimit.PerMinute(5).By(c.FormValue("email") + "|" + c.IP())
})
r.Post("/login", auth.Login).Use(ratelimit.Middleware("login"))
```

Responses include `RateLimit-*` headers and `Retry-After`.

## i18n

`t(ctx, "posts.created", "title", p.Title)`, pluralisation, locale from `Accept-Language`/session/route, JSON files in `resources/lang/<locale>.json`, validation messages translated.
