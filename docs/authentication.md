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
	AuthID() string       // primary key as a string
	AuthPassword() string // stored hash
}

type UserProvider[U Authenticatable] interface {
	ByID(ctx context.Context, id string) (U, error)
	ByCredentials(ctx context.Context, creds map[string]string) (U, error) // never checks the password
}
```

`auth.NewORMProvider[models.User](db)` is the default provider: `ByID` by primary key, `ByCredentials` by every credential except `password` (columns validated by the ORM), and it stores rehashed passwords (`PasswordColumn`, default `password`).

## Wiring

```go
// routes/web.go
users := auth.NewORMProvider[models.User](db)
webGuard := auth.NewSessionGuard("web", users, hash.Default(), auth.NewRememberStore(db))
apiGuard := auth.NewTokenGuard("api", users, auth.NewTokens(db))

r.Post("/login", func(c *web.Ctx) error {
	in, err := web.Parse(c, requests.LoginSchema)
	if err != nil {
		return err
	}

	if _, err := webGuard.Attempt(c, map[string]string{"email": in.Email, "password": in.Password},
		auth.Remember(in.Remember)); err != nil {
		return err // 422 on "email": "These credentials do not match our records."
	}

	return c.Redirect("/dashboard")
})

r.Get("/dashboard", dashboard).Use(auth.Required(webGuard))
r.Group("/api", func(g *router.Group) { g.Use(auth.Required(apiGuard)) /* ... */ })

