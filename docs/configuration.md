# Configuration

Typed, validated-at-boot configuration loaded from environment variables and `.env` files. Decision `A5` in [brainstorming.md](brainstorming.md).

## Goals

- No stringly lookups (`config("app.name")`). Every module exposes a typed struct.
- Invalid or missing required values abort boot with a list of **all** problems, not the first one.
- Secrets never reach logs.
- Tests inject configuration through a fake `getenv`, never `os.Setenv`.

## `.env` files

Loaded by a small stdlib-only parser in `config/`. Precedence, highest first:

1. Real process environment (`getenv`)
2. `.env.<APP_ENV>.local`
3. `.env.local` (skipped when `APP_ENV=test`)
4. `.env.<APP_ENV>`
5. `.env`

Supported syntax: `KEY=value`, `export KEY=value`, single/double quotes, `\n` escapes inside double quotes, `#` comments, `${OTHER}` interpolation, and the `@file:` prefix (`DB_PASSWORD=@file:/run/secrets/db`) to read a value from a file (Docker/Kubernetes secrets); a process environment variable set to `@file:<path>` is read the same way. Only regular files up to 1 MiB are read, and an unreadable reference fails `config.Load` (boot). Trust boundary: configuration values (process environment and `.env` files) are trusted operator input. `@file:` applies to every variable the app reads, follows symlinks, and resolves a relative path against the process working directory, so whoever can set an environment variable can make the app read any regular file the process can read. Write `@@file:` for a literal value that starts with `@file:` (one leading `@` is removed). The `@` keeps SQLite DSNs such as `file:storage/app.db` untouched.

`.env` and `.env.*.local` are git-ignored; `.env.example` is committed and kept in sync by generators that add variables.

## Typed module config

```go
// config/app.go (user app)
type App struct {
	Name  string         `env:"APP_NAME"  default:"Vukem"`
	Env   config.Env     `env:"APP_ENV"   default:"production"` // development | production | test
	Debug bool           `env:"APP_DEBUG" default:"false"`
	URL   string         `env:"APP_URL"   validate:"required,url"`
	Key   config.Secret  `env:"APP_KEY"`                                // decode with config.ParseKey (32+ bytes); see APP_KEY below
	Port  int            `env:"PORT"      default:"3000"`
}
```

