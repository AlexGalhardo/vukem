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
	Env   config.Env     `env:"APP_ENV"   default:"development"` // development | production | test
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

- Required in production and whenever `APP_ENV` is unset or empty. Only with an explicit `APP_ENV=development` or `test` does an app boot without it (development logs one warning), and every key-derived feature fails closed on use: cookie sessions (`SESSION_DRIVER=cookie`, the default, so `DefaultMiddleware` is still a boot error), `app.Crypt` (nil; `Decrypt` and `SetCookie` return `crypt.ErrNoKey`, `Encrypt` panics), `app.Key.Derive` (panics), `app.DeriveKey` (returns `config.ErrKeyMissing`), encrypted ORM columns (`orm.ErrNoEncrypter`) and the maintenance bypass cookie (disabled). An API-only app with token auth and no encryption runs without a key. A malformed key is always a boot error.
- `vukem key:generate` writes a 32-byte random key (`crypto/rand`), base64-encoded, to `.env`. The app binary's `key:generate` prints one instead (never writes files) and runs before the app is built, so Docker/CI images without the global CLI can create a key: `APP_KEY=$(./app key:generate)`.
- Sub-keys for cookies, sessions and encryption are derived with `crypto/hkdf` using distinct `info` labels, so the raw key is never used directly.
- `APP_PREVIOUS_KEYS` (optional) lists former `APP_KEY` values, comma-separated, in the same `base64:` format. They only open or verify; everything new is sealed or signed with `APP_KEY`. They cover `app.Crypt` (encrypted cookies, `orm.Encrypted` columns, queued mail), cookie sessions (re-sealed with the new key on their next request), the maintenance bypass cookie, and, through `app.DerivePreviousKeys(purpose, n)`, signed URLs and social login state (`signed.New(key).WithPreviousKeys(...)`, `social.New(key).WithPreviousKeys(...)`). A malformed entry is a boot error.
- To rotate: `vukem key:rotate` replaces `APP_KEY` in `.env` and moves the old key to the front of `APP_PREVIOUS_KEYS`; in production set both variables in the secret store. Remove the old key once sessions and links it signed have expired, queued mail is drained and encrypted columns are re-saved; see [Key rotation](security.md#key-rotation).

## Secure defaults

| Setting | Default | Notes |
| --- | --- | --- |
| `APP_ENV` | `development` | Must be `development`, `test` or `production` (anything else is a boot error) |
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
