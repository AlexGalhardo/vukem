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
| Queue | BETA | sync, memory, database (SQLite/Postgres, `FOR UPDATE SKIP LOCKED` on PG) | redis | ✅ (`queue.Fake`) |
| Scheduler | BETA | in-process cron | — | clock |
| Encryption | BETA (planned) | AES-256-GCM with key rotation | — | — |
| Rate limiting | ALPHA / BETA | memory (GCRA) | redis | clock |
| i18n | BETA | JSON files, CLDR plural rules subset | — | `i18n.New` + `Add` |
| SSE | BETA (planned) | in-process hub | redis pub/sub | — |

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
- Queued mail: `app.MailLater(ctx, mailable)` renders now and sends from a retried queue job (see [Queue](#queue)).

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
- Stores implement `cache.Store` (`Get`, `Set`, atomic `Add`, `Delete`, owner-checked `DeleteIf`, `Increment`, `Flush`): `cache.NewMemory(maxEntries)` (LRU + TTL) and `cache.NewDatabase(db, table)` (SQLite/Postgres upserts; `Prune` removes expired rows). Redis comes with `drivers/redis` ([Redis](#redis)). `cache/cachetest.Run` is the conformance suite, run on memory, SQLite and Postgres.

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
- Queued listeners: `events.OnQueued("kind", fn)` enqueues the event and runs `fn` in a queue worker (see [Queue](#queue)).

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
- `vukem make:job SendInvoice` writes `app/jobs/send_invoice.go` (job type, `Kind`, handler) and registers `queue.Handle(HandleSendInvoice)` in `app/jobs/registry.go`, which `bootstrap` passes to `WithJobs`.
- **Queued mail:** `app.MailLater(ctx, mailable)` renders now and sends from a job (retried; the message ID is stable across retries).
- **Queued listeners:** `events.OnQueued("update_stock", listeners.UpdateStock)` (or `make:listener UpdateStock --event OrderShipped --queued`) enqueues the event as JSON and runs the listener in a worker; `Dispatch` returns only enqueue errors.
- **Testing:** pass `vukem.WithQueueDriver(fake)` with `fake := queue.NewFake()`; `queue.Dispatched[jobs.SendInvoice](fake)` returns the dispatched jobs, and `q.Work(ctx, queue.WorkOptions{StopWhenEmpty: true})` runs them when the test wants to.
- Inspired by River (typed workers, transactional enqueue) and goqite (SQLite).

## Scheduler

```go
// routes/schedule.go (bootstrap passes it to vukem.WithSchedule)
func Schedule(s *schedule.Schedule, app *vukem.App) {
	s.Command("reports:send", "--daily").DailyAt("08:00").WithoutOverlapping()
	s.Job(jobs.PruneSessions{}).Hourly()
	s.Func("ping", func(ctx context.Context) error { ... }).EveryFiveMinutes()
	s.Func("invoices", sendInvoices).Cron("30 9 1 * *").In(saoPaulo)
}
```

- Expressions: five-field cron (`*`, values, ranges, steps, lists, `JAN`/`MON` names, `@daily`-style macros; Vixie OR semantics when both day fields are set) or helpers: `EveryMinute`, `EveryFiveMinutes`, `Every(15*time.Minute)` (whole minutes dividing an hour or a day), `Hourly`, `HourlyAt`, `Daily`, `DailyAt("HH:MM")`, `Weekdays`, `WeeklyOn`, `MonthlyOn`. Bad expressions panic at boot. The parser is fuzzed.
- Time zone: `SCHEDULE_TIMEZONE` (IANA name, default UTC; the zone database is embedded, so it works on Windows hosts without one) or `.In(loc)` per task.
- `WithoutOverlapping()` takes a lock in `App.Cache` (use `CACHE_STORE=database` or Redis when several servers run the schedule); a run is skipped while the previous one holds it.
- Run it in process with `vukem schedule:work` (ticks at the start of every minute, drains running tasks on shutdown) or with one cron entry: `* * * * * cd /app && ./app schedule:run`. `vukem schedule:list` shows each task's next run.
- Tasks run concurrently; failures and panics are logged and do not stop other tasks. Command tasks run in the same process through `App.Call`.

## Redis

`go get github.com/AlexGalhardo/vukem/drivers/redis` (go-redis v9; works with Redis 7+ and Valkey 8+, both run in CI).

```go
rc, err := redis.Open(ctx, os.Getenv("REDIS_URL")) // redis://localhost:6379/0, rediss:// for TLS

app, err := vukem.New(
	vukem.WithCacheStore(rc.CacheStore("cache:")),   // also backs scheduler and cache locks
	vukem.WithSessionStore(rc.SessionStore("")),     // "session:" keys with TTL
	vukem.WithQueueDriver(rc.Queue(redis.QueueOptions{})),
)

limiter := rc.RateLimiter("api:", 60, time.Minute) // ratelimit.Limiter shared by every server
msgs, err := rc.Subscribe(ctx, "orders")          // pub/sub (SSE fan-out); rc.Publish(ctx, "orders", b)
```

- The queue keeps jobs in sorted sets under `{queue}:` (one Cluster slot) and reserves them with one Lua script; abandoned jobs are taken over after their timeout plus one minute, failed jobs support `queue:failed`, `queue:retry` and `queue:forget`.
- `Flush` deletes only the store's prefix (`SCAN` + `DEL`), never the whole database, and refuses an empty prefix.
- The driver passes the `cachetest` and `queuetest` conformance suites.

## Encryption

Package `crypt`, stdlib only. `app.Crypt` is built at boot from a key derived with HKDF from `APP_KEY` (`app.Key.Derive("crypt", 32)`), plus the keys derived from `APP_PREVIOUS_KEYS` ([configuration.md](configuration.md#app_key)).

```go
token := app.Crypt.EncryptString("4111 1111 1111 1111")
plain, err := app.Crypt.DecryptString(token) // err is crypt.ErrInvalid on any failure

// Additional data binds a token to its purpose; Open needs the same bytes.
token = app.Crypt.Seal(payload, []byte("invoice:42"))
payload, err = app.Crypt.Open(token, []byte("invoice:42"))

if app.Crypt.NeedsRotation(token) { // sealed with a previous key
	token, err = app.Crypt.Rotate(token, []byte("invoice:42"))
}

// Encrypted cookies: the name is the additional data.
err = app.Crypt.SetCookie(w, r, http.Cookie{Name: "cart", Value: "3", Path: "/"})
cart, err := app.Crypt.Cookie(r, "cart") // http.ErrNoCookie or crypt.ErrInvalid
```

- AES-256-GCM with a random 96-bit nonce per token (`cipher.NewGCMWithRandomNonce`); keys must be 32 bytes (`crypt.New(current, previous...)`).
- Token: unpadded base64url of `version (1) | key id (4) | nonce (12) | ciphertext | tag (16)`. The key id (truncated SHA-256 of the key) selects the key; the header is authenticated with the additional data.
- Every failure (malformed, tampered, unknown key, other additional data) returns `crypt.ErrInvalid`, without telling which.
- `SetCookie` always sets `HttpOnly`, defaults `SameSite` to Lax, adds `Secure` when the request came over TLS (set `Secure` yourself behind a TLS-terminating proxy), refuses `SameSite=None` without `Secure` and values over 4 KB (`crypt.ErrCookieTooLarge`).
- Database columns: [`orm.Encrypted[T]`](database.md#encrypted-columns).

## i18n

Package `i18n`, stdlib only. One JSON catalog per locale in `resources/lang/<locale>.json` (`en.json`, `pt-BR.json`; `pt_BR.json` is accepted). Keys are nested objects or dotted names; values are strings with `{name}` placeholders, or plural objects.

```json
{
  "welcome": "Hello, {name}!",
  "posts": {
    "created": "Post created.",
    "count": { "zero": "No posts", "one": "{count} post", "other": "{count} posts" }
  }
}
```

```go
app, err := vukem.New(
	vukem.WithTranslations(os.DirFS("resources/lang")), // App.I18n; APP_LOCALE is the default (en)
	vukem.WithViews(resources.Views()),
	vukem.WithRoutes(routes), // routes call vukem.DefaultMiddleware
)

c.Flash("status", i18n.T(c.Context(), "posts.created"))   // handlers
b.T("pt-BR", "posts.count", "count", 3)                   // a Bundle directly
```

```html
<html lang="{{locale}}">
<p>{{t "welcome" "name" .User.Name}} · {{t "posts.count" "count" (len .Posts)}}</p>
```

- **Fallback chain:** the locale, its shorter forms, then the default (`pt-BR` → `pt` → `en`). A missing key returns the key and calls `Options.OnMissing` when set; translating never panics.
- **Plurals:** an object whose keys are CLDR categories (`zero`, `one`, `two`, `few`, `many`, `other`; `other` required) is a plural message, chosen by the numeric `count` argument. Rules cover en, de, nl, sv, da, nb, fi, et, it, ca, es, el, hu, tr, bg, pt, pt-PT, fr, ru, uk, be, pl, cs, sk, ar, he, and ja, zh, ko, th, vi, id, ms (only `other`); other languages use the English rule. An explicit `zero` form wins for 0 in every language. `i18n.PluralCategory(locale, n)` exposes the rules.
- **Arguments:** key/value pairs (`"name", "Ana"`) or one `map[string]any`; unknown placeholders stay as written. Output is plain text, so `html/template` escapes it.
- **Locale resolution:** `i18n.Middleware(bundle, i18n.MiddlewareConfig{})` checks, in order, `?lang=`, the `lang` cookie, the session value `locale` (when the session middleware runs first), `Accept-Language` (RFC 9110 q-values, at most 16 entries) and the default. Candidates must be valid tags matching an available catalog (`Bundle.Match`: exact, shorter form, then same language); invalid input is ignored. It stores the bundle and locale in the context (`i18n.Locale`, `i18n.WithLocale`, `i18n.BundleFrom`), sets `Content-Language` and adds `Vary: Accept-Language` when the header was consulted. Rename or disable (`"-"`) sources with `Query`, `Cookie`, `SessionKey`. Apps persist a choice by setting the cookie or session value themselves.
- **App wiring:** `WithTranslations` loads the bundle at boot (bad JSON or file names fail `New`), gives views the `t` and `locale` functions (`view.Config.I18n`) and makes `DefaultMiddleware` install the middleware after sessions.
- **Validation:** the middleware also translates `web.Parse`/`Bind` messages from `validation.*` keys ([validation.md](validation.md#translated-messages)).
- Starters do not ship catalogs yet; add `resources/lang/en.json` and pass `WithTranslations`.
