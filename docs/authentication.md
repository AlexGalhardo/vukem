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
tokens := auth.NewTokens(db)
tokens.Now = app.Now // the app clock (vukem.WithClock) drives token expiry
apiGuard := auth.NewTokenGuard("api", users, tokens)

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

Every guard that authenticates a request also records `auth.Principal(ctx)` (`guard, id, ok`; cleared on logout; combine guard and id length-prefixed or hashed, since either may contain any character), for code that keys data by caller without knowing the guard, such as `api.Idempotency`.

### Session guard (`web`)

- `Attempt(c, creds, auth.Remember(bool))`: provider lookup → constant-time hash check → `Login`. Unknown users still pay for a hash verification against a dummy hash, and both failures return the same 422 error (`TestSecurity_A07_LoginErrorsDoNotRevealUsers`). Failures emit the `auth.login_failed` security event.
- `Login(c, u)` regenerates the session id (fixation) and stores the user id under `auth_<guard>` and the owner `"<guard>:<id>"` (`SessionUserID`) under `web.SessionUserKey` (backfilled on the next request for older sessions), so `session.Manager.RevokeOthers(ctx, owner, keepID)` can end the user's other server-side sessions; `Logout(c)` invalidates the session and deletes the remember-me token.
- A user type implementing `SessionVersioner` (`AuthSessionVersion() int64`) has its version recorded on login; a session whose version no longer matches the user's is signed out on its next request, whatever the session driver. Bump the version to end every session; `RefreshSession(c)` re-records it in the current session when that one must stay, and re-issues its remember-me token. Remember-me tokens are bound to the version they were issued under: a bump also ends remember-me logins. `SessionValues(guard, user)` returns what `Login` stores. A stale session is invalidated whole. Declare `AuthSessionVersion` on the value receiver: `NewSessionGuard` panics when only `*U` implements it. Existing apps add a `users.session_version` column (`BIGINT DEFAULT 0`) with a migration before adopting it (see the Sessions page, "Adopting session versions in an existing app").
- A session whose user no longer exists is forgotten: custom `UserProvider`s must report a missing user from `ByID` as `auth.ErrUserNotFound` (a wrapped `orm.ErrNotFound` or `sql.ErrNoRows` is accepted too). Any other lookup error (database down) is returned as a 503 and `auth.Required` answers with it, keeping the session, so an outage neither logs users out nor sends them to the login page (`TestSecurity_A04_SessionKeptWhenUserLookupFails`).
- Remember-me: cookie `remember_<guard>` = `<id>.<secret>` (HttpOnly, SameSite=Lax, Secure unless `InsecureCookies`), only `SHA-256(secret)` stored in `remember_tokens`, single use and rotated on every use, 30 days by default (`RememberFor`).
- Rehash on login when `hasher.NeedsRehash` (e.g. bcrypt → argon2id) and the provider implements `PasswordUpdater`.

### Token guard (`api`)

