# Subsystems

Mail, cache, storage, events, queue, scheduler, encryption, rate limiting, i18n, logging and observability. All follow the **manager/driver** pattern: one typed config section, an interface, several drivers selected by config, a fake for tests. Drivers with heavy dependencies live in `drivers/*` modules.

| Subsystem | Phase | Core drivers | Driver modules | Fake |
| --- | --- | --- | --- | --- |
| Logging (`log/slog`) | Prototype / ALPHA | text (dev), JSON (prod), multi; **Telegram** handler (ALPHA) | — | capture handler |
| Health | Prototype | `/up` + per-service checks | — | — |
| Mail | ALPHA | SMTP (`net/smtp`), **Resend** (HTTP API), log, array | — | ✅ |
| Cache | ALPHA | memory (LRU + TTL), database | redis | ✅ |
| Storage | ALPHA / BETA | local (`os.Root`), memory | s3 (AWS, MinIO, R2…) | ✅ |
| Events | ALPHA | in-process sync/async | — | ✅ (`events.NewFake`) |
| Queue | BETA | sync, memory, database (SQLite/Postgres, `FOR UPDATE SKIP LOCKED` on PG) | redis | ✅ (`queue.Fake`) |
| Scheduler | BETA | in-process cron | — | clock |
| Encryption | BETA | AES-256-GCM with key rotation | — | — |
| Rate limiting | ALPHA / BETA | memory (GCRA) | redis | ✅ (memory + `synctest`) |
| i18n | BETA | JSON files, CLDR plural rules subset | — | `i18n.New` + `Add` |
| SSE | BETA | in-process hub | redis pub/sub | — |
| Tracing (OpenTelemetry) | BETA | — | otel | `tracetest` recorder |

## Logging & observability