- `config.Load[T](src)` fills a struct from the merged source using `env` and `default` tags and runs the [validator](http.md#validation).
- Supported field types: `string`, `bool`, ints, floats, `time.Duration`, `[]string` (comma-separated), `*url.URL`, `config.Secret`, and any `encoding.TextUnmarshaler`.
- Each framework module ships its own struct (`database.Config`, `session.Config`, `mail.Config`…). Generators that install a module add its struct and `.env.example` keys.

## `Secret`

```go
type Secret struct{ v string }
func (s Secret) Reveal() string          // explicit access
func (s Secret) String() string          // "[redacted]"
func (s Secret) LogValue() slog.Value    // "[redacted]"
func (s Secret) MarshalJSON() ([]byte, error) // "\"[redacted]\""
```

## `APP_KEY`

- Required in production and whenever `APP_ENV` is unset or empty (an unset `APP_ENV` is production). Only with an explicit `APP_ENV=development` or `test` does an app boot without it (development logs one warning), and every key-derived feature fails closed on use: cookie sessions (`SESSION_DRIVER=cookie`, the default, so `DefaultMiddleware` is still a boot error), `app.Crypt` (nil; `Decrypt` and `SetCookie` return `crypt.ErrNoKey`, `Encrypt` panics), `app.Key.Derive` (panics), `app.DeriveKey` (returns `config.ErrKeyMissing`), encrypted ORM columns (`orm.ErrNoEncrypter`) and the maintenance bypass cookie (disabled). An API-only app with token auth and no encryption runs without a key. A malformed key is always a boot error.
- `vukem key:generate` writes a 32-byte random key (`crypto/rand`), base64-encoded, to `.env`. The app binary's `key:generate` prints one instead (never writes files) and runs before the app is built, so Docker/CI images without the global CLI can create a key: `APP_KEY=$(./app key:generate)`.
- Sub-keys for cookies, sessions and encryption are derived with `crypto/hkdf` using distinct `info` labels, so the raw key is never used directly.
- `APP_PREVIOUS_KEYS` (optional) lists former `APP_KEY` values, comma-separated, in the same `base64:` format. They only open or verify; everything new is sealed or signed with `APP_KEY`. They cover `app.Crypt` (encrypted cookies, `orm.Encrypted` columns, queued mail), cookie sessions (re-sealed with the new key on their next request), the maintenance bypass cookie, and, through `app.DerivePreviousKeys(purpose, n)`, signed URLs and social login state (`signed.New(key).WithPreviousKeys(...)`, `social.New(key).WithPreviousKeys(...)`). A malformed entry is a boot error.
- To rotate: `vukem key:rotate` replaces `APP_KEY` in `.env` and moves the old key to the front of `APP_PREVIOUS_KEYS`; in production set both variables in the secret store. Remove the old key once sessions and links it signed have expired, queued mail is drained and encrypted columns are re-saved; see [Key rotation](security.md#key-rotation).

## Secure defaults

| Setting | Default | Notes |
| --- | --- | --- |
| `APP_ENV` | `production` | Must be `development`, `test` or `production` (anything else is a boot error). Unset or empty means production, so set `APP_ENV=development` for local work: the `vukem new` scaffolds write it to `.env` |
| `APP_DEBUG` | `false` | Debug error page only when true **and** env ≠ production |
| Cookies | `HttpOnly`, `Secure` (unless `APP_ENV=development`), `SameSite=Lax` | Session cookie uses `__Host-` prefix over HTTPS |
| CORS | deny all | Explicit origins required |
| Destructive commands | require `--force` in production | `migrate:fresh`, `db:wipe` |
| `DEVTOOLS` | `false` | `/_vukem` dashboard; a boot error in production ([devtools.md](devtools.md)) |
| `DEVTOOLS_INSECURE_ALLOW_REMOTE` | `false` | Serve the dashboard to non-loopback clients |
| `LOG_DEDUP_WINDOW` | `1m` in production, off elsewhere | Repeated identical ERROR records of the default logger become one `repeated` summary per window; `0` disables it ([subsystems.md](subsystems.md#logging--observability)) |
| `LOG_TRUNCATE_IP` | `false` | The request log writes the /24 (IPv4) or /48 (IPv6) network instead of the client address in `peer` and `ip` (data minimisation); `c.IP()` and rate limits keep the full address |
| `NOINDEX` | `false` | Sends `X-Robots-Tag: noindex, nofollow` on every response of the default stacks (staging); pass `app.Config.NoIndex` to `robots.File.NoIndex` for a disallow-all `robots.txt` |
| `TRUSTED_PROXIES` | empty (trust nobody) | Comma-separated CIDRs or IPs whose `X-Forwarded-For` sets `c.IP()` (rate limits, login throttles, logs); applied by `vukem.New` to the router's `web.Services`. An invalid entry is a boot error; when it is empty in production, the first request from a private/CGNAT/loopback peer carrying `X-Forwarded-For` or `X-Real-IP` logs one WARN. Railway: `100.64.0.0/24` (the observed edge peer, confirm in your logs) with `CLIENT_IP_HEADER=X-Real-IP` |
| `CLIENT_IP_HEADER` | empty | A single-IP header set by the trusted proxy (e.g. `X-Real-IP`) that `c.IP()` reads instead of `X-Forwarded-For`, only from `TRUSTED_PROXIES` peers; requires `TRUSTED_PROXIES`, an invalid or list header name is a boot error |
| `RATE_LIMIT_IPV6_PREFIX` | `64` | Prefix length `ratelimit.ByIP` groups IPv6 clients by (1–128; `128` keys each address); IPv4 stays per address; outside 1–128 is a boot error |
| `TRUST_FORWARDED_PROTO` | `false` | Treat `X-Forwarded-Proto: https` as HTTPS when the peer is in `TRUSTED_PROXIES` (HSTS, `web.Ctx.IsHTTPS`, Inertia's `X-Inertia-Location`); a boot error without `TRUSTED_PROXIES` |
| `HSTS_FORCE` | `false` | Send HSTS on every response in production, for a TLS proxy whose range cannot be put in `TRUSTED_PROXIES` (`SecureHeadersConfig.HSTSForce`); validated in every environment (a boot error unless `APP_URL` is `https://`), applied only in production (a boot warning otherwise) |
| `HTTP_MAX_HEADER_BYTES` | `65536` | Cap on the request line plus headers the app server reads (64 KiB; Go's own default is 1 MiB); larger requests get 431; outside 4096–1048576 is a boot error |
| `HTTP_H2C` | `false` | The app server speaks HTTP/2 without TLS (prior knowledge, for gRPC clients) outside production; `true` enables it in production behind a proxy that speaks h2c ([api.md](api.md#grpc)); a separate `grpc.Serve` port has its own opt-in, `GRPC_INSECURE_CLEARTEXT` |
| `TLS_CERT_FILE`, `TLS_KEY_FILE` | — | PEM key pair: the app server serves HTTPS on `PORT` with HTTP/2 (ALPN `h2`) and HTTP/1.1, TLS 1.2 minimum, reloaded when a file changes; set both or neither; exclusive with `HTTP_H2C` ([http.md](http.md#https-and-http2)) |
| `TLS_REDIRECT_ADDR` | — | With TLS on, also listen for plain HTTP on this address (e.g. `:80`) and redirect (308) to `APP_URL`, which must be `https` |

## Every environment variable

Generated from the config structs by `task config:docs`; `task ci` fails when it is out of date.

<!-- configref:begin -->
| Variable | Type | Default | Description | Source |
| --- | --- | --- | --- | --- |
| `API_VERSION` | `string` | `1.0.0` | APIVersion is info.version of the OpenAPI document. | `Config.APIVersion` |
| `APP_DEBUG` | `bool` | — | Debug shows error details and the debug page; it must be false when APP_ENV is production. | `Config.Debug` |
| `APP_ENV` | `config.Env` | `production` | Env is development, test or production; any other value is a boot error, and unset or empty means production. | `Config.Env` |
| `APP_KEY` | `config.Secret` | — | Key is the secret key that signs and encrypts; see the APP_KEY section of docs/configuration.md. | `Config.Key` |
| `APP_LOCALE` | `string` | — | — | `app.go:initFrontend` |
| `APP_NAME` | `string` | `Vukem` | Name is the application name, shown in logs, mail and pages. | `Config.Name` |
| `APP_PREVIOUS_KEYS` | `config.Secret` | — | PreviousKeys are former APP_KEY values (comma-separated, same format) that still open what they sealed or signed after a key rotation: app.Crypt tokens (encrypted cookies and columns, queued mail), cookie sessions, the maintenance bypass and, through DerivePreviousKeys, signed URLs and social login state. | `Config.PreviousKeys` |
| `APP_PROBLEM_BASE_URL` | `string` | — | ProblemBaseURL prefixes the "type" URI of every RFC 9457 problem (BASE/not-found, BASE/validation); empty means "about:blank". | `Config.ProblemBaseURL` |
| `APP_URL` | `string` | `http://localhost:3000` | URL is the public origin of the app, used for absolute URLs. | `Config.URL` |
| `CACHE_MAX_ENTRIES` | `int` | `10000` | MaxEntries bounds the memory store. | `CacheConfig.MaxEntries` |
| `CACHE_PREFIX` | `string` | `vukem:` | Prefix is added in front of every cache key; use a distinct one per app on a shared Redis or table. | `CacheConfig.Prefix` |
| `CACHE_STORE` | `string` | `memory` | Store is "memory" (default) or "database" (table CACHE_TABLE). | `CacheConfig.Store` |
| `CACHE_TABLE` | `string` | `cache` | Table is the table of the database cache store. | `CacheConfig.Table` |
| `CLIENT_IP_HEADER` | `string` | — | ClientIPHeader names a single-IP header set by the trusted proxy (such as X-Real-IP) that c.IP() reads instead of X-Forwarded-For. | `Config.ClientIPHeader` |
| `CSRF_TRUSTED_ORIGINS` | `string` | — | — | `http.go:DefaultMiddlewareWith` |
| `DATABASE_URL` | `string` | `file:storage/database.sqlite` | URL says where the database is; the scheme picks the driver. | `DatabaseConfig.URL` |
| `DB_MAX_IDLE` | `int` | `5` | MaxIdle is the number of idle connections kept ready. | `DatabaseConfig.MaxIdle` |
| `DB_MAX_OPEN` | `int` | `10` | MaxOpen is the maximum number of open connections (on SQLite, the reader pool). | `DatabaseConfig.MaxOpen` |
| `DEVTOOLS` | `bool` | — | Devtools enables the /_vukem dashboard (refused in production). | `Config.Devtools` |
| `DEVTOOLS_INSECURE_ALLOW_REMOTE` | `bool` | — | DevtoolsInsecureAllowRemote serves the dashboard to non-loopback clients (containers, remote VMs). | `Config.DevtoolsInsecureAllowRemote` |
| `HOST` | `string` | `localhost` | Host is the listen address (0.0.0.0 listens on all IPv4 interfaces). | `Config.Host` |
| `HSTS_FORCE` | `bool` | — | HSTSForce sends HSTS on every response in production, for TLS proxies whose range cannot be put in TRUSTED_PROXIES. | `Config.HSTSForce` |
| `HTTP_H2C` | `bool` | — | H2C serves HTTP/2 without TLS (prior knowledge, for gRPC clients) in production too, behind a proxy that speaks h2c. | `Config.H2C` |
| `HTTP_MAX_HEADER_BYTES` | `int` | `65536` | MaxHeaderBytes caps the request line plus headers the HTTP server reads (4096 to 1048576 bytes, Go's own default; outside is a boot error). | `Config.MaxHeaderBytes` |
| `LOG_DEDUP_WINDOW` | `string` | — | LogDedupWindow collapses repeated identical ERROR records of the default logger into one summary per window (a Go duration; "0" disables it). | `Config.LogDedupWindow` |
| `LOG_FORMAT` | `string` | — | LogFormat is text or json; empty means json in production and text elsewhere. | `Config.LogFormat` |
| `LOG_LEVEL` | `string` | `info` | LogLevel is debug, info, warn or error. | `Config.LogLevel` |
| `LOG_TRUNCATE_IP` | `bool` | — | LogTruncateIP makes the request log write the /24 (IPv4) or /48 (IPv6) network instead of the client address (data minimisation). | `Config.LogTruncateIP` |
| `MAILTRAP_API_TOKEN` | `config.Secret` | — | MailtrapAPIToken is required with the mailtrap mailer. | `MailConfig.MailtrapAPIToken` |
| `MAILTRAP_INBOX_ID` | `int64` | — | MailtrapInboxID > 0 sends to that Email Sandbox inbox instead of delivering. | `MailConfig.MailtrapInboxID` |
| `MAIL_FROM` | `string` | `Vukem <hello@example.com>` | From is the default sender when the envelope has no From. | `MailConfig.From` |
| `MAIL_MAILER` | `string` | — | Mailer is "smtp", "resend", "mailtrap", "log" or "array" (tests). | `MailConfig.Mailer` |
| `MAIL_REPLY_TO` | `string` | — | ReplyTo is the Reply-To for envelopes that set none; empty sends none. | `MailConfig.ReplyTo` |
| `NOINDEX` | `bool` | — | NoIndex sends X-Robots-Tag: noindex, nofollow on every response, so search engines never index the site (staging, previews). | `Config.NoIndex` |
| `OPENAPI_IN_PRODUCTION` | `bool` | — | OpenAPIInProduction serves /openapi.json in production too. | `Config.OpenAPIInProduction` |
| `OPENAPI_PREFIX` | `string` | — | OpenAPIPrefix keeps only routes under it in the document (e.g. /api). | `Config.OpenAPIPrefix` |
| `OPENAPI_REQUIRED_OUTPUTS` | `bool` | — | OpenAPIRequiredOutputs sets openapi.Info.RequiredOutputs: response schemas list the fields the server always sends as required. | `Config.OpenAPIRequiredOutputs` |
| `OPENAPI_SERVERS` | `[]string` | — | OpenAPIServers lists the document's server URLs, comma-separated (default APP_URL); WithOpenAPI's Servers win over it. | `Config.OpenAPIServers` |
| `OPENAPI_STRIP_PREFIX` | `string` | — | OpenAPIStripPrefix removes this path prefix from the documented paths (openapi.Info.StripPrefix), for a server URL that already ends with it; WithOpenAPI's StripPrefix wins over it. | `Config.OpenAPIStripPrefix` |
| `OPENAPI_TYPED_ONLY` | `bool` | — | OpenAPITypedOnly sets openapi.Info.TypedOnly: a plain route under the prefix fails the document build. | `Config.OpenAPITypedOnly` |
| `PORT` | `int` | `3000` | Port is the listen port, 0 to 65535; 0 picks a free port. | `Config.Port` |
| `QUEUE_ALLOW_LEGACY_MAIL` | `bool` | — | AllowLegacyMail sends clear-text mail jobs queued before v0.24.0 (an upgrade window: drain the queue before upgrading, or set it until the old jobs are gone). | `QueueConfig.AllowLegacyMail` |
| `QUEUE_CONCURRENCY` | `int` | `4` | Concurrency is the number of parallel workers. | `QueueConfig.Concurrency` |
| `QUEUE_CONNECTION` | `string` | `sync` | Connection is "sync" (default: jobs run during Dispatch), "memory" (in process, run by a worker inside serve) or "database". | `QueueConfig.Connection` |
| `QUEUE_FAILED_TABLE` | `string` | `failed_jobs` | — | `QueueConfig.FailedTable` |
| `QUEUE_IN_SERVE` | `bool` | `true` | InServe runs a queue worker inside serve for the database driver and custom drivers (Redis). | `QueueConfig.InServe` |
| `QUEUE_LIMITS` | `string` | — | Limits caps the running jobs of some queues, as "name=n" pairs separated by commas ("ingest=2"), so a slow queue cannot take every worker. | `QueueConfig.Limits` |
| `QUEUE_NAMES` | `string` | `default` | Queues lists the queues workers poll, in priority order. | `QueueConfig.Queues` |
| `QUEUE_TABLE` | `string` | `jobs` | Table and FailedTable are the database driver's tables. | `QueueConfig.Table` |
| `RATE_LIMIT_IPV6_PREFIX` | `int` | `64` | RateLimitIPv6Prefix is the prefix length ratelimit.ByIP groups IPv6 clients by (1-128): one client usually owns a whole /64 and can rotate addresses inside it. | `Config.RateLimitIPv6Prefix` |
| `RATE_LIMIT_STORE` | `string` | `memory` | Store is "memory" (default: GCRA per process) or "cache" (fixed windows counted in App.Cache, which must be shared by every server: CACHE_STORE=database or a Redis store set with WithCacheStore). | `RateLimitConfig.Store` |
| `RESEND_API_KEY` | `config.Secret` | — | ResendAPIKey is required with the resend mailer. | `MailConfig.ResendAPIKey` |
| `SCHEDULE_IN_SERVE` | `bool` | `true` | InServe runs the scheduler inside serve. | `ScheduleConfig.InServe` |
| `SCHEDULE_TIMEZONE` | `string` | `UTC` | Timezone of the expressions (IANA name, default UTC). | `ScheduleConfig.Timezone` |
| `SESSION_COOKIE` | `string` | `vukem_session` | CookieName is the session cookie name; __Host- is added when the cookie is Secure. | `SessionConfig.CookieName` |
| `SESSION_DRIVER` | `string` | `cookie` | Driver is "cookie" (encrypted cookie, default) or "memory"; the database driver is installed by the database provider. | `SessionConfig.Driver` |
| `SESSION_IDLE_TIMEOUT` | `time.Duration` | `2h` | IdleTimeout ends a session after this much inactivity. | `SessionConfig.IdleTimeout` |
| `SESSION_LIFETIME` | `time.Duration` | `24h` | Lifetime is the absolute lifetime of a session. | `SessionConfig.Lifetime` |
| `SESSION_RECORD_CLIENT` | `bool` | `false` | RecordClient keeps the user agent and client network with each logged-in server-side session (session.Config.RecordClient). | `SessionConfig.RecordClient` |
| `SESSION_SECURE` | `string` | `auto` | Secure defaults to true in production or when APP_URL is https. | `SessionConfig.Secure` |
| `SHUTDOWN_TIMEOUT` | `time.Duration` | `15s` | ShutdownTimeout is how long a graceful shutdown may take. | `Config.ShutdownTimeout` |
| `SMTP_HOST` | `string` | `localhost` | SMTPHost is the SMTP server host. | `MailConfig.SMTPHost` |
| `SMTP_INSECURE` | `bool` | — | SMTPInsecure allows servers without STARTTLS (local catchers only). | `MailConfig.SMTPInsecure` |
| `SMTP_PASSWORD` | `config.Secret` | — | SMTPPassword is the SMTP password. | `MailConfig.SMTPPassword` |
| `SMTP_PORT` | `int` | `587` | SMTPPort is 587 (STARTTLS) or 465 (implicit TLS). | `MailConfig.SMTPPort` |
| `SMTP_USERNAME` | `string` | — | SMTPUsername is the login; empty skips authentication. | `MailConfig.SMTPUsername` |
| `TELEGRAM_API_URL` | `string` | `https://api.telegram.org` | APIURL is the Bot API base URL, for a self-hosted Bot API server or a fake in tests. | `TelegramConfig.APIURL` |
| `TELEGRAM_BOT_TOKEN` | `config.Secret` | — | BotToken is the bot token from BotFather; it is required. | `TelegramConfig.BotToken` |
| `TELEGRAM_CHAT_ID` | `string` | — | ChatID is the default chat; it is required. | `TelegramConfig.ChatID` |
| `TELEGRAM_LOG` | `bool` | `true` | Log forwards log records at MinLevel and above to the chat (default true); false keeps app.Telegram for explicit Send and Contact only. | `TelegramConfig.Log` |
| `TELEGRAM_MIN_LEVEL` | `string` | `error` | MinLevel is debug, info, warn or error (default error). | `TelegramConfig.MinLevel` |
| `TELEGRAM_RATE_LIMIT` | `int` | — | RateLimit caps forwarded log records per minute (0: no cap). | `TelegramConfig.RateLimit` |
| `TELEGRAM_THREAD_ID` | `int` | — | ThreadID is the forum topic id inside a group (message_thread_id). | `TelegramConfig.ThreadID` |
| `TLS_CERT_FILE` | `string` | — | TLSCertFile and TLSKeyFile (PEM) serve HTTPS on PORT with HTTP/2 (ALPN h2) and HTTP/1.1, TLS 1.2 or later; the pair is reloaded when either file changes. | `Config.TLSCertFile` |
| `TLS_KEY_FILE` | `string` | — | TLSKeyFile is the PEM private key that pairs with TLSCertFile. | `Config.TLSKeyFile` |
| `TLS_REDIRECT_ADDR` | `string` | — | TLSRedirectAddr (e.g. ":80") also listens for plain HTTP there and redirects every request (308) to APP_URL, which must be https. | `Config.TLSRedirectAddr` |
| `TRUSTED_PROXIES` | `string` | — | TrustedProxies lists the proxies (comma-separated CIDRs or IPs) whose X-Forwarded-For is honoured by c.IP(). | `Config.TrustedProxies` |
| `TRUST_FORWARDED_PROTO` | `bool` | — | TrustForwardedProto treats X-Forwarded-Proto: https from a trusted proxy as HTTPS (HSTS). | `Config.TrustForwardedProto` |
<!-- configref:end -->

## App options

Every exported `With*` option of the root package, grouped in the source by subsystem. `Option` and the `options` struct live in `app.go`.

| Option | Subsystem | Defined in | Doc |
| --- | --- | --- | --- |
| `WithAppSeeders` | Database | `database.go` | [database.md](database.md) |
| `WithCacheStore` | Cache | `services.go` | [subsystems.md](subsystems.md) |
| `WithCacheStoreFunc` | Cache | `services.go` | [subsystems.md](subsystems.md) |
| `WithClock` | Runtime | `options_runtime.go` | [testing.md](testing.md) |
| `WithDatabaseDrivers` | Database | `database.go` | [database.md](database.md) |
| `WithDevtools` | Devtools | `options_runtime.go` | [devtools.md](devtools.md) |
| `WithEnv` | Configuration | `options_runtime.go` | [configuration.md](configuration.md) |
| `WithEventsFake` | Events | `services.go` | [testing.md](testing.md) |
| `WithFlags` | Feature flags | `services.go` | [subsystems.md](subsystems.md) |
| `WithHealthCheck` | Health checks | `options_runtime.go` | [http.md](http.md) |
| `WithInertia` | Inertia | `options_views.go` | [frontend.md](frontend.md) |
| `WithJobs` | Queue | `background.go` | [subsystems.md](subsystems.md) |
| `WithJobsFunc` | Queue | `background.go` | [subsystems.md](subsystems.md) |
| `WithKind` | Runtime | `options_runtime.go` | [architecture.md](architecture.md) |
| `WithListeners` | Events | `services.go` | [subsystems.md](subsystems.md) |
| `WithLogOutput` | Logging | `options_runtime.go` | [subsystems.md](subsystems.md) |
| `WithLogger` | Logging | `options_runtime.go` | [subsystems.md](subsystems.md) |
| `WithMailTransport` | Mail | `services.go` | [subsystems.md](subsystems.md) |
| `WithMigrations` | Database | `database.go` | [database.md](database.md) |
| `WithOpenAPI` | OpenAPI | `http.go` | [api.md](api.md) |
| `WithProviders` | Providers | `options_runtime.go` | [architecture.md](architecture.md) |
| `WithQueryObservers` | Database | `database.go` | [database.md](database.md) |
| `WithQueueDriver` | Queue | `background.go` | [subsystems.md](subsystems.md) |
| `WithQueueDriverFunc` | Queue | `background.go` | [subsystems.md](subsystems.md) |
| `WithQueueWork` | Queue | `background.go` | [subsystems.md](subsystems.md) |
| `WithRateLimiters` | Rate limiting | `http.go` | [subsystems.md](subsystems.md) |
| `WithReady` | Runtime | `options_runtime.go` | [testing.md](testing.md) |
| `WithRoutes` | Routing | `options_routes.go` | [http.md](http.md) |
| `WithRoutesAfterBoot` | Routing | `options_routes.go` | [architecture.md](architecture.md) |
| `WithRunner` | Runners | `options_runtime.go` | [architecture.md](architecture.md) |
| `WithSchedule` | Scheduler | `background.go` | [subsystems.md](subsystems.md) |
| `WithSeeders` | Database | `database.go` | [database.md](database.md) |
| `WithSessionStore` | Sessions | `services.go` | [subsystems.md](subsystems.md) |
| `WithSessionStoreFunc` | Sessions | `services.go` | [subsystems.md](subsystems.md) |
| `WithSource` | Configuration | `options_runtime.go` | [configuration.md](configuration.md) |
| `WithStderr` | Runtime | `options_runtime.go` | [testing.md](testing.md) |
| `WithStdin` | Runtime | `options_runtime.go` | [testing.md](testing.md) |
| `WithStdout` | Runtime | `options_runtime.go` | [testing.md](testing.md) |
| `WithTelegram` | Logging | `services.go` | [subsystems.md](subsystems.md) |
| `WithTranslations` | i18n | `options_views.go` | [subsystems.md](subsystems.md) |
| `WithViewFuncs` | Views | `options_views.go` | [frontend.md](frontend.md) |
| `WithViews` | Views | `options_views.go` | [frontend.md](frontend.md) |
| `WithVite` | Vite | `options_views.go` | [frontend.md](frontend.md) |
| `WithoutHTTP` | Runtime | `options_runtime.go` | [architecture.md](architecture.md) |