- Opaque tokens `<prefix><id>.<secret>` (`crypto/rand.Text()`). `tokens.Prefix = "vk_"` (set by the `install:auth` scaffold; pick one specific to your app) starts every new token so secret scanners can match leaks: register a custom pattern such as `vk_[A-Z2-7]{26}\.[A-Z2-7]{26}` in GitHub secret scanning or gitleaks. The prefix is up to 32 letters, digits or underscores ending in `_` (`Create` fails otherwise); tokens issued without it or under an earlier prefix stay valid (`TestSecurity_A04_TokenPrefixForSecretScanning`).
- `access_tokens` stores `SHA-256(secret)`, name, abilities (JSON), `last_used_at` (updated at most once a minute), optional expiry and `revoked_at` (`TestSecurity_A04_TokensStoredHashed`).
- Revocation is soft: `Revoke`, `RevokeFor` and `RevokeAll` set `revoked_at` (a second revoke keeps the first time) and keep the row, so `List` shows revoked tokens with `RevokedAt`; `Find` refuses a revoked or expired token only after the same digest comparison a valid one pays, and an unknown id compares against a placeholder digest (`TestSecurity_A07_RevokedTokenRejected`). `Prune(ctx, cutoff)` hard-deletes tokens revoked before `cutoff` or expired before the earlier of `cutoff` and now, so a future cutoff never deletes a live token.
- Upgrading: tables created before v0.26.0 lack `revoked_at`. Until it exists every token is refused with 503 (fail closed), and `Revoke`, `RevokeFor` and `RevokeAll` delete the rows instead (revoking stays possible, also inside a transaction: the column is probed first, since a failed `UPDATE` would abort a PostgreSQL transaction); one error naming `auth.TokensRevokedAtMigration` is logged per `Tokens` (the guard uses the request logger, revokes `Tokens.Log`, default `slog.Default()`). The missing column is recognised by SQLSTATE `42703` (PostgreSQL), `no such column` (SQLite) or error `1054` (MySQL/MariaDB). Add `auth.TokensRevokedAtMigration(id)` to the app's migrations; it adds the column only when missing (`schema.Schema.HasColumn`) and its `Down` is a no-op (`TestTokensRevokedAtMigration`, `TestTokensRevokeOnLegacyTable`; `drivers/postgres` `TestTokensSoftRevoke`, `drivers/mysql` `TestTokensLegacyTable`).
- The token guard answers a missing, unknown, wrong, expired or revoked token with 401, and a failing store (database down, missing column, a user lookup error other than not found) with 503, like the session guard: an outage is never reported as bad credentials (`TestSecurity_A04_TokenGuardStoreFailureIs503`, `TestTokenGuardUserLookupErrors`). `auth.Required` stops at the first guard that fails with something other than 401, so on routes that accept both a session and a token list the session guard first: `auth.Required(webGuard, apiGuard)`. With the token guard first, a request carrying a valid session and any `Bearer` header is answered 503 while the token store fails.
- `tokens.Create(ctx, user.AuthID(), "cli", []string{"posts:read"}, 30*24*time.Hour)` returns the plain token once. A `ttl` of `0` means **no expiry**: the token stays valid until revoked, so pass a ttl unless that is intended (the `install:api` scaffold issues 30-day tokens); `List(ctx, userID)` (no secrets, revoked tokens included), `RevokeFor(ctx, userID, id)` (owner-checked: use it when the id comes from user input; `false` for another user's, unknown or already revoked token), `Revoke(ctx, id)` (unchecked, for the current token), `RevokeAll(ctx, userID)`; `auth.CurrentToken(c, "api")`, `auth.TokenCan(c, "api", "posts:read")`; `"*"` grants every ability.
- Hashes are compared with `subtle.ConstantTimeCompare`; rejected tokens emit `token.rejected`.

### JWT guard (`auth/jwt`)

Standard library only. `jwt.Sign(key, jwt.Claims{Subject, Issuer, Audience, ExpiresAt, NotBefore, IssuedAt, ID, Extra})` with `jwt.HS256(kid, secret)` (≥ 32 bytes), `jwt.RS256(kid, rsaKey)` (≥ 2048 bits), `jwt.ES256(kid, p256Key)` or `jwt.EdDSA(kid, ed25519Key)`.

- `jwt.NewVerifier(keys, jwt.VerifyOptions{Issuer, Audience, Algorithms, Leeway, AllowMissingExpiry})`: the header `alg` must be allowed **and** equal the algorithm of the key picked by `kid`, so HMAC-with-a-public-key confusion and `none` are impossible (`TestSecurity_A04_JWTAlgorithmConfusion`); `exp` is required by default, `nbf`/`iat` checked with 30s leeway, `iss`/`aud` exact; duplicate JSON members and tokens over 8 KiB are rejected. Every failure wraps `jwt.ErrInvalid`.
- Key sets: `jwt.StaticKeys(current, previous...)` for rotation; `&jwt.RemoteJWKS{URL: issuer + "/.well-known/jwks.json"}` caches an identity provider's keys, refetches on an unknown `kid` at most once a minute and refreshes hourly. A verifier over a `RemoteJWKS` needs `Audience` and `Issuer`: the provider signs tokens for all of its clients with the same keys, so without them a token issued to another app would pass. Missing either, `verifier.Err()` says which, `jwt.NewGuard` panics at boot, and every `Verify` fails with a bare `jwt.ErrInvalid` (the detail never reaches clients). Custom key sets that wrap a `RemoteJWKS` implement `jwt.SharedKeySet` (`SharedKeys() bool`; embedding `*jwt.RemoteJWKS` forwards it) to get the same check. `InsecureSkipAudience` / `InsecureSkipIssuer` opt out (dangerous) (`TestSecurity_A07_RemoteJWKSRequiresAudienceAndIssuer`); `jwt.PublicJWKS(keys...)` builds your own JWKS document (HMAC keys are never published).
- `jwt.NewGuard[User]("api", verifier, provider)` implements `auth.Checker` (`auth.Required(guard)`); `jwt.ClaimsFrom(c, "api")` exposes the claims; rejected tokens emit `jwt.rejected`.
- Refresh tokens: `jwt.NewRefreshTokens(db)` with `jwt.RefreshTokensMigration(id)` stores only `SHA-256` of opaque tokens; `Issue(ctx, userID)` starts a family, `Rotate(ctx, token)` consumes it and returns the user and a new token, atomically (one winner among concurrent rotations, `TestSecurity_A07_RefreshConcurrentRotationSingleWinner`); presenting a consumed token returns `jwt.ErrRefreshReused` and revokes the whole family. `Revoke(ctx, token)` (logout) and `RevokeUser(ctx, id)` (password change).

### Middleware

| Middleware | Effect |
| --- | --- |
| `auth.Required(guards...)` | first guard that authenticates wins; JSON clients get 401 with `WWW-Authenticate: Bearer`, browsers a redirect to the route named `login` (intended GET URL kept in the session under `auth.IntendedKey`). When every guard is an `auth.APIGuard` (token and JWT guards), nobody is redirected: the 401 is always problem+json, whatever `Accept` or the session cookie says. A guard failing with 503 (store down) stops the list: put the session guard before the token guard |
| `auth.Guest(guards...)` | sends logged-in users to the route named `home` |
| `auth.Verified(guard)` | 403 unless the user implements `EmailVerified() bool` and returns true |
| `auth.Ability(guard, abilities...)` | 403 unless the token grants every ability (`authz.denied` event); problem+json for an `auth.APIGuard` |

### Testing

`app.Client(t).ActingAs("web", user)` starts a logged-in session (with the user's session version), `.WithSession(values)` adds values such as an app's own login stamp; `.WithToken(plain)` sends a bearer token.

## Hashing

| Driver | Default params | Notes |
| --- | --- | --- |
| **argon2id** (default) | m=64 MiB, t=1, p=4, 16-byte salt, 32-byte key | PHC string `$argon2id$v=19$m=…,t=…,p=…$salt$hash` |
| bcrypt | cost you choose (12 recommended) | inputs over 72 bytes are rejected (`ErrPasswordTooLong`), never truncated |

`hash.Default()`, `hash.Argon2id(params)` (start from `hash.DefaultArgon2Params()`), `hash.Bcrypt(cost)`, `hash.Fake()` (tests). `Verify` reads both formats; `NeedsRehash` is true for other parameters or algorithms. Both come from `golang.org/x/crypto`.

## `install:auth` scaffold

`vukem install:auth` copies **user-owned code** into the project (Fortify-style), registers its migrations and routes, and runs `go mod tidy`:

```text
app/models/user.go                         User (AuthID, AuthPassword, EmailVerified)
app/accounts/{accounts,users,passwords}.go guards, registration, verification, password resets
app/accounts/{messages.go,lang/}          auth messages in en and pt-BR (accounts.T)
app/controllers/account/*.go               login, register, logout, forgot/reset password, verification, dashboard
app/requests/auth.go                       z schemas (password ≥ 12 characters, confirmation)
app/mail/auth.go                           VerifyEmail, ResetPassword and AccountExists mailables
database/migrations/*_create_users_table.go, *_create_auth_tables.go (remember, access and reset tokens)
resources/views/auth/*.html, resources/views/mail/{verify_email,reset_password,account_exists}.html
routes/auth.go                             appended to routes.All()
tests/auth_test.go                         the whole flow, run by go test
tests/auth_messages_test.go                every message translated
```

| Flow | Behaviour |
| --- | --- |
| Register | the same answer for new and existing emails (redirect to `/register/check-email`, no validation error, **no login**): a new email gets the account and a verification mail; an existing one gets a "someone tried to sign up" notice (`AccountExists`, at most one per hour per address through the cache, keyed by a hash of the address) and nothing is created or changed. Both paths hash the password with argon2id and queue one mail (`SendLater`), so the timing does not reveal accounts either. A cache error on that path skips the notice and is logged, never returned, so an outage does not reveal accounts either. Sign-up is limited to 10 per 10 minutes per IP and 5 per hour per address (keyed by a SHA-256 of the address, through `app.RateLimiters`, so `RATE_LIMIT_STORE=cache` shares it); the limit counts requests, not accounts, so it answers 429 alike for new and existing addresses. The per-address limit can keep a specific address from signing up for an hour (anyone can spend its budget), without revealing whether it has an account (`TestSecurity_A07_SignupIsEnumerationSafe`, `TestSecurity_A07_SignupRateLimited`, `TestSecurity_A07_SignupCacheOutageIsNoOracle`, generated `TestRegisterDoesNotRevealAccounts`, `TestRegisterThrottle`, `TestRegisterSurvivesCacheOutage`) |
| Login | `Attempt` with remember-me, intended URL, **5 attempts per minute per email + IP** (`ratelimit`), same error for unknown users. Signed-in pages (dashboard, 2FA settings, billing, admin panel) also need a verified email (`auth.Verified`): an unverified login lands on the verification notice, so an account someone pre-registered with another person's address is useless until that person verifies it (and a password reset then ends the squatter's sessions). `POST /api/v1/tokens` answers 403 to unverified users, and `install:admin` admins need a verified `ADMIN_EMAILS` address |
| Email verification | signed link (`signed` package, HMAC + 1 hour expiry) that works signed out (then leads to `/login`; it proves the mailbox, not the password), goes to the dashboard for its own account and answers 403 to another signed-in user; resend (signed in) limited to 3 per minute. The link carries a SHA-256 of the address (`email_hash`) and is void once the email changes. Verifying ends every other session (session version bump), remember-me token and API token of the account, so whatever someone opened after signing up first with another person's address dies; a verifier signed in as the account keeps their session (`RefreshSession`). Known limit: if the owner clicks a verification link for an account they did not create, the account becomes verified with the squatter's password; the mail says to ignore it, and the owner's own sign-up attempt gets the "sign in or reset your password" notice, whose reset ends the squatter's access |
| Forgot / reset password | same answer for unknown emails; random token stored as SHA-256, 1 hour, single use; reset ends every session on every session driver (`users.session_version` bumped with the password; server-side records deleted), remembered session and API token (`Accounts.RevokeOtherSessions`; call it too if you add a password change form) |
| Logout | session invalidated, remember-me token deleted |

Since sign-up never says "this email is taken", a user who forgot they have an account learns it from the notice mail, which links to the login and password reset pages. The trade-off: a sign-up form that cannot tell users their email is taken, and a mail queue that must work (`QUEUE_CONNECTION=database` in production). Apps that ran `install:auth` before v0.26.0 keep the old flow (409 for a taken email, login before verification) until they port `Register`, `Verify`, `routes/auth.go` and the new `AccountExists` mail, or re-run `install:auth --force` (and `install:admin` / `install:billing` / `install:api` for their verified-email checks). The `examples/` apps pin an older vukem and keep the old flow.

Flash messages, controller errors and mail subjects go through `accounts.T(ctx, key)`, with English and Brazilian Portuguese catalogs in `app/accounts/lang/<locale>/*.json` (keys `auth.*`; `--2fa` and `--social` add `two_factor.json` and `social.json`). `T` uses the request locale that `i18n.Middleware` resolves when the app uses `vukem.WithTranslations`, and English otherwise. A key in the app's own catalog for that locale or its language (`resources/lang/pt-BR.json`, then `pt.json`) wins, so the app can reword a message or add a locale without editing the scaffold. Field messages of the `z` schemas in `app/requests` and the views stay in English. Errors from the framework itself (failed login, unverified email, missing token ability, social sign-in, `orm.Unique`) carry their own keys and are translated by the built-in catalogs ([i18n](subsystems.md#i18n)); the login error stays the same for unknown users and wrong passwords in every locale. `tests/auth_messages_test.go` checks that every key exists in every locale. Mailables built without a `Subject` keep the English one.

The reset link mail is queued (`mailer.SendLater`), so the forgot-password answer does not wait for the mail server and its timing does not tell known emails from unknown ones. That needs a real queue: set `QUEUE_CONNECTION=database` in production (`memory` also answers fast, but a restart loses queued reset mails, so users may need to ask again); with the default `sync` the mail is still sent during the request.

Existing projects created before `routes.All()` get the exact lines to add printed instead. `--force` overwrites existing files.

`vukem install:auth --social github,google` (also on a project that already ran `install:auth`) adds `routes/social.go` (`/auth/{provider}/redirect` and `/callback`), the `social_accounts` model and migration (unique provider + provider id), `accounts.NewSocial` (credentials from `SOCIAL_<NAME>_CLIENT_ID`/`_SECRET`, also appended to `.env.example`) and `FindOrCreateFromSocial`: an existing link signs in; a provider-verified email links to the existing account; an unverified email that matches an account is refused (409) so a provider account cannot take over a password account; when a provider-verified email links to a local account that was never verified, the local password is replaced with a random one and its sessions, remember-me and API tokens end (like a password reset), since they may belong to whoever signed up first with the address; otherwise a new user is created. Generated tests use `vukemtest.FakeSocial(social.User{...})`, which only works in test apps.

## Two-factor authentication (`install:auth --2fa`)

`vukem install:auth --2fa` (also on a project that already ran `install:auth`, like `--social`) adds Fortify-style TOTP two-factor authentication built on `auth/totp` (RFC 6238: SHA-1, 6 digits, 30 s, the authenticator-app defaults), for the HTMX starter and for the Inertia React, Vue and Svelte starters:

```text
app/models/two_factor.go                   TwoFactor: the 2FA columns of users (Table() = "users")
app/accounts/two_factor.go                 enable, confirm, verify, recovery codes, disable, pending login
app/controllers/account/two_factor*.go     settings page and login challenge handlers
app/requests/two_factor.go                 code, challenge and password confirmation schemas
database/migrations/*_add_two_factor_columns.go
resources/views/auth/two-factor*.html      or resources/js/pages/Auth/TwoFactor*.{tsx,vue,svelte}
routes/two_factor.go                       appended to routes.All()
tests/two_factor_test.go                   the whole flow, run by go test
```

It also patches the user-owned `Login` handler (`Validate`, then the challenge, then `Login`), links the settings page from the dashboard and, when `routes/social.go` exists (before or after `--2fa`), makes social sign-in stop at the challenge too. If `Login` was rewritten by hand the command fails before writing anything and prints the change to make, so 2FA is never installed without being enforced.

| Flow | Behaviour |
| --- | --- |
| Storage | `two_factor_secret` (`orm.Encrypted[string]`) and `two_factor_recovery_codes` (`orm.Encrypted[[]string]` of SHA-256 hashes) are encrypted with `APP_KEY`, never plaintext; `two_factor_confirmed_at`; `two_factor_last_step`. `TwoFactor` is its own model, so the secret never reaches `User` or the shared `auth.user` prop |
| Enable (`GET/POST /user/two-factor`) | logged-in users get a new secret: QR code (inline SVG from `totp.QR` of the `otpauth://` URI, issuer `APP_NAME`) and the setup key for manual entry. A confirmed setup is never replaced (409), so enabling again cannot switch 2FA off without the password |
| Confirm (`POST /user/two-factor/confirm`) | 2FA is on only after a valid code; then 8 recovery codes are shown **once** (flashed in the encrypted session, only hashes are stored); every other session of the user ends (`Accounts.RevokeOtherSessions`, then `Web.RefreshSession` keeps the current one) |
| Recovery codes (`POST /user/two-factor/recovery-codes`) | regenerate: the old ones stop working, the new ones are shown once; the page shows how many are left |
| Disable (`POST /user/two-factor/disable`) | needs the current password |
| Login | a correct password for a user with confirmed 2FA does **not** log in: the session id is regenerated, the pending user id and the remember-me choice are stored, and the browser goes to `/two-factor-challenge`. The pending login expires after 5 minutes (`accounts.TwoFactorChallengeTTL`) |
| Challenge (`GET/POST /two-factor-challenge`) | an app code (`totp.Validate` against `two_factor_last_step`, then the matched step is stored) or a recovery code (consumed, the remaining hashes stored) logs in with the remember-me choice and goes to the intended URL |
| Replay | a code whose step is at or before the last accepted one is refused, including the code used to confirm; codes are checked inside a transaction that first writes the row, so concurrent requests with the same code run one after the other and one wins |
| Throttling | challenge, confirm and disable: **5 per minute per user + IP** (`ratelimit`, 429 with `Retry-After`) |
| Events | failed codes and passwords log the `auth.2fa_failed` security event (`user_id`, `stage`: `confirm`, `challenge`, `disable`) |

`totp.QR` (SVG) and `totp.QRMatrix` (modules) encode any text in byte mode at error correction level M, versions 1 to 40: up to 2331 bytes (long `otpauth://` URIs, PIX BR Codes); longer text returns `totp.ErrTooLong`.

The raw SVG is inserted with `template.HTML` (HTMX) or the framework's raw-HTML binding (`dangerouslySetInnerHTML`, `v-html`, `{@html}`): safe only because `totp.QR` writes it itself from server data; never do that with user input. Generated tests compute codes with `totp.Code(secret, time.Now(), totp.Options{})`.

## Social login (`social`)

Ally-style OAuth2/OIDC sign-in, standard library only (no `x/oauth2`).

```go
key, err := app.DeriveKey("social.state", 32) // config.ErrKeyMissing without APP_KEY
if err != nil {
	return err
}

s := social.New(key, // panics on a key shorter than 32 bytes
	social.GitHub(social.Config{ClientID: id, ClientSecret: secret, RedirectURL: "https://app.test/auth/github/callback"}),
	social.Google(googleCfg),                  // OIDC
	social.Microsoft("common", microsoftCfg),  // OIDC (Entra ID)
	social.GitLab(gitlabCfg), social.Discord(discordCfg),
	social.OIDC("okta", "https://example.okta.com", oktaCfg), // any OIDC issuer
	social.LinkedIn(linkedinCfg),              // OIDC
	social.X(xCfg),                            // OAuth 2.0 + PKCE, Basic auth at the token endpoint; no email
	apple,                                     // social.Apple(social.AppleConfig{Config, TeamID, KeyID, PrivateKey: p8})
).WithPreviousKeys(app.DerivePreviousKeys("social.state", 32)...) // APP_KEY rotation
```

Sign in with Apple signs its client secret (an ES256 JWT, cached for an hour) with the `.p8` key, and answers with `response_mode=form_post`: the callback is a cross-site `POST` from `appleid.apple.com`. Register `POST` on the callback route, serve the app over HTTPS (the state cookie becomes `SameSite=None; Secure`, and `Redirect` refuses to run with `InsecureCookies`), and add `https://appleid.apple.com` to `CSRF_TRUSTED_ORIGINS`. Apple sends the user's name only on the first sign-in (read from the posted `user` field). `install:auth --social apple,linkedin,x` wires all three; Apple reads `SOCIAL_APPLE_CLIENT_ID`, `SOCIAL_APPLE_TEAM_ID`, `SOCIAL_APPLE_KEY_ID` and `SOCIAL_APPLE_PRIVATE_KEY_PATH`.

```go
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
| CSRF / replay | random `state`, PKCE verifier and OIDC `nonce` in a 10-minute, HMAC-signed, `HttpOnly`, `SameSite=Lax` cookie bound to the provider; state compared in constant time; the cookie is cleared and the state recorded as used, so a replayed callback fails (`TestSecurity_A07_SocialStateMismatch`, `TestSecurity_A07_SocialReplayAndPKCE`). Used states live in process memory unless `s.States` is set to a cache store (`c, _ := app.Cache(); s.States = c.Store()`, keys `social:state:<state>`); set it when the app runs more than one replica, with `CACHE_STORE=database` or Redis. In production the store must be persistent and shared: the memory cache store is bounded (`CACHE_MAX_ENTRIES`) and evicts entries, and an evicted state could be replayed until its cookie expires. A store error refuses the callback (503) |
| OIDC | discovery document, JWKS cache refreshed once for unknown `kid`, only RS256/ES256 (never `none`/HS*), RSA keys ≥ 2048 bits, checks `iss` (Microsoft `{tenantid}` templates), `aud`/`azp`, `exp`/`iat` with 1 minute leeway, `nonce` (`TestSecurity_A07_IDTokenChecks`) |
| User | `social.User{Provider, ID, Email, EmailVerified, Name, Nickname, AvatarURL, Token{Access, Refresh, Expiry, Scopes}, Raw}` |
| Email trust | `EmailVerified` only when the provider vouches: GitHub primary verified address, GitLab `confirmed_at`, Discord `verified`, OIDC `email_verified` (Microsoft: `xms_edov`). Link accounts by `(Provider, ID)`, by email only when verified |
| Stateless | `s.UserFromToken(ctx, "google", accessToken)` for API/SPA flows (profile/userinfo endpoint) |
| Options | `social.WithEndpoints(...)` (GitHub Enterprise, self-hosted GitLab, tests), `social.WithAuthParams(...)`, `Config.Scopes`, `OIDC(...).AuthParams(...)`, `social.OAuth2(name, cfg, endpoints, scopes, mapUser)` |
| Security | a provider `error` other than `access_denied` answers `social.ErrProvider` (400, fixed message) and logs the sanitized code, so a crafted callback link cannot put text on the page (`TestSecurity_A03_SocialProviderErrorNotReflected`); redirect URL fixed in config, provider calls through a client with a 10 s timeout and 1 MiB response limit, tokens never logged |

Apple (`social.Apple`, form-post callback), LinkedIn (`social.LinkedIn`) and X (`social.X`) are built in. `social.Fake(name, callbackURL, user)` is a provider for tests.

## Authorization

Package `authz`, generic over your user type. Decisions **fail closed**: an ability or policy that was never registered denies (403) and the cause is logged (`TestSecurity_A01_UnregisteredPolicyDenies`).

### Gates

```go
gate := authz.New(func(ctx context.Context) (*models.User, error) {
	return auth.UserFrom[*models.User](ctx, "web") // who is asking
})

gate.Define("edit-settings", func(ctx context.Context, u *models.User) bool { return u.Role == "admin" })
gate.DefineResult("see-billing", func(ctx context.Context, u *models.User) authz.Result { return authz.DenyAsNotFound() })

r.Services().Authorizer = gate // enables c.Authorize and {{can}}

if err := c.Authorize("edit-settings", nil); err != nil {
	return err // 401 without a user, 403 denied, 404 for DenyAsNotFound
}
```

### Policies

```go
// app/policies/post_policy.go — vukem make:policy Post
type PostPolicy struct{}

func (PostPolicy) Update(ctx context.Context, u *models.User, p *models.Post) authz.Result {
	if u.ID == p.UserID {
		return authz.Allow()
	}

	return authz.DenyAsNotFound() // 404 instead of 403
}

func (PostPolicy) Create(ctx context.Context, u *models.User) bool { return u.Verified() } // type-level ability

// app/policies/registry.go (make:policy appends here)
func All() []authz.Policy { return []authz.Policy{authz.For[*models.Post](PostPolicy{})} }

if err := gate.Register(policies.All()...); err != nil { // at boot: signatures are checked here
	return err
}

if err := c.Authorize("update", post); err != nil { return err }
if err := c.Authorize("create", (*models.Post)(nil)); err != nil { return err }
```

- Policies are found by the subject's Go type; ability names map to methods (`"update"` → `Update`). Methods return `authz.Result` (`Allow`, `Deny(msg)`, `DenyAsNotFound`) or `bool`; other methods on the policy are ignored.
- `gate.Before(func(ctx, u, ability) *authz.Result)` decides first (super-admins).
- `gate.Allows(ctx, u, ability, subject)` for code outside requests.
- Denials through `c.Authorize` emit the `authz.denied` security event. Without an `Authorizer` every check denies.
- Templates: `{{if can "update" .Post}}…{{end}}`, `{{if can "edit-settings"}}…{{end}}`.

## Rate limiting

Login and password reset are throttled by default via `ratelimit` (GCRA in memory; `drivers/redis` provides a limiter shared by every server).