- `app.Log` is a `*slog.Logger`; request logger adds `request_id`, method, route name, status, duration.
- Context-aware: `vukem.Logger(ctx)` returns the request-scoped logger.
- `Secret` values are redacted via `LogValuer`.
- Error aggregation: `logging.NewDedup(next, opts)` is a stdlib `slog.Handler` that writes the first ERROR record of a kind (same level, message, handler attributes and `event` / `error` / `err` values) and counts the repeats inside a window; when the window ends (a timer per window, or `Close`), one `repeated` record carries `message`, `count` and `window`. Untracked kinds beyond 1024 per window pass through. The default logger uses it in production with a 1-minute window, in front of Telegram, and `app.Shutdown` flushes pending summaries; `LOG_DEDUP_WINDOW` changes the window (`0` disables, any duration enables it outside production). Loggers passed with `WithLogger` are used as given.
- OpenTelemetry: `drivers/otel` (BETA) traces every request **in a separate module** to keep OTel out of core ([OpenTelemetry](#opentelemetry)).
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
- Stores implement `cache.Store` (`Get`, `Set`, atomic `Add`, `Delete`, owner-checked `DeleteIf`, `Increment`, `Flush`): `cache.NewMemory(maxEntries)` (LRU + TTL) and `cache.NewDatabase(db, table)` (SQLite, Postgres and MySQL upserts; `Increment` is one atomic upsert, so concurrent servers never read the same count; `CreateTable` indexes `expires_at`; `Prune` removes expired rows in batches and every app with `CACHE_STORE=database` schedules `cache:prune` hourly). Redis comes with `drivers/redis` ([Redis](#redis)). `cache/cachetest.Run` is the conformance suite (concurrent `Increment` included), run on memory, SQLite, Postgres, MySQL, MariaDB and Redis.

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

- `storage.Disk`: `Put` (atomic rename on local disks), `Open`, `Exists`, `Delete`, `List`, `URL`; helpers `storage.Get` and `storage.PutBytes`. Disks: `NewLocal` and `NewMemory` (tests); S3 in `drivers/s3` ([S3](#s3)). `storage/storagetest` holds the conformance and traversal suites (`TestSecurity_A01_StorageTraversal`).
- `PutUploaded` never uses the client's file name: random name, extension from the type detected in the content (`http.DetectContentType`), size checked from the header and again while copying, 422 for disallowed types or sizes.
- `Handler` never serves dotfiles or directories, sends `X-Content-Type-Options: nosniff`, and with a `signed.Signer` only answers signed, unexpired URLs.
- Decision: instead of `storage:link` (a symlink, which needs extra privileges on Windows), public files are served by `storage.Handler` from the disk itself.

### S3

`go get github.com/AlexGalhardo/vukem/drivers/s3`: a `storage.Disk` on Amazon S3 or any S3-compatible service (MinIO, Cloudflare R2, DigitalOcean Spaces, Backblaze B2). No AWS SDK: requests are signed by a stdlib Signature Version 4 implementation verified against the AWS test suite.

```go
cfg, err := config.Load[s3.Config](app.Config.Source)
disk, err := s3.New(cfg) // validates config, no I/O

err = disk.Put(ctx, "videos/intro.mp4", file)            // one PUT up to S3_PART_SIZE, multipart above
info, err := disk.Stat(ctx, "videos/intro.mp4")          // HEAD: Size, ETag, ContentType, LastModified
up, err := disk.PresignPut("uploads/avatar.png", 5*time.Minute) // browser uploads straight to the bucket
link, err := disk.PresignGet("invoices/42.pdf", time.Hour)
```

| Variable | Default | Meaning |
| --- | --- | --- |
| `S3_BUCKET` | — | bucket name (DNS-compatible: 3–63 of `a-z 0-9 . -`) |
| `S3_REGION` | `us-east-1` | signing region (`auto` for R2) |
| `S3_ENDPOINT` | `https://s3.<region>.amazonaws.com` | bare origin of an S3-compatible service |
| `S3_ACCESS_KEY_ID`, `S3_SECRET_ACCESS_KEY` | — | required; `S3_SESSION_TOKEN` for temporary credentials |
| `S3_USE_PATH_STYLE` | `false` | `endpoint/bucket/key` instead of `bucket.endpoint/key` (MinIO) |
| `S3_INSECURE_HTTP` | `false` | allow an `http://` endpoint (local development only) |
| `S3_PART_SIZE` | 8 MiB | multipart part size, 5 MiB to 5 GiB |
| `S3_PUBLIC_URL` | — | prefix for `disk.URL` (CDN or public bucket); empty = private |

- `Put` buffers at most one part: small bodies go in one signed `PUT`, larger ones become a multipart upload that is aborted on any failure (part error, reader error, cancelled context, a `200` that carries an `<Error>`), so no orphaned parts are billed. Content type comes from the extension, else from the first bytes.
- `Open` streams the object; `List` uses ListObjectsV2 and follows continuation tokens; folder placeholder keys (ending in `/`) are skipped. Missing objects match `fs.ErrNotExist`; other failures are `*s3.Error` (`StatusCode`, `Code`, `Message`, `RequestID`).
- Keys go through `storage.Clean`: no empty key, no leading `/` (so `/a` and `a` cannot name different objects), no `..`, backslash or `:`.
- Presigned URLs sign only the `host` header and expire after 1 s to 7 days (the SigV4 maximum). Credentials are `config.Secret`; errors and `%+v` never show them.
- Integration tests run against MinIO (`task services:up`, port 59000); the driver passes `storagetest.Run` and `storagetest.RunTraversal`.

## Uploads

Package `upload` (BETA, stdlib only) goes beyond `storage.PutUploaded`: it never buffers a whole file.

```go
rules := upload.Images(5 << 20) // MaxFileSize + Types are required: zero Rules reject everything
rules.MaxFiles, rules.Dir = 3, "photos"
rules.Scanner = upload.ClamAV("127.0.0.1:3310") // optional

form, err := upload.Receive(c, disk, rules) // form.Values, form.Files[i].Path/Name/Type/Size/SHA256
```

- `Receive` reads `r.MultipartReader()` part by part and streams each file to the `storage.Disk` (a 64 MiB upload allocates about 100 KiB). `Rules` bound the file size, the whole request (`MaxSize`, default `MaxFiles*MaxFileSize + 1 MiB`; it replaces the router-wide body limit for that route), the file count, and field count/size. Sizes fail with 413, types/extensions/count with 422; on any error every file already written is deleted.
- Type checks: `http.DetectContentType` on the first 512 bytes must be in `Types`, and the client's extension must be one of those known for that type (`upload.Extensions("image/jpeg")` → `.jpg`, `.jpeg`), plus `Extensions` when set. The client's `Content-Type` is ignored. Stored names are random with the canonical extension; the sanitized client name (`upload.SanitizeName`) is metadata only.
- `upload.Save(ctx, disk, rules, field, filename, r)` applies the same checks to any reader (finished tus uploads, imports).
- Scanning: with `Rules.Scanner`, files are written to `QuarantineDir/.<random>` (dot-named, so `storage.Handler` never serves them), scanned, then copied to `Dir` and the quarantined copy deleted. `ErrInfected` → deleted + 422; a scanner error → deleted + 503 (fail closed). `upload.ClamAV(addr)` speaks clamd's `INSTREAM` over TCP with per-operation timeouts.
- Nothing may read the body before `Receive`: `middleware.MethodOverride` leaves multipart bodies unread (multipart forms put `_method` in the query string) and handlers must not call `c.Input()`/`c.FormFile()` first.

Resumable uploads implement tus 1.0 core with the creation, expiration and termination extensions:

```go
tus, err := upload.Tus(upload.TusConfig{
	Dir: "storage/app/tus", BasePath: "/uploads/tus/", MaxSize: 2 << 30,
	Authorize: func(r *http.Request) error { /* session or token check */ return nil },
	OnComplete: func(ctx context.Context, info upload.TusInfo, open func() (io.ReadCloser, error)) error {
		rc, err := open()
		if err != nil {
			return err
		}
		defer rc.Close()
		_, err = upload.Save(ctx, disk, rules, "file", info.Filename(), rc)
		return err
	},
})
r.Handle("/uploads/tus/", tus)
app.OnShutdown(func(context.Context) error { return tus.Close() })
```

- `OPTIONS` advertises `Tus-Version`, `Tus-Max-Size`, `Tus-Extension`; `POST` checks `Upload-Length` (≤ `MaxSize`) and `Upload-Metadata` (≤ 4 KiB, 32 pairs) and answers 201 with `Location: BasePath + <26-char random id>`; `HEAD` returns `Upload-Offset`/`Upload-Length` with `Cache-Control: no-store`; `PATCH` needs `Tus-Resumable: 1.0.0` (412 otherwise), `Content-Type: application/offset+octet-stream` (415) and the current `Upload-Offset` (409), and appends the body as it streams — an interrupted request keeps what arrived, and the client resumes from the offset `HEAD` reports. Concurrent `PATCH`es on one upload get 423.
- Partial data lives in `Dir` through `os.Root`. The id is the capability, so `Authorize` is required (boot error when nil; return `nil` explicitly to allow everyone). Abandoned uploads expire after `Expiry` (24h): requests on them get 404 and `tus.Cleanup(ctx)` (schedule it) deletes them. When the last byte arrives `OnComplete` runs; its error is sent to the client (422 for validation) and the partial data is deleted either way.

Direct-to-storage uploads skip the app server: `upload.Presigner` (`PresignPut(ctx, key, contentType, maxSize, ttl)`) is an interface you implement for your storage provider; `drivers/s3`'s `PresignPut(key, expires)` signs only the host, so wrap it (or another SDK) when the bucket must enforce the content type and size. `upload.Direct(DirectConfig{Presigner, Rules, Authorize})` answers `POST {"filename","content_type","size"}` with `{"key","url","method":"PUT","headers","expires_at"}` after checking the declared type, extension and size, under a random key in `Rules.Dir` (TTL 15 minutes, at most 1 hour). The declared type cannot be trusted, so call `upload.Verify(ctx, disk, key, rules)` when the client reports the upload done: it sniffs the stored object and deletes it when it breaks the rules.

The HTMX starter ships `resources/views/components/upload.html` (a `data-upload` form with a `<progress>`) and a `/uploads` demo page. `resources/js/app.ts` swaps htmx 4's `fetch` for `XMLHttpRequest` on those forms (`htmx:before:request`) to report upload progress; no inline scripts, so the CSP holds. Validate files received through `c.Input()` with [`z.File()`](validation.md#schemas).

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
limiters, err := app.RateLimiters() // RATE_LIMIT_STORE=memory (default) or cache
login := ratelimit.Middleware(limiters("login", 5, time.Minute), account.LoginThrottleKey)
r.Post("/login", h.Login).Use(login)
```

Limiters are keyed by `ratelimit.ByIP` (default), `ByToken` (after `auth.Required` only) or your own `KeyFunc`; responses include `RateLimit-*` headers, `429` adds `Retry-After`; a failing store denies. `install:auth` and `install:api` throttle logins, mails and tokens by default, through `app.RateLimiters()`.

| Store | Algorithm | Shared by servers |
| --- | --- | --- |
| `ratelimit.NewMemory(limit, window)` (`RATE_LIMIT_STORE=memory`) | GCRA, bursts up to `limit` | no |
| `ratelimit.NewWindow(counter, name, limit, window)` (`RATE_LIMIT_STORE=cache`) | fixed window on `Increment`; up to 2×`limit` around a window boundary | yes, with `CACHE_STORE=database` (SQLite, PostgreSQL, MySQL) or a Redis cache store |
| `rc.RateLimiter(prefix, limit, window)` ([Redis](#redis)) | GCRA in a Lua script | yes |

GCRA needs compare-and-set, which `cache.Store` lacks, so the store-agnostic limiter uses fixed windows: correct with an atomic `Increment` alone (`cache.Database.Increment` is one upsert, so concurrent servers never read the same count). Window keys are hashed, limiter names are 1–64 of `A-Z a-z 0-9 . _ -`, and `RATE_LIMIT_STORE=cache` over the memory cache is a boot error.

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
- **Outgoing webhooks:** `webhook.Sender` registers a `webhook.deliver` job (`s.Job()`) and `s.Send(ctx, q, webhook.Delivery{Endpoint, Payload})` enqueues it. Each attempt looks the endpoint up by id (URL and secret never enter the jobs tables), signs it with the Standard Webhooks scheme and posts through `httpclient.Safe`: 2xx succeeds, 410 Gone stops (`OnGone`), anything else retries (8 times, exponential from 30s). `webhook.SQLDeliveryLog` records each attempt. See [security.md](security.md#building-blocks).
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
- `serve` runs the scheduler in process by default (`SCHEDULE_IN_SERVE=true`), so a single-process deploy still runs the hourly `session:prune`/`cache:prune`; it starts only when tasks exist and stops with the app (stops ticking, waits for running tasks, then shutdown hooks). Set `SCHEDULE_IN_SERVE=false` when a dedicated process runs the schedule instead: `vukem schedule:work` (ticks at the start of every minute, drains running tasks on shutdown) or one cron entry: `* * * * * cd /app && ./app schedule:run`. `vukem schedule:list` shows each task's next run.
- Several replicas: each process running the scheduler fires every due task (no leader election). Tasks without `WithoutOverlapping()` run once per replica; `WithoutOverlapping()` with a shared lock store only skips a run while another replica still holds the lock, so a short task can still run on several replicas in the same minute (with the in-memory cache, locks are per process). Built-in prune tasks are idempotent; for run-once tasks set `SCHEDULE_IN_SERVE=false` and run a single `schedule:work`.
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

## OpenTelemetry

`go get github.com/AlexGalhardo/vukem/drivers/otel` (OpenTelemetry Go SDK v1.47, OTLP/HTTP exporter, semantic conventions v1.43).

```go
app, err := vukem.New(
	vukem.WithProviders(otel.New(otel.Config{})), // before routes: its middleware wraps the whole stack
	vukem.WithRoutes(routes),
)

r.UseServer(otel.Middleware(tp)) // or bring your own trace.TracerProvider
```

- `otel.Middleware(tp, opts...)` is `func(http.Handler) http.Handler`: one server span per request, the trace continued from the incoming `traceparent` (W3C TraceContext + Baggage by default, `otel.WithPropagator` to change it).
- The span is named `METHOD /route/{param}` after the matched ServeMux pattern, or just `METHOD` when nothing matched; raw paths never become span names. Installed with both `Router.UseServer` and `Router.Use` (what the provider does), the route-level copy names the span even when middleware in between replaced the request.
- Attributes: `http.request.method` (unknown methods as `_OTHER`), `http.route`, `http.response.status_code`, `url.scheme`, `server.address`, `server.port`, `network.protocol.version`, `user_agent.original`. Paths and query strings are not recorded: they can carry tokens and signatures.
- 5xx responses and panics set the span status to Error with `error.type`; 4xx stay unset, as the HTTP conventions ask. Panics are re-raised for `middleware.Recover`.
- The provider reads `OTEL_EXPORTER_OTLP_TRACES_ENDPOINT` (used as is) or `OTEL_EXPORTER_OTLP_ENDPOINT` (`/v1/traces` appended); a non-http(s) URL is a boot error, and with neither set tracing is off. `OTEL_SDK_DISABLED=true` turns it off too. Other `OTEL_EXPORTER_OTLP_*` variables (headers, timeout, compression) are read by the exporter itself.
- The resource carries `service.name` (`Config.ServiceName`, else `OTEL_SERVICE_NAME`, else `APP_NAME`) and `deployment.environment.name` (`APP_ENV`). Spans are batched and flushed by `app.Shutdown`.
- `Config.Exporter` replaces OTLP (tests use `tracetest.NewInMemoryExporter`), `Config.Sampler` defaults to `ParentBased(AlwaysSample)`, and `Config.Global` also sets the OpenTelemetry globals for libraries instrumented through them (off by default: no package-level state).
- Log correlation: `otel.LogHandler(next)` adds `trace_id` and `span_id` (hex) to records whose context carries a valid span; the provider wraps `app.Log` and the router's `web.Ctx.Log()` logger with it when tracing is on, so request logs and `c.Log().InfoContext(c.Context(), ...)` records link to their trace. Log with the `*Context` methods; records logged without a context are not correlated.
- Database spans are not built in: wrap `*sql.DB` drivers with an OTel `database/sql` instrumentation in the app if needed.

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

## Stripe (`contrib/stripe`)

Separate module (`github.com/AlexGalhardo/vukem/contrib/stripe`), standard library only (no stripe-go). Use a restricted key (`rk_…`).

- `stripe.New(key, stripe.WithRetries(2))`: every request pins `Stripe-Version: 2026-08-26.dahlia` (`stripe.APIVersion`), POSTs carry an `Idempotency-Key` (generated, or yours for `CreatePaymentIntent(ctx, params, "order-42")`) so retries on 429/5xx/network errors are safe, honouring `Retry-After` and `Stripe-Should-Retry`. Errors are `*stripe.Error{Status, Type, Code, DeclineCode, Message, Param, RequestID}`; test them with `errors.Is(err, stripe.ErrCardDeclined)` (also `ErrInvalid`, `ErrRateLimited`, `ErrAuth`). The client prints as `rk_…abcd`, never the key (`TestSecurity_A09_StripeKeyRedacted`).
- Resources: `CreateCustomer/GetCustomer/UpdateCustomer`, `CreateCheckoutSession/GetCheckoutSession` (never pass `payment_method_types`; dynamic payment methods come from the Dashboard), `GetSubscription/UpdateSubscription/CancelSubscription`, `CreatePortalSession`, `CreatePaymentIntent/GetPaymentIntent`, and `Do` for any other endpoint. `stripe.Params` nests (`line_items[0][price]`).
- Webhooks: `stripe.WebhookHandler(stripe.WebhookConfig{Secret, Store, Bus})` verifies `Stripe-Signature` (HMAC-SHA256 of `t.payload`, any of several `v1` signatures, ±5 min tolerance), claims the event id in a `cache.Store` (memory, database or Redis) so a duplicate delivery is acknowledged without processing, dispatches `stripe.Received{Event}` on the `events.Bus` and only then marks it done; a failing listener releases the claim and answers 500 so Stripe redelivers (`TestSecurity_A08_StripeWebhookSignature`). Decode the object with `stripe.Decode[stripe.CheckoutSession](ev)`. Mount it outside CSRF protection; fulfil orders from `checkout.session.completed`/`checkout.session.async_payment_succeeded`, never from the success page. `stripe.SignForTest` signs payloads for app tests.
- `vukem install:billing` scaffolds subscriptions on top of this module (plans, Checkout, Billing Portal, webhook sync, `Subscribed`/`OnPlan` middleware): see [billing.md](billing.md).
