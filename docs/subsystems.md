# Subsystems

Mail, cache, storage, events, queue, scheduler, encryption, rate limiting, i18n, logging and observability. All follow the **manager/driver** pattern: one typed config section, an interface, several drivers selected by config, a fake for tests. Drivers with heavy dependencies live in `drivers/*` modules.

| Subsystem | Phase | Core drivers | Driver modules | Fake |
| --- | --- | --- | --- | --- |
| Logging (`log/slog`) | Prototype / ALPHA | text (dev), JSON (prod), multi; **Telegram** handler (ALPHA) | — | capture handler |
| Health | Prototype | `/up` + per-service checks | — | — |
| Mail | ALPHA | SMTP (`net/smtp`), **Resend** and **Mailtrap** (HTTP APIs), log, array | — | ✅ |
| Cache | ALPHA | memory (LRU + TTL), database | redis | ✅ |
| Feature flags | v0.27 | code + `FLAG_<NAME>`, database (cached) | — | any `flags.Store` |
| Storage | ALPHA / BETA | local (`os.Root`), memory | s3 (AWS, R2, RustFS…) | ✅ |
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
- `/up` returns 200 when the app booted (liveness, no dependency touched). `app.AddHealthCheck` / `AddHealthCheckTimeout` register readiness checks (`app.DB()` adds `db`); `app.ReadyHandler()` (mount it at `/ready`) and `/up?checks=1` run them in parallel, each with its own timeout (default 3s), and answer 200 or 503 with `ok`/`fail` per check, never the error text. `app.CheckHealth(ctx)` returns the raw errors.

## Telegram logging

A `slog.Handler` (package `logging/telegram`) that sends selected records to Telegram chats, ported from the maintainer's TypeScript [telegram-logger](https://github.com/AlexGalhardo/telegram-logger). Stdlib only (`net/http`).

Apps turn it on with environment variables; the app logger then fans out to stderr and Telegram (`slog.NewMultiHandler`):

```sh
TELEGRAM_BOT_TOKEN=123456:ABC...   # config.Secret, never logged
TELEGRAM_CHAT_ID=-1001234567890
TELEGRAM_MIN_LEVEL=error           # debug, info, warn, error
TELEGRAM_THREAD_ID=42              # optional forum topic
TELEGRAM_LOG=true                  # false: no log forwarding, app.Telegram only for Send/Contact
TELEGRAM_RATE_LIMIT=20             # optional cap on forwarded log records per minute (0: none)
TELEGRAM_API_URL=https://api.telegram.org  # optional: self-hosted Bot API server or a fake; https outside development/test
```

```go
app.Telegram.Contact(ctx, map[string]string{"name": in.Name, "message": in.Message}) // never filtered by level
app.Telegram.Send(ctx, "<b>Deploy</b> finished")                                     // pre-formatted HTML

// In a queued job: deliver now and return the error so the job retries.
err := app.Telegram.ContactSync(ctx, fields) // or SendSync; telegram.ErrDisabled when not configured

// Only forward the records you mark (Filter sees log records, never Send/Contact).
vukem.WithTelegram(func(c *telegram.Config) {
	c.Filter = func(_ context.Context, r slog.Record) bool { return r.Level >= telegram.LevelFatal }
})

// Standalone: per-level routing, silent mode, custom error handling.
tg := telegram.NewHandler(telegram.Config{
	BotToken: token, ChatID: "-100", ChatIDs: map[slog.Level]string{telegram.LevelFatal: "-200"},
	MinLevel: slog.LevelWarn, AppName: "Shop", Env: "production", Silent: true, OnError: report,
})
defer tg.Close(ctx)
```

