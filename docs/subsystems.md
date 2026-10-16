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
- Context-aware: `vukem.Logger(ctx)` returns the request-scoped logger.
- `Secret` values are redacted via `LogValuer`.
- OpenTelemetry: a `telemetry` provider (BETA) wraps the router with `otelhttp`-style spans **in a separate module** to keep OTel out of core.
- `/up` returns 200 when the app booted; `/up?checks=1` runs registered health checks (DB ping, Redis ping) with timeouts.

## Telegram logging

A `slog.Handler` (package `logging/telegram`) that sends selected records to Telegram chats, ported from the maintainer's TypeScript [telegram-logger](https://github.com/AlexGalhardo/telegram-logger). Stdlib only (`net/http`).

Apps turn it on with environment variables; the app logger then fans out to stderr and Telegram (`slog.NewMultiHandler`):

```sh
TELEGRAM_BOT_TOKEN=123456:ABC...   # config.Secret, never logged
TELEGRAM_CHAT_ID=-1001234567890
TELEGRAM_MIN_LEVEL=error           # debug, info, warn, error
TELEGRAM_THREAD_ID=42              # optional forum topic
```

```go
app.Telegram.Contact(ctx, map[string]string{"name": in.Name, "message": in.Message}) // never filtered by level
app.Telegram.Send(ctx, "<b>Deploy</b> finished")                                     // pre-formatted HTML

// Standalone: per-level routing, silent mode, custom error handling.
tg := telegram.NewHandler(telegram.Config{
	BotToken: token, ChatID: "-100", ChatIDs: map[slog.Level]string{telegram.LevelFatal: "-200"},
	MinLevel: slog.LevelWarn, AppName: "Shop", Env: "production", Silent: true, OnError: report,
})
defer tg.Close(ctx)
```

| Feature | Detail |
| --- | --- |
| Levels | debug, info, warn, error plus `telegram.LevelSuccess` and `telegram.LevelFatal`; `MinLevel` filter |
| Routing | per-level chat ids, forum topic (`ThreadID`), `Silent` |
| Format | Telegram HTML: emoji and level, app name, environment, UTC time, then the message and attributes (groups dotted, error chains as "caused by") in a `<pre>` block; all content escaped; truncated to 4096 characters without cutting tags or entities |
| Reliability | bounded queue (`QueueSize`, drops and reports when full), per-request `Timeout`, one retry after HTTP 429 honouring `retry_after` capped by `MaxRetryAfter`, `OnError`, drained by `app.Shutdown` |
| Security | token held as `config.Secret`; request errors are redacted (`TestSecurity_A09_TelegramTokenRedacted`); `vukem about` shows only on/off; disabled when token or chat is missing |

## Mail

```go
// app/mail/welcome.go — vukem make:mail Welcome (also creates resources/views/mail/welcome.html)
type Welcome struct{ User models.User }

func (m Welcome) Envelope() mail.Envelope {
	return mail.Envelope{To: []string{m.User.Email}, Subject: "Welcome!", Tags: map[string]string{"kind": "welcome"}}
}

func (m Welcome) Content() mail.Content { return mail.View("mail/welcome", m) }

mailer, err := app.Mailer()
err = mailer.Send(ctx, Welcome{User: u})
```

- Content: `mail.View(name, data)` renders the app's `html/template` view; the text part comes from `<name>_text` when it exists, otherwise from the HTML (links become `text (url)`). `mail.HTML(html, text)` and `mail.Text(text)` for literal bodies. Mail views use `{{/* layout: none */}}` or a mail layout.
- Attachments: implement `Attachments() []mail.Attachment`; inline parts set `Inline` and `ContentID` and are referenced as `cid:...`.
- Addresses go through `net/mail` (no header injection); subjects, tags and content ids with line breaks are rejected (`TestSecurity_A05_MailHeaderInjection`). `Bcc` is never written as a header.
- Previews in development: `r.Handle("/_mail/", http.StripPrefix("/_mail/", mail.Preview(mailer, map[string]mail.Mailable{"welcome": sample})))`.
- Tests: `MAIL_MAILER=array` by default in `vukemtest`; `app.Mails().SentTo("ana@example.com")`.
- Queued mail arrives with the queue (BETA).