user, err := auth.User[models.User](c, "web") // in handlers behind Required
```

## Guards

### Session guard (`web`)

- `Attempt(c, creds, auth.Remember(bool))`: provider lookup → constant-time hash check → `Login`. Unknown users still pay for a hash verification against a dummy hash, and both failures return the same 422 error (`TestSecurity_A07_LoginErrorsDoNotRevealUsers`). Failures emit the `auth.login_failed` security event.
- `Login(c, u)` regenerates the session id (fixation) and stores the user id under `auth_<guard>`; `Logout(c)` invalidates the session and deletes the remember-me token.
- Remember-me: cookie `remember_<guard>` = `<id>.<secret>` (HttpOnly, SameSite=Lax, Secure unless `InsecureCookies`), only `SHA-256(secret)` stored in `remember_tokens`, single use and rotated on every use, 30 days by default (`RememberFor`).
- Rehash on login when `hasher.NeedsRehash` (e.g. bcrypt → argon2id) and the provider implements `PasswordUpdater`.

### Token guard (`api`)

- Opaque tokens `<id>.<secret>` (`crypto/rand.Text()`); `access_tokens` stores `SHA-256(secret)`, name, abilities (JSON), `last_used_at` (updated at most once a minute) and optional expiry (`TestSecurity_A04_TokensStoredHashed`).
- `tokens.Create(ctx, user.AuthID(), "cli", []string{"posts:read"}, 30*24*time.Hour)` returns the plain token once; `Revoke(ctx, id)`, `RevokeAll(ctx, userID)`; `auth.CurrentToken(c, "api")`, `auth.TokenCan(c, "api", "posts:read")`; `"*"` grants every ability.
- Hashes are compared with `subtle.ConstantTimeCompare`; rejected tokens emit `token.rejected`. A JWT guard arrives in BETA (B.8.1).

### Middleware

| Middleware | Effect |
| --- | --- |
| `auth.Required(guards...)` | first guard that authenticates wins; JSON clients get 401 with `WWW-Authenticate: Bearer`, browsers a redirect to the route named `login` (intended GET URL kept in the session under `auth.IntendedKey`) |
| `auth.Guest(guards...)` | sends logged-in users to the route named `home` |
| `auth.Verified(guard)` | 403 unless the user implements `EmailVerified() bool` and returns true |
| `auth.Ability(guard, abilities...)` | 403 unless the token grants every ability (`authz.denied` event) |

### Testing

`app.Client(t).ActingAs("web", user)` starts a logged-in session; `.WithToken(plain)` sends a bearer token.

## Hashing

| Driver | Default params | Notes |
| --- | --- | --- |
| **argon2id** (default) | m=64 MiB, t=1, p=4, 16-byte salt, 32-byte key | PHC string `$argon2id$v=19$m=…,t=…,p=…$salt$hash` |
| bcrypt | cost you choose (12 recommended) | inputs over 72 bytes are rejected (`ErrPasswordTooLong`), never truncated |

`hash.Default()`, `hash.Argon2id(params)`, `hash.Bcrypt(cost)`, `hash.Fake()` (tests). `Verify` reads both formats; `NeedsRehash` is true for other parameters or algorithms. Both come from `golang.org/x/crypto`.

## `install:auth` scaffold

`vukem install:auth` generates **user-owned code** (Fortify-style), not a hidden package:

```text
app/controllers/auth/{login,register,logout,password_reset,email_verification}.go
app/actions/auth/{create_user,reset_password,password_rules}.go   # edit freely
app/models/user.go
database/migrations/*_create_users_table.go, *_create_password_resets_table.go, *_create_access_tokens_table.go
resources/views/auth/*.html                                       # HTMX forms
routes/auth.go                                                    # login, register, forgot-password…
tests/auth_test.go
```

Flows included: register, login (rate-limited 5/min per email+IP), logout, forgot/reset password (signed, expiring, single-use token), email verification (signed URL), password confirmation for sensitive routes, and social login with `--social` ([see below](#social-login-social)). 2FA (TOTP) is post-1.0.

## Social login (`social`)

Ally-style OAuth2/OIDC sign-in, standard library only (no `x/oauth2`).

```go
s := social.New(app.Key.Derive("social.state", 32),
	social.GitHub(social.Config{ClientID: id, ClientSecret: secret, RedirectURL: "https://app.test/auth/github/callback"}),
	social.Google(googleCfg),                  // OIDC
	social.Microsoft("common", microsoftCfg),  // OIDC (Entra ID)
	social.GitLab(gitlabCfg), social.Discord(discordCfg),
	social.OIDC("okta", "https://example.okta.com", oktaCfg), // any OIDC issuer
)

r.Get("/auth/{provider}/redirect", func(c *web.Ctx) error {
	return s.Redirect(c, c.Param("provider")) // 404 for unknown providers
})

r.Get("/auth/{provider}/callback", func(c *web.Ctx) error {
	u, err := s.User(c, c.Param("provider"))
	if err != nil {
		return err // 400 bad/expired state, 403 access denied, 502 provider failure
	}

	user, err := actions.FindOrCreateFromSocial(c.Context(), db, u) // user-owned code from install:auth --social
	if err != nil {
		return err
	}

	return webGuard.Login(c, user)
})
```

| Feature | Detail |
| --- | --- |
| Flow | Authorization Code + **PKCE (S256)** for every provider; `htmx` requests get `HX-Redirect` so the browser navigates |
| CSRF / replay | random `state`, PKCE verifier and OIDC `nonce` in a 10-minute, HMAC-signed, `HttpOnly`, `SameSite=Lax` cookie bound to the provider; state compared in constant time; the cookie is cleared and the state recorded as used, so a replayed callback fails (`TestSecurity_A07_SocialStateMismatch`, `TestSecurity_A07_SocialReplayAndPKCE`) |
| OIDC | discovery document, JWKS cache refreshed once for unknown `kid`, only RS256/ES256 (never `none`/HS*), RSA keys ≥ 2048 bits, checks `iss` (Microsoft `{tenantid}` templates), `aud`/`azp`, `exp`/`iat` with 1 minute leeway, `nonce` (`TestSecurity_A07_IDTokenChecks`) |
| User | `social.User{Provider, ID, Email, EmailVerified, Name, Nickname, AvatarURL, Token{Access, Refresh, Expiry, Scopes}, Raw}` |
| Email trust | `EmailVerified` only when the provider vouches: GitHub primary verified address, GitLab `confirmed_at`, Discord `verified`, OIDC `email_verified` (Microsoft: `xms_edov`). Link accounts by `(Provider, ID)`, by email only when verified |
| Stateless | `s.UserFromToken(ctx, "google", accessToken)` for API/SPA flows (profile/userinfo endpoint) |
| Options | `social.WithEndpoints(...)` (GitHub Enterprise, self-hosted GitLab, tests), `social.WithAuthParams(...)`, `Config.Scopes`, `OIDC(...).AuthParams(...)`, `social.OAuth2(name, cfg, endpoints, scopes, mapUser)` |
| Security | redirect URL fixed in config, provider calls through a client with a 10 s timeout and 1 MiB response limit, tokens never logged |

Planned: `vukem install:auth --social github,google` (routes, `social_accounts` migration, linking action) and `vukemtest.FakeSocial` (A.3.10, after mail); Apple, LinkedIn and X providers in BETA (B.8.3).

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
