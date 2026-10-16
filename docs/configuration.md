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

Supported syntax: `KEY=value`, `export KEY=value`, single/double quotes, `\n` escapes inside double quotes, `#` comments, `${OTHER}` interpolation, and the `@file:` prefix (`DB_PASSWORD=@file:/run/secrets/db`) to read a value from a file (Docker/Kubernetes secrets). The `@` keeps SQLite DSNs such as `file:storage/app.db` untouched.

`.env` and `.env.*.local` are git-ignored; `.env.example` is committed and kept in sync by generators that add variables.

## Typed module config

```go
// config/app.go (user app)
type App struct {
	Name  string         `env:"APP_NAME"  default:"Vukem"`
	Env   config.Env     `env:"APP_ENV"   default:"development"` // development | production | test
	Debug bool           `env:"APP_DEBUG" default:"false"`
	URL   string         `env:"APP_URL"   validate:"required,url"`
	Key   config.Secret  `env:"APP_KEY"   validate:"required,min=32"`
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

- Required. `vukem key:generate` writes a 32-byte random key (`crypto/rand`), base64-encoded, to `.env`.
- Sub-keys for cookies, sessions and encryption are derived with `crypto/hkdf` using distinct `info` labels, so the raw key is never used directly.
- `APP_PREVIOUS_KEYS` (optional) lists former `APP_KEY` values, comma-separated, in the same `base64:` format. `app.Crypt` encrypts with `APP_KEY` and still decrypts values sealed with any previous key, so encrypted cookies and `orm.Encrypted` columns survive a rotation. A malformed entry is a boot error.
- To rotate: move the current `APP_KEY` to `APP_PREVIOUS_KEYS`, run `vukem key:generate --force`, re-save rows with encrypted columns (each save re-encrypts with the new key), then drop the old key. Sessions and signed URLs use only the current key, so they are invalidated.

## Secure defaults

| Setting | Default | Notes |
| --- | --- | --- |
| `APP_DEBUG` | `false` | Debug error page only when true **and** env ≠ production |
| Cookies | `HttpOnly`, `Secure` (unless `APP_ENV=development`), `SameSite=Lax` | Session cookie uses `__Host-` prefix over HTTPS |
| CORS | deny all | Explicit origins required |
| Destructive commands | require `--force` in production | `migrate:fresh`, `db:wipe` |
| `DEVTOOLS` | `false` | `/_vukem` dashboard; a boot error in production ([devtools.md](devtools.md)) |
| `DEVTOOLS_INSECURE_ALLOW_REMOTE` | `false` | Serve the dashboard to non-loopback clients |