| Feature | Detail |
| --- | --- |
| Levels | debug, info, warn, error plus `telegram.LevelSuccess` and `telegram.LevelFatal`; `MinLevel` filter (`slog.LevelInfo` is the zero value: set `MinLevelSet` in a standalone config; `vukem.New` does it for `TELEGRAM_MIN_LEVEL`) |
| Routing | per-level chat ids, forum topic (`ThreadID`), `Silent` |
| Format | Telegram HTML: emoji and level, app name, environment, UTC time, then the message and attributes (groups dotted, error chains as "caused by") in a `<pre>` block; all content escaped; truncated to 4096 characters without cutting tags or entities; `Send`/`SendSync` truncate the caller's HTML outside tags and entities and close the tags left open |
| Reliability | bounded queue (`QueueSize`, drops and reports when full), per-request `Timeout`, one retry after HTTP 429 honouring `retry_after` capped by `MaxRetryAfter`, `OnError` (never reentrant; records logged from it or carrying its error are not forwarded, so it may log through the app logger), drained by `app.Shutdown`; `SendSync` / `ContactSync` skip the queue and return the error (bounded by `Timeout` per request and by ctx) |
| Log forwarding | on by default for records at `MinLevel`+; `TELEGRAM_LOG=false` turns it off; `Filter` and `RateLimit`/`RatePeriod` (`TELEGRAM_RATE_LIMIT` per minute, `ErrRateLimited` reported once per window) narrow it; `vukem.WithTelegram` edits the config in code |
| Security | token held as `config.Secret`; request errors are redacted, also from `SendSync`/`ContactSync` (`TestSecurity_A09_TelegramTokenRedacted`, `TestSecurity_A09_TelegramSyncErrorRedactsToken`); `vukem about` shows only on/off; disabled when token or chat is missing (`app.Telegram` is then a disabled handler, never nil: `Configured()` is false, `Send` drops, `SendSync` returns `ErrDisabled`); `TELEGRAM_API_URL` (default `https://api.telegram.org`, the token travels in its path) must be https outside development and test, without credentials, query or fragment, and a bad value fails boot without echoing it (`TestSecurity_A04_TelegramAPIURLRequiresHTTPS`); `WithTelegram` setting `BaseURL` still wins |

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
- Context: implement `EnvelopeContext(ctx) mail.Envelope` (`mail.ContextEnveloper`) to build the envelope from the send's context, e.g. a subject in the request locale with `i18n.T(ctx, key)`. The Mailer calls it instead of `Envelope`, which stays required as the context-free fallback. `SendLater` renders before queueing, so it uses the context of the call.
- Addresses go through `net/mail` (no header injection); subjects, tags and content ids with line breaks are rejected (`TestSecurity_A05_MailHeaderInjection`); `Mailer.Render` checks tags for every transport (non-empty name, no line break or colon), and the Resend transport also enforces Resend's charset (ASCII letters, digits, `_`, `-`) before the request. `Bcc` is never written as a header.
- Previews in development: `r.Handle("/_mail/", http.StripPrefix("/_mail/", mail.Preview(mailer, map[string]mail.Mailable{"welcome": sample})))`.
- Tests: `MAIL_MAILER=array` by default in `vukemtest`; `app.Mails().SentTo("ana@example.com")`.
- Queued mail: `mailer.SendLater(ctx, mailable, opts...)` (or `app.MailLater`) renders now and sends from a retried queue job whose payload is encrypted with `app.Crypt` (see [Queue](#queue)). A `*mail.Mailer` built with `mail.New` needs `SetEnqueue`; otherwise `SendLater` returns `mail.ErrNoQueue`. `mailer.SendLaterTx(ctx, tx, mailable)` (or `app.MailLaterTx`) queues it only if the transaction commits (`SetEnqueueTx`, `Queue.DispatchKindTx`).

| `MAIL_MAILER` | Transport | Settings |
| --- | --- | --- |
| `smtp` | `net/smtp`, STARTTLS required (or implicit TLS on 465), AUTH PLAIN; multipart/mixed → related → alternative MIME, base64 parts | `SMTP_HOST`, `SMTP_PORT` (587), `SMTP_USERNAME`, `SMTP_PASSWORD` (secret), `SMTP_INSECURE` (local catchers only) |
| `resend` | Resend HTTP API (`POST /emails`): tags, reply-to, attachments with content ids, the message id as `Idempotency-Key`, retries on 429 (honouring `Retry-After`, capped), 5xx and network errors (safe thanks to the idempotency key), typed `*mail.ResendError` | `RESEND_API_KEY` (secret, never logged: `TestSecurity_A09_ResendKeyRedacted`) |
| `mailtrap` | Mailtrap Email API (`POST send.api.mailtrap.io/api/send`, or `sandbox.api.mailtrap.io/api/send/{inbox}` with `MAILTRAP_INBOX_ID`): cc/bcc, one reply-to, attachments and inline images (`disposition: inline` + `content_id`), the `category` tag as `category` and other tags as `custom_variables`, retries on 429 (honouring `Retry-After`, capped), 5xx and failed connections (DNS, refused: the request never left; a reset or timeout after sending is not retried), a 2xx with `success: false` is a non-retryable error, typed `*mail.MailtrapError` (never the body or token; may quote addresses; at most 5 one-line items of 200 characters), redirects not followed. A sandbox inbox in production logs a warning. No idempotency key: a 5xx answered after delivery may send twice | `MAILTRAP_API_TOKEN` (secret, required, never logged: `TestSecurity_A09_MailtrapTokenRedacted`), `MAILTRAP_INBOX_ID` (sandbox) |
| `log` | logs recipients and subject (bodies at debug) | default outside production |
| `array` | in-memory (tests) | default in `vukemtest` |

`MAIL_FROM` sets the default sender. Production must set `MAIL_MAILER` explicitly; a missing or unknown value is an error, never silent loss of mail. An explicit `MAIL_MAILER` that cannot work (`resend` without `RESEND_API_KEY`, `mailtrap` without `MAILTRAP_API_TOKEN` or with a negative `MAILTRAP_INBOX_ID`, an unknown name) fails `app.Boot`, so `serve`, workers and commands stop at startup instead of at the first email. `WithMailTransport` skips the check.

An **unset** `MAIL_MAILER` in production is refused where mail is actually sent:

| Process | When it is refused |
| --- | --- |
| `serve` | at boot, as soon as anything asks for `app.Mailer()` |
| `queue:work`, `schedule:work` | at boot, through `app.RequireMailer()` — a worker sends queued mail as its normal work |
| any other command | at the `Send` call, with the same message |

The last row is the point: `install:auth` registers routes that build a mailer, so refusing at construction stopped **every** command. `about`, whose whole job is to report configuration, exited 1 for a reason unrelated to what it does, and a freshly generated image could not smoke-test itself (`docker run <image> about`). An app that sends no mail never needs to configure one. Call `app.RequireMailer()` at the start of any long-running process of your own that sends mail, so it fails at deploy time rather than per job. Hosts that block outbound SMTP (Railway on some plans, other PaaS) time out on `smtp`; use an HTTP transport (`resend` or `mailtrap`) there.

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
- Stores implement `cache.Store` (`Get`, `Set`, atomic `Add`, `Delete`, owner-checked `DeleteIf`, `Increment`, `Flush`): `cache.NewMemory(maxEntries)` (LRU + TTL) and `cache.NewDatabase(db, table)` (SQLite, Postgres and MySQL upserts; `Increment` is one atomic upsert, so concurrent servers never read the same count; `CreateTable` indexes `expires_at`, and `cache.Migrations` ships the same DDL for apps that manage the `cache` table with `migrate` ([tables the framework owns](database.md#tables-the-framework-owns)); `Prune` removes expired rows in batches and every app with `CACHE_STORE=database` schedules `cache:prune` hourly). Redis comes with `drivers/redis` ([Redis](#redis)). `cache/cachetest.Run` is the conformance suite (concurrent `Increment` included), run on memory, SQLite, Postgres, MySQL, MariaDB and Redis.
- Shared stores: `cache.Shared(store)` reports whether every server sees the same entries; `OnOneServer()` and `RATE_LIMIT_STORE=cache` use it. A store declares it by implementing `cache.Sharer` (`Shared() bool`): memory `false`, database and Redis `true`. A wrapper (metrics, tracing) forwards it with its own `Shared()` or by exposing the inner store with `Unwrap() cache.Store`; a store with neither is not shared (fail closed). `cachetest.Run` requires `cache.Sharer`.

## Feature flags

Typed flags declared in code with a default, overridden by configuration and by stored values; inspired by Laravel Pennant, without its per-flag resolver closures.

```go
// app/features/features.go (vukem install:flags)
var (
	NewCheckout    = flags.Bool("new_checkout", false).Describe("Serve the new checkout")
	UploadLimitMB  = flags.Int("upload_limit_mb", 10)
	AdminTwoFactor = flags.Bool("admin_two_factor", true).Lock() // never switchable
)

func All() []flags.AnyFlag { return []flags.AnyFlag{NewCheckout, UploadLimitMB, AdminTwoFactor} }

// bootstrap: vukem.WithFlags(features.All()...)
ff, err := app.Flags()
if ff.Enabled(ctx, features.NewCheckout) { ... }
limit := flags.Get(ctx, ff, features.UploadLimitMB)

r.Group("/checkout/v2", func(g *router.Group) { g.Use(ff.Require(features.NewCheckout)) }) // 404 while off
ctx = flags.WithScope(ctx, flags.Scope("user:"+u.AuthID()))                             // per-user values
_, err = flags.Put(ctx, ff, "tenant:7", features.UploadLimitMB, 50)
```

- **Kinds**: `flags.Bool`, `flags.Int`, `flags.String` (at most 1024 bytes), constrained by `flags.Type`. Names are lowercase letters, digits and `_` (up to 100). `Describe` adds the admin panel text, `Lock` pins the code default.
- **Resolution**, lowest first: code default, `FLAG_<NAME>` (`FLAG_NEW_CHECKOUT=true`), the stored global value, the stored value of the request scope (`flags.WithScope(ctx, scope)`, one `flags.Scope` per request, such as `user:42` or `tenant:7`). Locked flags always have their code default.
- **Scopes** are internal ids, never raw user input: 1 to 191 bytes of `a-z 0-9 : . _ -` (`flags.Global` is `""`). A read with an invalid scope returns the default and logs a warning; a write fails with `ErrInvalid`.
- **Boot errors** (`vukem.New`): invalid or duplicate names, a `FLAG_<NAME>` value of the wrong kind, `FLAG_<NAME>` for a locked flag, and `FLAG_*` keys in `.env` files that name no registered flag, also when no flag is registered at all (process variables cannot be listed, so a typo there is not caught). Reading a flag that was not registered returns its code default and logs an error once per name; `Require` and `Share` log it when called, at boot, and treat the flag as off.
- **Store**: `flags.Store` (`Load`, `Save`, `Delete` by scope). `app.Flags()` uses `flags.NewDatabaseStore` over `DB()`, table `feature_flags` (`flags.Migration`, added by `vukem install:flags`; `name`, `scope` (`""` = global), `value`, `updated_at`, unique `(name, scope)`; SQLite, Postgres and MySQL upserts). Without registered flags `app.Flags()` opens neither the database nor the cache.
- **Cache**: stored values per scope are read through `app.Cache()` with `flags.DefaultTTL` (30 s), under a key that carries a per-scope generation; `Set`, `Put` and `Reset` bump it, so a load that was in flight during a write cannot cache the old values again. Instances sharing the cache (`CACHE_STORE=database` or Redis) see a change at once; with the default memory cache each instance sees it **within the TTL**, so use a shared cache when a flag is a kill switch that must apply immediately everywhere.
- Known limit: a read costs one or two cache lookups (generation and values) plus a JSON decode of the scope's map; hot loops should read a flag once per request.
- **Fail closed**: when the store or the cache errors, reads return the default (the code default, or `FLAG_<NAME>` when set), never a stored value. A kill switch must therefore default to its safe value in code (or be `Lock`ed): during an outage the code default wins over what the admin stored. A failed read logs a warning (deduplicated by the app logger) and never fails a request and `Require` responds 404. A stored value that no longer parses as the flag's kind is ignored. `Set`/`Reset` return `ErrUnknown`, `ErrLocked`, `ErrInvalid`, `ErrNoStore` or the store's error; `States` (the admin view) returns the store's error instead of guessing. `ff.All()` describes the registered flags as `[]flags.Info`.
- **Audit**: `Change` holds the value before and after a write; `Sensitive` is true for string flags, which must be logged or audited only redacted (the admin panel records length and a SHA-256 prefix).
- **Views and Inertia**: `ff.Expose(ctx, features.NewCheckout)` returns `map[string]any{"new_checkout": false}` for view data; `ff.Share(features.NewCheckout)` (after `inertia`'s middleware) adds it as the `flags` prop of every page. Only the flags passed are exposed, never all of them.
- **Admin panel**: `admin.Config{Flags: ff}` adds `/admin/_flags` behind the `manageFlags` gate ability ([admin.md](admin.md#feature-flags)).
- **Tests**: set `FLAG_<NAME>` in `vukemtest.Options.Env`, or `flags.Put(ctx, ff, flags.Global, features.X, true)` against the test database.
- Out of scope: percentage rollouts, A/B variants and rule engines; build them in app code over `WithScope` and a flag's value.

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

- `storage.Disk`: `Put` (atomic rename on local disks, through a hidden `.name.tmp-…` file that `List` and `Handler` skip), `Open`, `Exists`, `Delete`, `List`, `URL`; helpers `storage.Get` and `storage.PutBytes`. Disks: `NewLocal` and `NewMemory` (tests); S3 in `drivers/s3` ([S3](#s3)). `storage/storagetest` holds the conformance and traversal suites (`TestSecurity_A01_StorageTraversal`).
- `PutUploaded` never uses the client's file name: random name, extension from the type detected in the content (`http.DetectContentType`), size checked from the header and again while copying, 422 for disallowed types or sizes. `UploadRules.Types` is required: an empty list is an error, not "any type" (`TestSecurity_A05_PutUploadedRequiresTypes`).
- `Handler` never serves dotfiles or directories, sends the file's real modification time as `Last-Modified` (none when the disk cannot report it) so `If-Modified-Since` answers 304, sends `X-Content-Type-Options: nosniff`, and with a `signed.Signer` only answers signed, unexpired URLs.
- `storage.Check(ctx, disk)` is the readiness check for a disk: it writes a small object under a unique key below `.vukem-health/`, reads it back, compares the bytes and deletes it, with the delete running through `context.WithoutCancel` so an abandoned probe leaves nothing behind. Use it from `vukem.WithHealthCheck`/`app.AddHealthCheck`, never in a liveness probe. The round trip is the point: the obvious check — does a known object exist? — **passes when nothing works**, because with the wrong credentials several S3 implementations answer "object does not exist" rather than refusing, and reading the bytes back also catches a disk that is up and returning the wrong object. Every error wraps `storage.ErrUnhealthy` and none wraps the driver's, which carries the endpoint, the bucket and sometimes the access key.
- Decision: instead of `storage:link` (a symlink, which needs extra privileges on Windows), public files are served by `storage.Handler` from the disk itself.

### S3

`go get github.com/AlexGalhardo/vukem/drivers/s3`: a `storage.Disk` on Amazon S3 or any S3-compatible service (Cloudflare R2, DigitalOcean Spaces, Backblaze B2, RustFS, SeaweedFS, Garage). No AWS SDK: requests are signed by a stdlib Signature Version 4 implementation verified against the AWS test suite.

```go
cfg, err := config.Load[s3.Config](app.Source)
disk, err := s3.New(cfg) // validates config, no I/O

err = disk.Put(ctx, "videos/intro.mp4", file)            // one PUT up to S3_PART_SIZE, multipart above
info, err := disk.Stat(ctx, "videos/intro.mp4")          // HEAD: Size, ETag, ContentType, LastModified
up, err := disk.PresignPut("uploads/avatar.png", 5*time.Minute) // browser uploads straight to the bucket
up, err := disk.PresignPutWith("uploads/avatar.png", 5*time.Minute, s3.PresignPutOptions{
	ContentType: "image/png", ContentLength: size, // signed: the upload must send exactly these
})
link, err := disk.PresignGet("invoices/42.pdf", time.Hour)
```

| Variable | Default | Meaning |
| --- | --- | --- |
| `S3_BUCKET` | — | bucket name (DNS-compatible: 3–63 of `a-z 0-9 . -`) |
| `S3_REGION` | `us-east-1` | signing region (`auto` for R2) |
| `S3_ENDPOINT` | `https://s3.<region>.amazonaws.com` | bare origin of an S3-compatible service |
| `S3_ACCESS_KEY_ID`, `S3_SECRET_ACCESS_KEY` | — | required; `S3_SESSION_TOKEN` for temporary credentials |
| `S3_USE_PATH_STYLE` | `false` | `endpoint/bucket/key` instead of `bucket.endpoint/key` (most self-hosted servers) |
| `S3_INSECURE_HTTP` | `false` | allow an `http://` endpoint (local development only) |
| `S3_PART_SIZE` | 8 MiB | multipart part size, 5 MiB to 5 GiB |
| `S3_PUBLIC_URL` | — | prefix for `disk.URL` (CDN or public bucket); empty = private |

- `Put` buffers at most one part: small bodies go in one signed `PUT`, larger ones become a multipart upload that is aborted on any failure (part error, reader error, cancelled context, a `200` that carries an `<Error>`), so no orphaned parts are billed. Content type comes from the extension, else from the first bytes.
- `Open` streams the object; `List` uses ListObjectsV2 and follows continuation tokens; folder placeholder keys (ending in `/`) are skipped. Missing objects match `fs.ErrNotExist`; other failures are `*s3.Error` (`StatusCode`, `Code`, `Message`, `RequestID`).
- `disk.CreateBucket(ctx)` creates the bucket in `S3_REGION` (a `LocationConstraint`, except for `us-east-1` and `auto`), for dev setup and tests against a fresh local S3 (RustFS, MinIO). A bucket the credentials already own is not an error; a name taken by another account is an `*s3.Error` with `Code` `BucketAlreadyExists`.
- Keys go through `storage.Clean`: no empty key, no leading `/` (so `/a` and `a` cannot name different objects), no `..`, backslash or `:`.
- Presigned URLs expire after 1 s to 7 days (the SigV4 maximum). `PresignGet` and `PresignPut` sign only the `host` header, so a `PresignPut` URL accepts any type and size. `PresignPutWith` also signs `Content-Type` and/or `Content-Length` (`s3.PresignPutOptions`): the uploader must send exactly those values or S3 answers 403. Browsers set `Content-Length` from the body but must send the `Content-Type` header explicitly (`fetch(url, {method: "PUT", headers: {"Content-Type": type}, body: file})`); invalid options return `s3.ErrInvalidPresign`. A signed `Content-Length` is an exact size, not a ceiling: pass the real file size the client declared (after checking it against your maximum), never the maximum itself, or every smaller file gets 403. Credentials are `config.Secret`; errors and `%+v` never show them.
- Integration tests run against RustFS (`rustfs/rustfs`, pinned in `.config/compose.yaml`; `task services:up`, port 59000); the driver passes `storagetest.Run` and `storagetest.RunTraversal`.

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
- **`Rules.Types` is only as strong as the detector, and says so.** Detection is `http.DetectContentType`, whose vocabulary is narrow: `image/avif`, `image/heic`, `image/heif`, `image/jxl` and `image/tiff` are **not** in it (measured on Go 1.27.2 — the detector answers `application/octet-stream` for all five). A rule naming one of them used to be accepted and then match nothing, so the application believed it accepted AVIF and accepted none. Those types are now an `upload.ErrUndetectableType` configuration error listing `upload.DetectableTypes()`. The escape hatch is explicit: `application/octet-stream` **is** detectable (it is what the detector says when it cannot tell), so a service handling AVIF or TIFF allows that and checks the type itself with a real sniffer. Doing it deliberately is the point; a rule that quietly matches nothing is not.
- `upload.Save(ctx, disk, rules, field, filename, r)` applies the same checks to any reader (finished tus uploads, imports).
- Scanning: with `Rules.Scanner`, files are written to `QuarantineDir/.<random>` (dot-named, so `storage.Handler` never serves them), scanned, then copied to `Dir` and the quarantined copy deleted. `ErrInfected` → deleted + 422; a scanner error → deleted + 503 (fail closed), with the scanner error kept as the `*errs.HTTPError` cause for the log. `upload.ClamAV(addr)` speaks clamd's `INSTREAM` over TCP with per-operation timeouts.
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
- Partial data lives in `Dir` through `os.Root`. The id is the capability, so `Authorize` is required (boot error when nil; return `nil` explicitly to allow everyone). Abandoned uploads expire after `Expiry` (24h): requests on them get 404 and `tus.Cleanup(ctx)` (schedule it) deletes them. When the last byte arrives `OnComplete` runs; its error is sent to the client (422 for validation; 5xx errors are also logged with their cause through the request logger) and the partial data is deleted either way. Validation means `*errs.ValidationError`, `z.FieldError` or `z.Issues` (the first message is the response text; a plain `Refine` error reads "The value field is invalid."); an `Authorize` error keeps its 4xx status (422 for z errors, anything else 403) but never its text.

Direct-to-storage uploads skip the app server: `upload.Presigner` (`PresignPut(ctx, key, contentType, maxSize, ttl)`) is an interface you implement for your storage provider; wrap `drivers/s3`'s `PresignPutWith(key, ttl, s3.PresignPutOptions{ContentType: contentType, ContentLength: maxSize})` and return `Content-Type` in `headers`, so the bucket enforces the type and size; plain `PresignPut(key, expires)` signs only the host. Despite its name, `Direct` passes the client's declared `size` (already checked against `Rules.MaxFileSize`) as `maxSize`, which is what a signed `Content-Length` needs: it is an exact size, so a wrapper that signed `Rules.MaxFileSize` would reject every smaller file with 403. The client must send exactly the size it declared. `upload.Direct(DirectConfig{Presigner, Rules, Authorize})` answers `POST {"filename","content_type","size"}` with `{"key","url","method":"PUT","headers","expires_at"}` after checking the declared type, extension and size, under a random key in `Rules.Dir` (TTL 15 minutes, at most 1 hour). The declared type cannot be trusted, so call `upload.Verify(ctx, disk, key, rules)` when the client reports the upload done: it sniffs the stored object and deletes it when it breaks the rules.

The HTMX starter ships `resources/views/components/upload.html` (a `data-upload` form with a `<progress>`) and a `/uploads` demo page. `resources/js/app.ts` swaps htmx 4's `fetch` for `XMLHttpRequest` on those forms (`htmx:before:request`) to report upload progress; no inline scripts, so the CSP holds. The demo stores files under `UPLOADS_DIR` (default `storage/app/uploads`); `UPLOADS_DISK=memory` swaps in `storage.NewMemory` for tests (refused in production), so the generated `TestUploads` never writes to the filesystem: on Windows, antivirus filters briefly recreate just-deleted files, which can make `t.TempDir` cleanup fail with "directory is not empty". Use a memory disk the same way in your own upload tests. Validate files received through `c.Input()` with [`z.File()`](validation.md#schemas).

## Archives

```go
entries, err := archive.Zip(readerAt, size, archive.Policy{
	MaxEntries: 512, MaxTotalSize: 64 << 20, MaxEntrySize: 8 << 20,
})
// entries[i].Key is safe to use as an identifier; Open() is bounded by Size.
```

`archive.Zip` reads the central directory under a policy and returns entries whose names are already safe keys. Every application that accepts a user-supplied archive needs the same defences, and getting any one wrong is a vulnerability rather than a bug, so they are here:

| Defence | What it does |
| --- | --- |
| entry count | `MaxEntries`, checked before anything is read |
| declared size | `MaxEntrySize` and `MaxTotalSize`, read from the **central directory**, so a bomb is refused on its claim rather than by inflating it |
| no paths | `Zip` takes no path of any kind and returns a key, so an entry name can never be an argument to a file operation (`TestSecurity_A01_ArchiveTakesNoPath` pins the signature) |
| name length and shape | `MaxNameLength`, plus a refusal for control characters and direction-changing runes — the name does not reach a path, but it does reach the metadata and from there a consumer's asset loader |
| key collisions | two entries resolving to one key are refused |
| bounded reads | `Entry.Open` is held to the declared size, so the declared total bounds what the archive can ever yield |

The collision one is the finding's own argument, and it is the one that **corrupts instead of failing**: `walk/frame.png` and `jump/frame.png` collapse to one key once the directory is dropped, so an app writes both frames under one name while its metadata describes one — every later frame off by one, a wrong result delivered as a correct one.

An escaping raw name (`../../etc/passwd`, an absolute path, a drive letter) is refused before `Policy.Key` sees it. The default `Key` is `path.Base`, which would quietly turn that into the perfectly valid key `passwd`: safe to use, an archive that should never have been accepted, and nothing in the log to say so.

What stays with the application is the per-entry content rule: what a frame may contain, how big an image may be, which types are acceptable. Errors (`ErrPolicy`, `ErrTooManyEntries`, `ErrTooLarge`, `ErrNameTooLong`, `ErrUnsafeName`, `ErrDuplicateKey`) never carry the raw entry name, which is attacker-controlled text a naive handler would put in a response body.

## Media types an app produces

`mediatype.Extension("image/avif")` → `.avif`, `mediatype.Label("image/avif")` → `AVIF image`, `mediatype.ByExtension(".avif")` → `image/avif`. This is the other direction from `upload.Extensions`: what to call a file the application **generated** and hands back as a download, independent of what any detector can recognise.

It is deliberately **not** a set of types an application accepts, and it is built so it cannot be used as one — three lookups, no exported list, no iterator, no `Known` predicate, pinned by `TestNoExportedListToMistakeForAnInputAllowlist`. An input policy is per application and is often the only thing keeping a vulnerable decoder unreachable: an app may rely on "no tool here accepts XML input", and a shared list that grew a type would take that argument away with no edit to the app. Validate input with `upload.Rules` or a detector of your own.

## Exports

Package `export` (stdlib only) streams downloads row by row from any `iter.Seq2[T, error]`, such as `orm` `Query.Each`, so large exports never sit in memory. Guide: [Exports](https://vukem.dev/docs/digging-deeper/exports).

- `export.CSV(w, r, name, header, rows, record)`, `export.JSON` (one array), `export.NDJSON` (`application/x-ndjson`), and `export.WriteCSV(w, r, name, fn)` for multi-section CSV files.
- Attachments with `export.ContentDisposition(name)` (RFC 6266, RFC 5987 `filename*` for non-ASCII), `Cache-Control: no-store` and `nosniff`.
- CSV cells go through `export.SafeCell` (OWASP CSV injection); `InsecureRawCells()` opts out.
- Nothing is sent before the first row, so an early query error reaches the error handler. A failure after the first byte drops the connection: HTTP/1.x connections are hijacked and closed and the error wraps `export.ErrIncomplete`; where hijacking is impossible (HTTP/2) the function panics with `http.ErrAbortHandler`, which net/http recovers to reset the stream. A cancelled request context (client gone) returns an error with status 499, logged below error level.
- `FlushEvery(n)` (500 rows), `WriteTimeout(d)` (1 minute, renewed before writes to the client at most every `d/4` or 1s, cleared at the end, so long exports outlive the server `WriteTimeout`), `WithBOM()` for Excel (CSV only).
- The file-name sanitizer lives in `internal/disposition` and is shared with `web.Ctx.Download`.

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

- Typed by generics: `events.On(fn)` infers the event type from the function. Synchronous listeners run in registration order and the first error stops the chain and is returned (a panic is recovered, logged as `listener panicked` and returned as an error); listeners are named by their function (`listeners.SendWelcome`) or, when queued, their job kind; async ones (`OnAsync`) run on a bounded pool with a context detached from the request, log their errors and panics, and are drained by `app.Shutdown`.
- Tests: `events.NewFake()` records dispatches; `events.Dispatched[OrderShipped](fake)`.
- Queued listeners: `events.OnQueued("kind", fn)` enqueues the event and runs `fn` in a queue worker (see [Queue](#queue)).

## Rate limiting

```go
limiters, err := app.RateLimiters() // RATE_LIMIT_STORE=memory (default) or cache
login := ratelimit.Middleware(limiters("login", 5, time.Minute), account.LoginThrottleKey)
r.Post("/login", h.Login).Use(login)
```

Limiters are keyed by `ratelimit.ByIP` (default), `ByToken` (after `auth.Required` only), `ByUser(guard)` or your own `KeyFunc`; responses include `RateLimit-*` headers, `429` adds `Retry-After`; a failing store denies.

Choosing the key is the whole decision:

| Key | Bucket | When |
| --- | --- | --- |
| `ByIP` | client address (IPv6 grouped by `web.Services.IPv6Prefix`) | anything reachable unauthenticated |
| `ByToken` | the bearer token, hashed; the IP when there is none | API routes, behind an IP limit |
| `ByUser(guard)` | the account the guard authenticated; the IP when nobody is | an authenticated action whose budget belongs to the account |

`ByUser` is the key for a limit like "10 new API keys an hour". `ByIP` punishes everybody behind one NAT while an attacker rotates addresses, and `ByToken` finds no bearer token on a session request, so it falls back to the IP and a stolen session can mint keys faster than the owner can revoke them. An unauthenticated caller gets its IP key, never a shared one: a constant key would let any caller exhaust everybody's budget and an empty one would skip the limit entirely — so put an IP limit in front as well when the route is reachable without authentication. `install:auth` and `install:api` throttle logins, mails and tokens by default, through `app.RateLimiters()`. `vukem.WithRateLimiters(factory)` replaces the `RATE_LIMIT_STORE` factory (a limiter of your own, or a fake in tests).

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

// app/jobs/registry.go; bootstrap passes vukem.WithJobsFunc(jobs.All)
func All(app *vukem.App) []queue.Registration {
	return []queue.Registration{
		queue.Handle(HandleSendInvoice(app),
			queue.Retries(5), queue.Backoff(queue.Exponential(time.Second)), queue.Timeout(time.Minute)),
	}
}

q, err := app.Queue()
err = q.Dispatch(ctx, SendInvoice{OrderID: 1}, queue.OnQueue("billing"), queue.Delay(5*time.Minute))
err = q.DispatchTx(ctx, tx, SendInvoice{OrderID: 1}) // exists only if tx commits (every driver)
```

- Payloads are JSON; `Kind()` maps to the registered handler (no reflection-based class names). Registering a kind twice is a boot error.
- Defaults: queue `default`, 3 retries with exponential backoff from 1s (capped at 1h), 1-minute timeout per attempt; options on `Handle` set the defaults, options on `Dispatch` override them.
- `QUEUE_CONNECTION`: `sync` (default; the job runs inside `Dispatch` and its error is returned; a delayed job is refused with `queue.ErrInlineDelay` instead of running early, so use `memory`, `database` or `queue.Fake` where jobs are delayed), `memory` (in process; `serve` runs workers, `QUEUE_CONCURRENCY` default 4, `QUEUE_NAMES` in priority order), `database` (SQLite, Postgres or MySQL; see below). Any other value is an `app.Queue()` error. Redis is not a `QUEUE_CONNECTION` value: wire it with `vukem.WithQueueDriver` / `WithQueueDriverFunc` ([Redis](#redis)).
- `serve` runs a queue worker: always for `memory`, and for `database` and custom drivers (`WithQueueDriver`, e.g. Redis) unless `QUEUE_IN_SERVE=false` (default `true`, mirroring `SCHEDULE_IN_SERVE`). A worker in `serve` next to `queue:work` processes is safe (atomic reservations with an owner token), it only adds workers. The runner exits at once for an inline driver.
- **Permanent failures:** a handler that wraps `queue.ErrPermanent` (`fmt.Errorf("convert %d: %w: %w", id, err, queue.ErrPermanent)`) skips its remaining retries. The job is *failed*, not acked: it lands in the failed jobs with the handler's error, so `queue:failed` still shows it and `queue:retry` can re-run it once the input is fixed — acking would delete a job nobody ever saw fail. It beats both the retry budget and the free-interruption grace, since the handler's verdict is better information than either. On an Inline driver (`sync`) there are no retries to skip and the error reaches the `Dispatch` caller unchanged, wrapping the sentinel.
- **Attempt in the handler's context:** `a, ok := queue.AttemptFrom(ctx)` returns `queue.Attempt{Number, MaxRetries}` plus `a.Last()` (`Number > MaxRetries`), so a handler can settle the row it owns on its last attempt instead of leaving it `running` forever when the retries run out. `ok` is false outside a worker (a handler function called directly in a test), where a zero `Attempt` would read as a last attempt. The context carries it because every handler already takes `ctx` first: the alternative — a wider handler signature — would break `queue.Handle`/`HandleKind` and every registered handler, including the framework's own (queued mail, queued listeners, webhook delivery). With an Inline driver (`sync`) the job gets exactly one attempt, so `MaxRetries` is 0 and `Last()` is true whatever `Retries` says; a handler that relies on `Last()` therefore behaves the same in `sync` development and in a worker.
- Workers (`q.Work`) recover panics, retry with backoff, then move the job to the failed jobs with its last error. On shutdown they stop reserving and let running jobs finish (their context is not cancelled; the job timeout still applies) for up to `WorkOptions.StopTimeout` (`SHUTDOWN_TIMEOUT` in `serve` and `queue:work`); then their context is cancelled. A job that fails with that cancellation (`context.Canceled`/`DeadlineExceeded`) is released without using an attempt, `queue.MaxFreeInterruptions` (3) times in a row (the count is kept in `LastError`, so no driver needs a schema change); later interruptions use attempts and other errors are retried or failed as usual. Interrupted jobs run again from the start: handlers must be idempotent and honor `ctx`. Each `Ack`/`Release`/`Fail` of a worker is bounded by 10s, so a hanging store cannot hold `queue:work` or the shutdown.
- Time: `queue.Delay`, retry backoff and the drivers' availability checks use one clock. `app.Queue()` sets the app clock (`WithClock`) with `Queue.SetClock` and on drivers implementing `queue.Clock` (`Memory`, `Database`, Redis `Queue`), so fake clocks make delayed jobs due.
- **Database driver** (`QUEUE_CONNECTION=database`, SQLite, Postgres and MySQL): tables `jobs` and `failed_jobs` (`QUEUE_TABLE`, `QUEUE_FAILED_TABLE`; `jobs` collides with the obvious name for a domain table, see [tables the framework owns](database.md#tables-the-framework-owns)) are created at `Boot` (on first use when the app is not booted; an unreachable database then fails the boot of every command with an error naming `QUEUE_CONNECTION=database`), so the first `app.Queue()` never runs DDL inside a transaction (on SQLite that would wait for the writer connection the transaction holds). Jobs available and created in the same millisecond pop in push order within one process: the jobs table has a `seq BIGINT NOT NULL DEFAULT 0` column, a per-driver counter seeded by the wall clock, and `Pop` orders by `available_at, created_at, seq, id`; across processes same-millisecond jobs interleave roughly by real time. `CreateTables` adds `seq` to a table created before v0.26.0 (existing rows keep 0 and still pop first, since they are older; any probe error other than an unknown column, such as a permission or connection error, fails `CreateTables` instead of triggering the `ALTER`). `queue:retry` gives a retried batch one `seq`, so retried jobs of the same millisecond pop in id order. A table created by your own migration needs the column, or `Push` fails; `queue.Migrations` (or `stores.Migrations`, see [managing them with migrate](database.md#managing-them-with-migrate)) ships the right DDL, and the boot-time creation stays as the fallback for apps that do not register it. With `StopWhenEmpty` (`queue:work -once`, `-stop-when-empty`) a reserve error ends `Work` with that error instead of looking like an empty queue. Workers reserve a job atomically (one `UPDATE … RETURNING`; Postgres and MySQL use `FOR UPDATE SKIP LOCKED`), so any number of `queue:work` processes can share them. A job whose worker died is taken over after its timeout plus one minute: delivery is at least once, so make handlers idempotent.
- `q.DispatchTx(ctx, tx, job)` ties the job to the caller's transaction: it exists only if the transaction commits. The database driver inserts it in the transaction; drivers without transactional storage (`sync`, `memory`, `queue.Fake`, Redis) dispatch it from `database.Tx.AfterCommit`, so the same code works in tests and development. On `sync` the job then runs right after the commit and a handler error is logged (the caller already returned). Errors known up front (unknown kind, `ErrInlineDelay`) are returned inside the transaction; with a `*database.DB` the job is dispatched at once.
- `JobEvent` carries `Attempt`, `MaxRetries` and `Final`, so a dashboard can tell a failure that will be retried from one that killed the job: `Final` is true only when the job moved to the failed jobs (permanent, or out of retries), and false for an attempt a shutdown gave back — which `Attempt` and `MaxRetries` alone cannot distinguish from an exhausted one, since the free attempt is counted before it is given back.
- Job observers (`queue.Observer`: dispatched, processed, failed; never the payload): `q.AddObserver` appends, `q.SetObserver` replaces them all; `app.Queue()` adds devtools and the `app.Instrument` hooks. Nil observers, typed nil pointers included, are ignored. A panicking observer is logged (observer type, job kind and id, panic value) and skipped: the dispatch or attempt goes on and the observers after it still run.
- **Progress is not a queue feature** (decided, not an omission). The queue's contract is delivery: `Push`/`Pop`/`Ack`/`Release`/`Fail` plus the retry policy. A long job's percentage, phase and byte counters belong to the job's own domain row, not to the message, for four reasons:
  - The reader is the HTTP layer, usually in another process (`serve` and `queue:work` are separate services), so progress needs shared, queryable storage with its own shape and retention — which is a table, and the app already has one for the work being tracked. The message is deleted on `Ack`, exactly when a UI still wants to show "done".
  - A hook on `queue.Observer` would not fix that: observers run in the worker process, on the caller's goroutine, so a progress value published through one never reaches the web process. It would look like a progress API and silently do nothing in the normal two-service deployment — worse than not having one.
  - Delivery is at least once and an interrupted or taken-over job runs again from the start, so a counter the queue owns would go backwards or lie after a retry. A row the handler owns is reset by the handler, in the same transaction as the work it describes.
  - Carrying progress in the message would need a new `Driver` method, breaking every driver (including third-party ones) for data the queue itself never reads.

  The pattern that works, and the one to copy: the handler writes its own progress row (`UPDATE conversions SET phase = $1, done_bytes = $2 WHERE id = $3`) with `app.DB()`; HTTP reads that row, and streams it with [`sse.Hub`](frontend.md#sse) plus an `sse.Broker` when the producer and the consumer are different processes. Stamp `queue.AttemptFrom(ctx)` into the row so a restarted attempt is visible instead of looking like progress going backwards, and close the row on `queue.ErrPermanent` or on `Attempt.Last()` so it never stays `running` after the job is gone.
- Commands: `queue:work [-queue high,default] [-concurrency 4] [-once] [-stop-when-empty] [-sleep 1s]` (Ctrl+C drains running jobs), `queue:failed [-json]`, `queue:retry <ids…|all>`, `queue:forget <ids…|all>`. They run in their own process, so they need a shared driver (`database`, Redis): `queue:work` on `sync` exits at once with `QUEUE_CONNECTION=sync runs jobs during Dispatch, so queue:work has nothing to process`, and the failed-job commands on `sync` or `memory` (whose failed jobs live in the serving process) return an error wrapping `vukem.ErrNoFailedStore` that names the setting.
- The `database/dbtest` conformance suite covers the driver (exactly-once processing with concurrent workers, `DispatchTx` rollback, failed/retry/forget, takeover of abandoned jobs) on SQLite and Postgres.
- `vukem make:job SendInvoice` writes `app/jobs/send_invoice.go` (job type, `Kind`, and `HandleSendInvoice(app *vukem.App)` returning the handler) and registers `queue.Handle(HandleSendInvoice(app))` in `app/jobs/registry.go`, whose `All(app)` `bootstrap` passes to `vukem.WithJobsFunc`. Handlers reach `app.DB()`, `app.Now()` and `app.Mailer()` without globals; the function runs once, inside the first `app.Queue()` call (a panic in it becomes that call's error). It may get `app.DB()`, `app.Mailer()`, `app.Crypt` and `app.Now()`, but must not dispatch: `app.Queue()`, `app.Events.Dispatch` with queued listeners, `SendLater`/`MailLater` or running scheduled jobs wait for the queue being built and deadlock (handlers may do all of that when they run). Projects whose `All()` takes no app keep getting dependency-free handlers registered with `WithJobs`.
- **Queued mail:** `mailer.SendLater(ctx, mailable)` / `app.MailLater(ctx, mailable)` renders now and sends from a job (retried; the message ID is stable across retries). The payload is the rendered message sealed with `app.Crypt`, so mail bodies and addresses never reach the jobs tables in clear text. Clear-text mail jobs (queued before v0.24.0) fail unless `QUEUE_ALLOW_LEGACY_MAIL=true`, an upgrade window that logs a warning per job: drain the queue before upgrading, or set the flag until the old jobs are gone. The additional data is fixed, so someone with write access to the jobs table can replay a sealed mail (accepted: that access allows worse).
- **Queued listeners:** `events.OnQueued("update_stock", listeners.UpdateStock)` (or `make:listener UpdateStock --event OrderShipped --queued`) enqueues the event as JSON and runs the listener in a worker; `Dispatch` returns only enqueue errors.
- **Outgoing webhooks:** `webhook.Sender` registers a `webhook.deliver` job (`s.Job()`) and `s.Send(ctx, q, webhook.Delivery{Endpoint, Payload})` enqueues it. Each attempt looks the endpoint up by id (URL and secret never enter the jobs tables), signs it with the Standard Webhooks scheme and posts through `httpclient.Safe`: 2xx succeeds, 410 Gone stops (`OnGone`), anything else retries (8 times, exponential from 30s). `webhook.SQLDeliveryLog` records each attempt in its own table; apps with an existing deliveries table implement `webhook.DeliveryLog` (or wrap a function in `webhook.DeliveryLogFunc`) and map `Attempt` onto their columns. See [security.md](security.md#building-blocks).
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
- `WithoutOverlapping()` takes a lock (`schedule:<app>:<task>`) in `App.Cache` (use `CACHE_STORE=database` or Redis when several servers run the schedule); a run is skipped while the previous one holds it.
- `OnOneServer()` takes a per-occurrence lock (`schedule:one:<app>:<task>:<minute>`, never released, expires after 1h) in `App.Cache`: one scheduler runs the occurrence, the others skip it. It fails closed: with a lock store `cache.Shared` does not report as shared (the memory cache, or a store without `cache.Sharer`) `Schedule.Validate` returns `schedule.ErrLocksNotShared`, `app.Schedule()` fails (so `serve`, `schedule:work` and `schedule:run` do not start) and the task refuses to run.
- Lock keys carry `<app>`, the normalized `APP_NAME` (`schedule.Options.Namespace`), so apps sharing one Redis or cache table keep their locks apart: give each app a unique `APP_NAME` (or `CACHE_PREFIX`). Upgrading changes the keys, so a run in progress during that deploy is not seen by the new version.
- `serve` runs the scheduler in process by default (`SCHEDULE_IN_SERVE=true`), so a single-process deploy still runs the hourly `session:prune`/`cache:prune`; it starts only when tasks exist and stops with the app (stops ticking, gives running tasks `SHUTDOWN_TIMEOUT`, then cancels their `ctx`; `schedule.Options.StopTimeout`, default 30s outside the app). Tasks must honor `ctx`: one that ignores it holds shutdown. Set `SCHEDULE_IN_SERVE=false` when a dedicated process runs the schedule instead: `vukem schedule:work` (ticks at the start of every minute, drains running tasks on shutdown within `SHUTDOWN_TIMEOUT`) or one cron entry: `* * * * * cd /app && ./app schedule:run`. `vukem schedule:list` shows each task's next run.
- Several replicas: each process running the scheduler fires every due task (no leader election). Tasks without `WithoutOverlapping()` run once per replica; `WithoutOverlapping()` with a shared lock store only skips a run while another replica still holds the lock, so a short task can still run on several replicas in the same minute (with the in-memory cache, locks are per process). Built-in prune tasks are idempotent; for run-once tasks use `OnOneServer()` with a shared cache, or set `SCHEDULE_IN_SERVE=false` and run a single `schedule:work`.
- Tasks run concurrently; failures and panics are logged and do not stop other tasks. Command tasks run in the same process through `App.Call`.

## Redis

`go get github.com/AlexGalhardo/vukem/drivers/redis` (go-redis v9; works with Redis 7+ and Valkey 8+, both run in CI).

```go
rc, err := redis.Open(ctx, os.Getenv("REDIS_URL")) // redis://localhost:6379/0, rediss:// for TLS

app, err := vukem.New(
	vukem.WithCacheStore(rc.CacheStore("cache:")),   // also backs scheduler and cache locks
	vukem.WithSessionStore(rc.SessionStore("")),     // "session:" keys with TTL; revoke and list per user
	vukem.WithQueueDriver(rc.Queue(redis.QueueOptions{})),
)

limiter := rc.RateLimiter("api:", 60, time.Minute) // ratelimit.Limiter shared by every server
msgs, err := rc.Subscribe(ctx, "orders")          // pub/sub (SSE fan-out); rc.Publish(ctx, "orders", b)
```

- `os.Getenv` runs before `vukem.New` loads `.env` and `vukemtest` sources. `vukem.WithCacheStoreFunc`, `WithSessionStoreFunc` and `WithQueueDriverFunc` take `func(*App) (store, error)` instead, called on the first `app.Cache()` / `app.Sessions()` / `app.Queue()` after configuration loaded, so the URL comes from `app.Source.Get("REDIS_URL")`; the function's error is returned by that call. The last of the eager and lazy option wins.
- A lazy function returns `nil, vukem.ErrUseDefault` (directly or through single `%w` wraps), e.g. when `REDIS_URL` is empty, to keep the store `CACHE_STORE` / `SESSION_DRIVER` / `QUEUE_CONNECTION` chooses (memory cache, cookie sessions, sync queue by default); the app logs the choice, at `WARN` in production. Only that sentinel falls back: any other error (malformed URL, `redis.Open` against a server that is down), also one joined with the sentinel (`errors.Join`, several `%w`), fails the accessor, so a configured but broken Redis is a boot error or, with `OpenLazy`, a failing readiness check, never a silent switch. `nil, nil` and a store returned with `ErrUseDefault` are errors. The fallback store keeps its `cache.Sharer` answer (memory: `OnOneServer` and `RATE_LIMIT_STORE=cache` fail at boot). With `QUEUE_CONNECTION=database`, boot runs the queue function once and creates the queue tables only when it falls back (its error fails the boot).
- Risk: a missing or misspelled `REDIS_URL` in production also falls back, to per-process stores. Return `ErrUseDefault` only when `!app.Config.IsProduction()` and an error otherwise, so production without Redis fails at boot.
- The queue keeps jobs in sorted sets under `{queue}:` (one Cluster slot) and reserves them with one Lua script; abandoned jobs are taken over after their timeout plus one minute, failed jobs support `queue:failed`, `queue:retry` and `queue:forget`.
- `Flush` deletes only the store's prefix (`SCAN` + `DEL`), never the whole database, and refuses an empty prefix.
- `Open` pings the server, so the app does not boot while Redis is down; a malformed URL always fails. `redis.OpenLazy(url)` only parses the URL and connects on first use, so the process starts and `rc.Ping` reports the outage in a readiness check (`app.AddHealthCheck("redis", rc.Ping)`). go-redis retries a failed command 3 times with backoff, so that `Ping` takes seconds while Redis is down; for a fast readiness answer set `?dial_timeout=1s&max_retries=-1` on the URL (or open a separate client with those options just for the check).
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
- The provider reads `OTEL_*` settings from `app.Source`, so `.env` files work as well as the process environment: `OTEL_EXPORTER_OTLP_TRACES_ENDPOINT` (used as is) or `OTEL_EXPORTER_OTLP_ENDPOINT` (`/v1/traces` appended); `OTEL_EXPORTER_OTLP_[TRACES_]HEADERS` (`key=value,...`, percent-decoded; collector API keys), `OTEL_EXPORTER_OTLP_[TRACES_]TIMEOUT` (milliseconds) and `OTEL_EXPORTER_OTLP_[TRACES_]COMPRESSION` (`gzip` or `none`); `OTEL_TRACES_SAMPLER` / `OTEL_TRACES_SAMPLER_ARG` (`always_on`, `always_off`, `traceidratio`, `parentbased_always_on`, `parentbased_always_off`, `parentbased_traceidratio`); `OTEL_RESOURCE_ATTRIBUTES`. A non-http(s) endpoint or a malformed value is a boot error that names the variable, never its value. With no endpoint tracing is off; `OTEL_SDK_DISABLED=true` turns everything off.
- The resource carries `deployment.environment.name` (`APP_ENV`), then `OTEL_RESOURCE_ATTRIBUTES`, and `service.name` from `Config.ServiceName`, else `OTEL_SERVICE_NAME`, else the attributes, else `APP_NAME`, and `service.version` from `Config.ServiceVersion`, else `OTEL_SERVICE_VERSION`, else the attributes. Spans and metrics are batched and flushed by `app.Shutdown`, which `app.Run` calls after the HTTP server and queue worker have drained (within `SHUTDOWN_TIMEOUT`); called with a context without deadline, the flush gives up after 10s.
- `Config.Exporter` replaces OTLP (tests use `tracetest.NewInMemoryExporter`), `Config.Sampler` wins over `OTEL_TRACES_SAMPLER` (default `ParentBased(AlwaysSample)`), and `Config.Global` also sets the OpenTelemetry globals for libraries instrumented through them (off by default: no package-level state); only signals that are on are installed (tracer and propagator with tracing, meter with metrics), so a disabled one never replaces an existing global with a no-op.
- Log correlation: `otel.LogHandler(next)` adds `trace_id` and `span_id` (hex) to records whose context carries a valid span; the provider wraps `app.Log` and the router's `web.Ctx.Log()` logger with it when tracing is on, so request logs and `c.Log().InfoContext(c.Context(), ...)` records link to their trace. Log with the `*Context` methods; records logged without a context are not correlated.

### Database, queue, mail and outbound HTTP

Core exposes neutral hook points and never imports OpenTelemetry: `app.Instrument(vukem.Instrumentation{Queries, Jobs, Mail})` adds a `database.QueryObserver`, a `queue.Observer` and a mail transport wrapper next to devtools, and `httpclient.Options.WrapTransport` wraps a `Safe` client's transport. The wrapper must delegate to the transport it is given, where the SSRF check runs; one returning nil fails every request with `httpclient.ErrNilTransport` instead of falling back to `http.DefaultTransport`. `Instrument` must run before `DB()`, `Queue()` or `Mailer()` is first used, so call it from a provider's `Register`; a late hook fails with `vukem.ErrInstrumentTooLate`. The otel provider treats that as a boot error, not a warning (list `otel.New` first in `WithProviders`): a silently partial trace is harder to notice than a failed boot, and the providers it built are shut down.

The provider attaches an `otel.Observer` (build one with `otel.NewObserver(tp, opts...)` for a custom setup):

| Source | Span | Attributes (never values, addresses or payloads) |
| --- | --- | --- |
| `DB()` statements | client span named after the operation (`SELECT`, `INSERT`...) | `db.system.name` (from the `DATABASE_URL` scheme: `postgresql`, `mysql`, `mariadb`, `sqlite`; `Config.DBSystem` / `otel.WithDBSystem` to set it), `db.operation.name`; `db.query.text` only with `Config.QueryText` / `otel.WithQueryText()` |
| `Queue()` dispatch | producer `send <queue>` | `messaging.system=vukem`, `messaging.destination.name`, `messaging.message.id`, `vukem.job.kind` |
| `Queue()` attempt | consumer `process <queue>` | as above plus `vukem.job.attempt`; failures set Error and `error.type` |
| `Mailer()` sends | client `mail send` | none (no recipient, subject or body) |
| `p.Transport(rt)` / `observer.Transport(rt)` | client `METHOD` | `http.request.method`, `url.scheme`, `server.address`, `server.port`, `http.response.status_code`; 4xx/5xx set Error |

Database and job reports arrive after the work is done, so those spans are back-dated by the reported duration and parented to the caller's context (a query inside a request is a child of the request span). Jobs run by a worker start a new trace: the dispatching request's context is not carried in the job payload. Outbound requests never get the path or query string recorded, and `traceparent`/`baggage` are injected only with `otel.WithClientPropagation()` (trace ids and baggage would otherwise reach third parties).

```go
client := &http.Client{Transport: tracing.Transport(nil)}                     // tracing := otel.New(cfg)
safe := httpclient.Safe(httpclient.Options{WrapTransport: tracing.Transport}) // user-supplied URLs
```

### Metrics

Metrics are on when `Config.MetricReader` is set, or else when `OTEL_EXPORTER_OTLP_METRICS_ENDPOINT` (used as is) or else `OTEL_EXPORTER_OTLP_ENDPOINT` (`/v1/metrics` appended) selects an OTLP/HTTP collector: as in the OpenTelemetry spec, `OTEL_METRICS_EXPORTER` defaults to `otlp` and the generic endpoint applies to every signal (since v0.27; before, metrics also needed `OTEL_METRICS_EXPORTER=otlp`). `OTEL_METRICS_EXPORTER=none` turns metrics off, for a collector that accepts only traces such as Railway's tracing receiver; values other than `otlp` and `none` fail the boot. `OTEL_EXPORTER_OTLP_[METRICS_]HEADERS`, `_TIMEOUT` and `_COMPRESSION` follow the traces rules, and `OTEL_METRIC_EXPORT_INTERVAL` (milliseconds, default 60000) sets the export period. The provider builds a `MeterProvider` with the same resource and records, through the same hooks:

| Metric | Kind | Attributes |
| --- | --- | --- |
| `http.server.request.duration` (s) | histogram | method, `http.route`, status code, `url.scheme`, `error.type` on 5xx and panics |
| `http.client.request.duration` (s) | histogram | method, `server.address`, `server.port`, status code, `error.type` |
| `db.client.operation.duration` (s) | histogram | `db.system.name`, `db.operation.name`, `error.type` |
| `messaging.process.duration` (s) | histogram | `messaging.destination.name`, `vukem.job.kind`, `error.type` |
| `vukem.queue.job.failures` | counter | as above |

Histogram counts are the request, query and job counts. The module ships the OTLP/HTTP metric exporter (`otlpmetrichttp`) used by the endpoint variables above; for another backend, pass `Config.MetricReader` (`sdkmetric.NewPeriodicReader(exporter)` with that backend's exporter), which wins over the variables. `otel.Middleware(tp, otel.WithMeterProvider(mp))` records the server metric without the provider. Without a reader (and without an endpoint) the provider does nothing.

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
- Key rotation: `app.Crypt` opens tokens sealed with any `APP_PREVIOUS_KEYS` entry and seals with `APP_KEY`. Other keyed features take their previous keys from `app.DerivePreviousKeys(purpose, n)`; see [Key rotation](security.md#key-rotation) for the full procedure and `vukem key:rotate`.

## i18n

Package `i18n`, stdlib only. One JSON catalog per locale in `resources/lang/<locale>.json` (`en.json`, `pt-BR.json`; `pt_BR.json` is accepted); any other `.json` name at the root of the folder fails `i18n.Load` with `file name is not a locale`. Keys are nested objects or dotted names; values are strings with `{name}` placeholders, or plural objects.

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
- **Plurals:** an object whose keys are CLDR categories (`zero`, `one`, `two`, `few`, `many`, `other`; `other` required: such an object without `other` fails the load) is a plural message, chosen by the numeric `count` argument. Rules cover en, de, nl, sv, da, nb, fi, et, it, ca, es, el, hu, tr, bg, pt, pt-PT, fr, ru, uk, be, pl, cs, sk, ar, he, and ja, zh, ko, th, vi, id, ms (only `other`); other languages use the English rule. An explicit `zero` form wins for 0 in every language. `i18n.PluralCategory(locale, n)` exposes the rules.
- **Arguments:** key/value pairs (`"name", "Ana"`) or one `map[string]any`; unknown placeholders stay as written. Output is plain text, so `html/template` escapes it.
- **Locale resolution:** `i18n.Middleware(bundle, i18n.MiddlewareConfig{})` returns `(middleware, error)`; the middleware checks, in order, `?lang=`, the `lang` cookie, the session value `locale` (when the session middleware runs first), `Accept-Language` (RFC 9110 q-values, at most 16 entries) and the default. Candidates must be valid tags matching an available catalog (`Bundle.Match`: exact, shorter form, then same language); invalid input is ignored. It stores the bundle and locale in the context (`i18n.Locale`, `i18n.WithLocale`, `i18n.BundleFrom`), sets `Content-Language`, adds `Vary: Cookie` when the cookie or session was consulted and `Vary: Accept-Language` when the header was. Rename or disable (`"-"`) sources with `Query`, `Cookie`, `SessionKey`; `Order` (`i18n.SourceQuery`, `SourceCookie`, `SourceSession`, `SourceHeader`) picks the sources and their order (left out = off) and `Locales` limits the selectable locales (must include the default). `MiddlewareConfig.Validate(bundle)` reports bad entries (`i18n.Middleware` returns them; `i18n.NewMiddleware` is a deprecated `//go:fix inline` alias); `vukem.MiddlewareOptions.I18n` passes the config to the default stack and validates it at boot. Apps persist a choice by setting the cookie or session value themselves.
- **App wiring:** `WithTranslations` loads the bundle at boot (bad JSON or file names fail `New`), gives views the `t` and `locale` functions (`view.Config.I18n`) and makes `DefaultMiddleware` install the middleware after sessions.
- **Validation:** the middleware also translates `web.Parse`/`Bind` messages from `validation.*` keys ([validation.md](validation.md#translated-messages)).
- **Framework messages:** errors from `auth`, `social` and `orm.Unique`/`Exists` carry message keys (`auth.failed`, `auth.unauthenticated`, `auth.unverified`, `auth.token_ability` with `{ability}`, `social.unknown_provider`, `social.state`, `social.denied`, `social.failed`, `validation.unique`, `validation.exists` with `{field}`) and keep their English text as the fallback. `WithTranslations` adds the built-in English and Brazilian Portuguese catalogs (`Bundle.AddBuiltin`) to the catalogs the app already has (pt-BR messages fill `pt-BR` and `pt`, not `pt-PT`; no catalog is created), never overriding a key the app defines for that locale or its language. The middleware installs `Bundle.Translator(locale)` as the request's `errs.Translator`: the error handler shows `errs.NewKey(status, key, fallback, params)` errors through `HTTPError.LocalizedText`, and code with a context calls `errs.Translate(ctx, key, fallback, params)`. Login and reset messages stay generic in every locale (no user enumeration).
- Starters do not ship catalogs yet; add `resources/lang/en.json` and pass `WithTranslations`.

## Stripe (`contrib/stripe`)

Separate module (`github.com/AlexGalhardo/vukem/contrib/stripe`), standard library only (no stripe-go). Use a restricted key (`rk_…`).

- `stripe.New(key, stripe.WithRetries(2))`: every request pins `Stripe-Version: 2026-08-26.dahlia` (`stripe.APIVersion`), every POST carries an `Idempotency-Key` (generated per call, or yours: `CreatePaymentIntent(ctx, params, "order-42")`, or `stripe.IdempotencyKey("order-42-transfer")` on `Call` and the Connect methods; GETs never send one) so retries on 429/5xx/network errors are safe, honouring `Retry-After` and `Stripe-Should-Retry`. Errors are `*stripe.Error{Status, Type, Code, DeclineCode, Message, Param, RequestID}`; test them with `errors.Is(err, stripe.ErrCardDeclined)` (also `ErrInvalid`, `ErrRateLimited`, `ErrAuth`, `ErrNotFound`). The client prints as `rk_…abcd`, never the key (`TestSecurity_A09_StripeKeyRedacted`).
- Resources: `CreateCustomer/GetCustomer/UpdateCustomer`, `CreateCheckoutSession/GetCheckoutSession` (never pass `payment_method_types`; dynamic payment methods come from the Dashboard), `GetSubscription/UpdateSubscription/CancelSubscription`, `CreatePortalSession`, `CreatePaymentIntent/GetPaymentIntent`, `CreateRefund/GetRefund` (`Refund{Status, FailureReason, TransferReversal, …}`; pass `IdempotencyKey`, and `OnAccount` for a direct charge's refund), and `Call(ctx, method, path, params, &out, opts...)` for any other endpoint (the path must start with `/v1/` or `/v2/` and contain no `..`, `%2e`, `?`, `#`, `\` or control characters, or the call fails with `stripe.ErrInvalidRequestOption`; a `/v2/...` path is API v2: writes are JSON, a `nil` value is JSON `null`, and POST and DELETE carry an `Idempotency-Key`; a DELETE without params sends no body) (`Do` is deprecated in favour of `Call`; `go fix` inlines it). Expandable ids (`customer`, `subscription`, `payment_intent`, `latest_charge`, `on_behalf_of`, `transfer_data.destination`, a transfer's `destination`/`destination_payment`/`source_transaction`, `price.product`) stay strings: when you pass `expand[]` the typed structs decode the expanded object's `id`; decode into your own struct or `*json.RawMessage` for the full object. A generated key only protects the client's own retries; pass `IdempotencyKey` tied to the business operation when a retried job must never charge or pay twice, e.g. `c.Call(ctx, http.MethodPost, "/v1/checkout/sessions", params, &cs, stripe.IdempotencyKey("order-42-checkout"))`. `stripe.Params` nests (`line_items[0][price]`).
- Connect, Accounts v2 ([Stripe's current connected-account API](https://docs.stripe.com/connect/accounts-v2); the marketplace showcase saw v1 account creation refused on a new platform): `CreateAccountV2` (POST `/v2/core/accounts`, JSON: `dashboard=express`, `defaults.responsibilities.fees_collector`/`losses_collector=application`, `identity.country`, `configuration.merchant`/`recipient` capabilities, `include`), `GetAccountV2(ctx, "acct_…", []string{"configuration.recipient", "requirements"})` (sent as `include[0]=…`; v2 returns configurations and requirements only when included, so a nil `Configuration.Recipient` means not included), `UpdateAccountV2`, `CreateAccountLinkV2` (`account`, `use_case.type=account_onboarding|account_update` and its `refresh_url`, `return_url` and `configurations`, which the pinned `2026-08-26.dahlia` requires and `2026-09-30.endive` removes). `AccountV2` types the merchant (`card_payments`, `stripe_balance.payouts`) and recipient (`stripe_balance.stripe_transfers`, which must be `active` before transfers) capabilities with their `status_details`, and `Requirements.Entries`/`Summary.MinimumDeadline`; v2 timestamps decode as `time.Time`. Every request (v1 and v2) pins the same `Stripe-Version`, which [v2 requires](https://docs.stripe.com/api-v2-overview). v2 errors keep the `{"error":{…}}` envelope; the client maps the v2 type `rate_limit` to `rate_limit_error` and an empty or unknown type (e.g. code `invalid_fields`) to the HTTP status, so `ErrInvalid`, `ErrAuth`, `ErrRateLimited` and the new `ErrNotFound` (any 404: v1 `resource_missing`, v2 `not_found`) match both APIs. A v2 account id works with the v1 endpoints below.
- Connect, v1 (still supported; Stripe still requires v1 for OAuth, Treasury and card issuing, and discourages keeping both versions indefinitely): `CreateAccount` (pass `controller[stripe_dashboard][type]=express`, `controller[fees][payer]=application`, `controller[losses][payments]=application`, or the legacy `type=express`), `GetAccount` (`ChargesEnabled`, `DetailsSubmitted`, `Requirements`), `CreateAccountLink` (`account`, `type=account_onboarding`, `refresh_url`, `return_url`; single-use, give it only to the signed-in account holder), `CreateLoginLink(ctx, "acct_…")` (Express dashboard), `CreateTransfer/GetTransfer` (separate charges and transfers: `destination`, `transfer_group`, `source_transaction`), `CreateTransferReversal(ctx, "tr_…", params)/GetTransferReversal` (reverse the seller's share before refunding the buyer; pass `IdempotencyKey`). Destination charges are plain params: `application_fee_amount` and `transfer_data[destination]` on a PaymentIntent, or `payment_intent_data[application_fee_amount]` on a Checkout Session; `PaymentIntent` decodes `ApplicationFeeAmount`, `TransferData`, `OnBehalfOf`, `TransferGroup`. Act as a connected account (direct charges, reads) with `c.ForAccount("acct_…")` or the per-request `stripe.OnAccount("acct_…")`, which send `Stripe-Account`. The Connect methods (`CreateAccount`, `GetAccount`, `CreateAccountLink`, `CreateLoginLink`, `CreateTransfer`, `GetTransfer`, the `…AccountV2`/`CreateAccountLinkV2` methods and the transfer reversals) act on the platform account, so through `ForAccount` or with `OnAccount` they fail with `stripe.ErrInvalidRequestOption` instead of acting on the wrong account (`TestSecurity_A01_StripePlatformOnlyRefusesAccountScope`). Account ids must be `acct_` + alphanumerics (likewise `tr_` transfers, `trr_` reversals, `re_`/`pyr_` refunds) and idempotency keys printable ASCII up to 255 bytes; anything else fails with `stripe.ErrInvalidRequestOption` before a request is sent (`TestSecurity_A03_StripeRequestHeadersValidated`).
- Webhooks: `stripe.WebhookHandler(stripe.WebhookConfig{Secret, Store, Bus})` verifies `Stripe-Signature` (HMAC-SHA256 of `t.payload`, any of several `v1` signatures, ±5 min tolerance), claims the event id in a `cache.Store` (memory, database or Redis) so a duplicate delivery is acknowledged without processing, dispatches `stripe.Received{Event}` on the `events.Bus` and only then marks it done; a failing listener releases the claim and answers 500 so Stripe redelivers (`TestSecurity_A08_StripeWebhookSignature`). Decode the object with `stripe.Decode[stripe.CheckoutSession](ev)`. For Connect, add the Connect endpoint's secret to `Secrets` (a platform endpoint and a Connect endpoint sign with different secrets even on the same URL; the payload must verify against one of them) and read the connected account from `Event.Account` (`""` for platform events), e.g. `account.updated` decoded with `stripe.Decode[stripe.Account]`. Mount it outside CSRF protection; fulfil orders from `checkout.session.completed`/`checkout.session.async_payment_succeeded`, never from the success page. `stripe.SignForTest` signs payloads for app tests.
- `vukem install:billing` scaffolds subscriptions on top of this module (plans, Checkout, Billing Portal, webhook sync, `Subscribed`/`OnPlan` middleware): see [billing.md](billing.md).
