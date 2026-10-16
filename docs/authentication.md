# Authentication & authorization

Guards, user providers, hashing, tokens, the `install:auth` scaffold, gates and policies. Decisions `H4`, `H5` in [brainstorming.md](brainstorming.md). Phase: **ALPHA** (password hashing ships in the prototype).

## Concepts

| Concept | Responsibility |
| --- | --- |
| **Guard** | *How* a request is authenticated (session cookie, bearer token) |
| **UserProvider** | *Where* users come from (database table via ORM, or custom) |
| **Hasher** | Password hashing and verification |
| **Gate** | Named abilities (`edit-settings`) |
| **Policy[T]** | Abilities scoped to a model type (`update` a `Post`) |

## Interfaces

```go
type Authenticatable interface {
	AuthID() string
	AuthPassword() string
}

type UserProvider[U Authenticatable] interface {
	ByID(ctx context.Context, id string) (U, error)
	ByCredentials(ctx context.Context, creds map[string]string) (U, error) // never checks password
}

type Guard[U Authenticatable] interface {
	User(c *web.Ctx) (U, error)       // cached per request
	Check(c *web.Ctx) bool
	Login(c *web.Ctx, u U) error
	Logout(c *web.Ctx) error
}
```

`auth.ORMProvider[models.User](db)` is the default provider (looks up by `email`).

## Guards

### `session` (web)

- `Attempt(c, creds)`: fetch by credentials → verify hash → `session.Regenerate()` → store user id.
- When the user is not found the hasher still runs against a dummy hash (timing-attack resistance).
- Remember-me (opt-in): long-lived random token, SHA-256 stored in `remember_tokens`, rotated on use.
- Password rehash on login when `Hasher.NeedsRehash` is true.

### `access_token` (API)

- Opaque token `<id>.<secret>` where secret is `crypto/rand.Text()`; only `SHA-256(secret)` is stored in `access_tokens(id, user_id, name, hash, abilities, last_used_at, expires_at)`.
- `auth.Tokens.Create(ctx, user, "cli", []string{"posts:read"}, 30*24*time.Hour)` returns the plain token once.
- Compared with `subtle.ConstantTimeCompare`; `TokenCan(c, "posts:read")`.
- Revocation: delete the row. JWT is intentionally not the default (revocable, simpler); a JWT guard can be added later behind the same `Guard` interface.

### Middleware

| Middleware | Effect |
| --- | --- |
| `auth.Required(guards...)` | 401 (API) or redirect to `login` route (web) |
| `auth.Guest()` | redirect authenticated users to `home` |
| `auth.Verified()` | requires `email_verified_at` |
| `auth.Ability("posts:write")` | token ability check |

## Hashing

| Driver | Default params | Notes |
| --- | --- | --- |
| **argon2id** (default) | m=64 MiB, t=1, p=4, 16-byte salt, 32-byte key | PHC string `$argon2id$v=19$m=…,t=…,p=…$salt$hash` |
| bcrypt | cost 12 | 72-byte input limit enforced |

`hash.Make`, `hash.Verify` (constant time), `hash.NeedsRehash`. Both from `golang.org/x/crypto`. In tests, `hash.Fake()` uses minimal parameters for speed.

## `install:auth` scaffold

`lumek install:auth` generates **user-owned code** (Fortify-style), not a hidden package:

```text
app/controllers/auth/{login,register,logout,password_reset,email_verification}.go
app/actions/auth/{create_user,reset_password,password_rules}.go   # edit freely
app/models/user.go
database/migrations/*_create_users_table.go, *_create_password_resets_table.go, *_create_access_tokens_table.go
resources/views/auth/*.html                                       # HTMX forms
routes/auth.go                                                    # login, register, forgot-password…
tests/auth_test.go
```

Flows included: register, login (rate-limited 5/min per email+IP), logout, forgot/reset password (signed, expiring, single-use token), email verification (signed URL), password confirmation for sensitive routes. 2FA (TOTP) and social login are post-1.0.

## Authorization

### Gates

```go
app.Gate.Define("edit-settings", func(ctx context.Context, u *models.User) bool {
	return u.Role == "admin"
})
if err := c.Authorize("edit-settings", nil); err != nil { return err } // 403
```

### Policies

```go
type PostPolicy struct{}

func (PostPolicy) Update(ctx context.Context, u *models.User, p *models.Post) authz.Result {
	if u.ID == p.UserID { return authz.Allow() }
	return authz.DenyAsNotFound() // 404 instead of 403
}

// bootstrap/policies.go (edited by make:policy)
authz.Register[*models.Post](app.Gate, PostPolicy{})

// in a handler
if err := c.Authorize("update", post); err != nil { return err }
```

- Policies are looked up by the subject's Go type (a `map[reflect.Type]` built at boot — no naming-convention discovery).
- Policy methods are resolved once at registration by the ability name (`"update"` → `Update`); unknown abilities fail at boot when referenced through `authz.Ability[*Post]("update")`, or at first call otherwise.
- `Before(ctx, user, ability) *authz.Result` hook for super-admins.
- Templates: `{{ if can "update" .Post }}…{{ end }}`.

## Rate limiting

Login and password reset are throttled by default via `ratelimit` (`x/time/rate`-style token bucket in memory; database/Redis stores in BETA).