| `MAIL_MAILER` | Transport | Settings |
| --- | --- | --- |
| `smtp` | `net/smtp`, STARTTLS required (or implicit TLS on 465), AUTH PLAIN; multipart/mixed → related → alternative MIME, base64 parts | `SMTP_HOST`, `SMTP_PORT` (587), `SMTP_USERNAME`, `SMTP_PASSWORD` (secret), `SMTP_INSECURE` (local catchers only) |
| `resend` | Resend HTTP API (`POST /emails`): tags, reply-to, attachments with content ids, the message id as `Idempotency-Key`, retries on 429 (honouring `Retry-After`, capped) and 5xx, typed `*mail.ResendError` | `RESEND_API_KEY` (secret, never logged: `TestSecurity_A09_ResendKeyRedacted`) |
| `log` | logs recipients and subject (bodies at debug) | default outside production |
| `array` | in-memory (tests) | default in `vukemtest` |

`MAIL_FROM` sets the default sender. Production must set `MAIL_MAILER` explicitly; a missing or unknown value is an error, never silent loss of mail.

## Cache

```go
c, err := app.Cache() // CACHE_STORE=memory (default) or database; CACHE_PREFIX, CACHE_TABLE, CACHE_MAX_ENTRIES

user, err := cache.Remember(ctx, c, "user:42", time.Hour, func(ctx context.Context) (models.User, error) {
	return orm.QueryOf[models.User](db).Find(ctx, 42)
})

err = c.Put(ctx, "settings", settings, 10*time.Minute)
v, ok, err := cache.Get[Settings](ctx, c, "settings")
n, err := c.Increment(ctx, "visits", 1, 24*time.Hour)

lock := c.NewLock("reports:daily", time.Minute)
if ok, _ := lock.TryAcquire(ctx); ok {
	defer lock.Release(ctx) // only the owner can release
}
```

- Values are JSON. `Remember` runs `fn` once for concurrent misses of a key in the process (singleflight) and never caches errors.
- Stores implement `cache.Store` (`Get`, `Set`, atomic `Add`, `Delete`, owner-checked `DeleteIf`, `Increment`, `Flush`): `cache.NewMemory(maxEntries)` (LRU + TTL) and `cache.NewDatabase(db, table)` (SQLite/Postgres upserts; `Prune` removes expired rows). Redis comes with `drivers/redis` (BETA). `cache/cachetest.Run` is the conformance suite, run on memory, SQLite and Postgres.

## Storage

```go
disk, err := storage.NewLocal("storage/app/public", "/storage") // os.Root: no "..", no escaping symlinks
r.Handle("/storage/", http.StripPrefix("/storage", storage.Handler(disk, nil)))

path, err := storage.PutUploaded(ctx, disk, fileHeader, "avatars", storage.Images(2<<20))
url := disk.URL(path)

private, _ := storage.NewLocal("storage/app/private", "")
r.Handle("/files/", http.StripPrefix("/files", storage.Handler(private, signer)))
link, err := storage.TemporaryURL(signer, "/files", "invoices/42.pdf", 15*time.Minute)
```

- `storage.Disk`: `Put` (atomic rename on local disks), `Open`, `Exists`, `Delete`, `List`, `URL`; helpers `storage.Get` and `storage.PutBytes`. Disks: `NewLocal` and `NewMemory` (tests); S3 in BETA (`drivers/s3`). `storage/storagetest` holds the conformance and traversal suites (`TestSecurity_A01_StorageTraversal`).
- `PutUploaded` never uses the client's file name: random name, extension from the type detected in the content (`http.DetectContentType`), size checked from the header and again while copying, 422 for disallowed types or sizes.
- `Handler` never serves dotfiles or directories, sends `X-Content-Type-Options: nosniff`, and with a `signed.Signer` only answers signed, unexpired URLs.
- Decision: instead of `storage:link` (a symlink, which needs extra privileges on Windows), public files are served by `storage.Handler` from the disk itself.

## Events

```go
// app/events/order_shipped.go — vukem make:event OrderShipped
type OrderShipped struct{ OrderID int64 }

// app/listeners/notify_customer.go — vukem make:listener NotifyCustomer --event OrderShipped [--async]
func NotifyCustomer(ctx context.Context, e events.OrderShipped) error { ... }

// app/listeners/registry.go (make:listener appends), passed to vukem.WithListeners(listeners.All()...)
func All() []events.Registration { return []events.Registration{events.OnAsync(NotifyCustomer)} }

err := app.Events.Dispatch(ctx, events.OrderShipped{OrderID: 42})
```

- Typed by generics: `events.On(fn)` infers the event type from the function. Synchronous listeners run in registration order and the first error stops the chain and is returned; async ones (`OnAsync`) run on a bounded pool with a context detached from the request, log their errors and panics, and are drained by `app.Shutdown`.
- Tests: `events.NewFake()` records dispatches; `events.Dispatched[OrderShipped](fake)`.
- Queue-backed listeners arrive with the queue (BETA).

## Rate limiting

```go
login := ratelimit.Middleware(ratelimit.NewMemory(5, time.Minute), account.LoginThrottleKey)
r.Post("/login", h.Login).Use(login)
```

GCRA limiter keyed by `ratelimit.ByToken`, `ByIP` or your own `KeyFunc`; responses include `RateLimit-*` headers, `429` adds `Retry-After`; a failing store denies. `install:auth` throttles logins per email and IP by default. Redis store in BETA.

## Queue

```go
type SendInvoice struct{ OrderID int64 }
func (SendInvoice) Kind() string { return "send_invoice" }

// bootstrap: vukem.WithJobs(jobs.All()...)
queue.Handle(func(ctx context.Context, j SendInvoice) error { ... },
	queue.Retries(5), queue.Backoff(queue.Exponential(time.Second)), queue.Timeout(time.Minute))

q, err := app.Queue()
err = q.Dispatch(ctx, SendInvoice{OrderID: 1}, queue.OnQueue("billing"), queue.Delay(5*time.Minute))
err = q.DispatchTx(ctx, tx, SendInvoice{OrderID: 1}) // database driver: enqueued only if tx commits (B.1.2)
```

- Payloads are JSON; `Kind()` maps to the registered handler (no reflection-based class names). Registering a kind twice is a boot error.
- Defaults: queue `default`, 3 retries with exponential backoff from 1s (capped at 1h), 1-minute timeout per attempt; options on `Handle` set the defaults, options on `Dispatch` override them.
- `QUEUE_CONNECTION`: `sync` (default; the job runs inside `Dispatch` and its error is returned), `memory` (in process; `serve` runs workers, `QUEUE_CONCURRENCY` default 4, `QUEUE_NAMES` in priority order), `database` (B.1.2), `redis` (B.2).
- Workers (`q.Work`) recover panics, retry with backoff, then move the job to the failed jobs with its last error. On shutdown they stop reserving and let running jobs finish (their context is not cancelled; the job timeout still applies).
- **Database driver** (`QUEUE_CONNECTION=database`, SQLite and Postgres): tables `jobs` and `failed_jobs` (`QUEUE_TABLE`, `QUEUE_FAILED_TABLE`) are created on first use. Workers reserve a job with one atomic `UPDATE … RETURNING` (Postgres adds `FOR UPDATE SKIP LOCKED`), so any number of `queue:work` processes can share them. A job whose worker died is taken over after its timeout plus one minute: delivery is at least once, so make handlers idempotent.
- `q.DispatchTx(ctx, tx, job)` inserts the job in the caller's transaction: it exists only if the transaction commits.
- Commands: `queue:work [-queue high,default] [-concurrency 4] [-once] [-stop-when-empty]` (Ctrl+C drains running jobs), `queue:failed [-json]`, `queue:retry <ids…|all>`, `queue:forget <ids…|all>`.
- The `database/dbtest` conformance suite covers the driver (exactly-once processing with concurrent workers, `DispatchTx` rollback, failed/retry/forget, takeover of abandoned jobs) on SQLite and Postgres.
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

## i18n

`t(ctx, "posts.created", "title", p.Title)`, pluralisation, locale from `Accept-Language`/session/route, JSON files in `resources/lang/<locale>.json`, validation messages translated.
