# Security

What Vukem does **by default** to protect applications, mapped to the [OWASP Top 10:2025](https://top10.owasp.org/2025/), and what remains the application's responsibility. Reporting vulnerabilities in Vukem itself: [SECURITY.md](../SECURITY.md).

> This document is a design contract. Each default lands in the phase shown (**P** = Prototype, **A** = ALPHA, **B** = BETA) and is not "done" until a test asserts it (see [Verification](#verification)).

## Principles

1. **Secure by default, explicit to loosen.** Unsafe behaviour needs an opt-out with a visible name (`InsecureSkipX`, `AddInsecureBypassPattern`), never a missing opt-in.
2. **Fail closed.** If a check cannot be performed (store down, key missing, policy not registered), the request is denied.
3. **Refuse to boot when misconfigured.** Missing `APP_KEY`, debug in production, wildcard CORS with credentials: boot error, not a warning.
4. **Stdlib crypto only.** `crypto/*` and `golang.org/x/crypto`; no custom algorithms.
5. **Small dependency surface** so supply-chain risk stays auditable.

## OWASP Top 10:2025 mapping

### A01:2025 — Broken Access Control (includes SSRF)

| Default | Phase |
| --- | --- |
| Routes are public unless wrapped in `auth.Required()`; `vukem route:list` shows the middleware of every route so unprotected routes are visible | P / A |
| Policies resolved by Go type; **unregistered policy = deny** (fail closed), not allow | A |
| `DenyAsNotFound()` to avoid leaking resource existence (IDOR enumeration) | A |
| Query builder scopes (`Scope(ownedBy(user))`) and route-level `Authorize` make ownership checks one line | A |
| Mass assignment impossible by construction: models are filled from explicit request structs (`Bind[T]`), never from raw maps | P |
| CORS deny-by-default; `*` with credentials refused at boot | P |
| Static files and uploads served through `os.Root` (no `../` traversal); dotfiles never served | P |
| Signed URLs (HMAC, expiry) for temporary downloads and email verification | A |
| **SSRF:** `httpclient.Safe()` dialer rejects loopback, private, link-local and metadata (`169.254.169.254`) addresses after DNS resolution, limits redirects and re-checks each hop; used by framework features that fetch user-supplied URLs | A |
| Idempotency keys are scoped to the caller, so a reused key never replays another user's response (`TestSecurity_A01_IdempotencyScopedToCaller`); a store outage refuses the request instead of risking a duplicate (`TestSecurity_A04_IdempotencyFailsClosed`) | B |

**App responsibility:** call `Authorize` in every handler that touches owned data; scope queries by tenant/user.

### A02:2025 — Security Misconfiguration

| Default | Phase |
| --- | --- |
| Boot refuses: missing/short `APP_KEY`, `APP_DEBUG=true` with `APP_ENV=production`, CORS wildcard + credentials, cookies without `Secure` in production | P |
| `http.Server` always has `ReadHeaderTimeout`, `ReadTimeout`, `WriteTimeout`, `IdleTimeout`, `MaxHeaderBytes` | P |
| Security headers middleware on by default: `X-Content-Type-Options: nosniff`, `Referrer-Policy: strict-origin-when-cross-origin`, `X-Frame-Options: DENY` + CSP `frame-ancestors 'none'`, `Cross-Origin-Opener-Policy: same-origin`, `Strict-Transport-Security` (HTTPS only, configurable) | P |
| Content-Security-Policy builder with per-request nonces (`{{ cspNonce }}`), starter ships a strict policy; Vite tags get the nonce (`TestSecurity_A02_ViteTagsCarryNonce`) | P / B |
| `boost:mcp` `database_query` accepts one `SELECT`/`WITH`/`EXPLAIN` statement without write keywords and runs it on a read-only connection (SQLite `query_only`, read-only transactions elsewhere) (`TestSecurity_A05_BoostQueryIsReadOnly`) | B |
| The Inertia page object is embedded as JSON with `<`, `>`, `&` and `/` escaped, so prop data cannot close its `<script>` (`TestSecurity_A05_InertiaPageScriptCannotBreakOut`) | B |
| The CSP allows the Vite dev server origin only while its hot file exists outside production; the origin must be a bare `http(s)://host:port` so it cannot inject directives | B |
| Server-sent event fields (`id`, `event`, `data`) cannot carry CR/LF into new fields (`TestSecurity_A05_SSEFieldInjection`) | B |
| Maintenance mode stores only the SHA-256 of the bypass secret; the bypass cookie is an HMAC (key derived from `APP_KEY`) bound to that secret, `HttpOnly`, `SameSite=Lax`, `Secure` in production; an unreadable state file keeps the app down (`TestSecurity_A01_MaintenanceBypassNeedsSecret`) | B |
| JWTs: algorithm pinned per key (no `none`, no HMAC/public-key confusion), `exp` required, weak keys refused (`TestSecurity_A04_JWTAlgorithmConfusion`); refresh tokens stored hashed, single use, reuse revokes the family (`TestSecurity_A07_RefreshConcurrentRotationSingleWinner`) | B |
| The Vite `hot` file is ignored in production (`TestSecurity_A02_ViteHotFileIgnoredInProduction`) and must hold an `http(s)` URL (`TestSecurity_A05_ViteRejectsUnsafeHotURL`) | B |
| Debug error page only in development; production errors never include stack traces, SQL or config (`TestSecurity_A02_DebugPageOffInProduction`); the debug page redacts credentials and escapes error text and source (`TestSecurity_A09_DebugPageRedactsSecrets`) | P / B |
| `vukem about` prints effective security settings (secrets redacted) to review deployments | P |
| No default credentials, no sample admin user, `.env` git-ignored, `.env.example` without secrets | P |
| Destructive commands (`migrate:fresh`, `db:wipe`) require `--force` in production | P |
| `/openapi.json` (`vukem.ServeOpenAPI`) and the API reference UI (`apidocs.Serve`) are not served in production unless `OPENAPI_IN_PRODUCTION=true` (`TestSecurity_A02_OpenAPIOffInProduction`, `TestSecurity_A02_APIDocsOffInProduction`); the vendored Scalar bundle is pinned by SHA-256 and runs with telemetry and remote fonts off | B |

**App responsibility:** terminate TLS (or run behind a proxy that does), set `APP_URL` to the `https` origin, configure trusted proxies.

### A03:2025 — Software Supply Chain Failures

| Default | Phase |
| --- | --- |
| Core depends only on the stdlib, `modernc.org/sqlite` and `golang.org/x/crypto`; heavy drivers are separate modules (opt-in) | P |
| `go.sum` checksum database verification (Go default) — never disabled | P |
| `govulncheck` in CI, pre-push hook and weekly cron; generated apps get the same `task vuln` | P |
| Dependabot for Go modules and GitHub Actions; actions pinned by commit SHA; OpenSSF Scorecard | P / B |
| Releases built by GoReleaser with checksums and **SBOM**; signed artifacts (cosign, keyless) | P / B |
| htmx is vendored with a pinned version and checksum verified by a test; no CDN by default | P |
| `depguard` blocks non-allowlisted imports in core | P |
| Starters pin tool versions with the `go.mod` `tool` directive | P |

**App responsibility:** keep running `govulncheck`, review dependency updates, protect CI secrets.

### A04:2025 — Cryptographic Failures

| Default | Phase |
| --- | --- |
| Passwords: **argon2id** (m=64 MiB, t=1, p=4) with PHC strings; bcrypt supported; automatic rehash on login | A |
| Randomness only from `crypto/rand` (`rand.Text()` for tokens); `math/rand` blocked in security packages by lint | P |
| Session cookies and encrypted cookies: AES-256-GCM, keys derived with HKDF from `APP_KEY` (distinct `info` per purpose) | P |
| API tokens stored as SHA-256 hashes; plain token shown once | A |
| Constant-time comparisons (`subtle.ConstantTimeCompare`, `hmac.Equal`) for tokens, signatures, CSRF tokens | P |
| `app.Crypt` (`crypt` package): AES-256-GCM with random nonces, key derived with HKDF from `APP_KEY`; versioned tokens carry a key id; additional data binds a token to its purpose; tampering, unknown keys and wrong additional data all fail with `crypt.ErrInvalid` (`TestSecurity_A04_TamperingRejected`, `TestSecurity_A04_AssociatedDataMismatchRejected`, `TestSecurity_A04_AppCryptUsesDerivedKey`) | B |
| Key rotation via `APP_PREVIOUS_KEYS` (decrypt-only old keys; a malformed one is a boot error) (`TestSecurity_A04_KeyRotation`, `TestSecurity_A04_AppCryptRotatesWithPreviousKeys`, `TestSecurity_A04_EncryptedColumnKeyRotation`) | B |
| Encrypted cookies (`app.Crypt.SetCookie`/`Cookie`): cookie name authenticated, `HttpOnly`, `SameSite=Lax` and `Secure` over TLS by default (`TestSecurity_A04_EncryptedCookieDefaults`, `TestSecurity_A04_EncryptedCookieCannotMoveBetweenNames`) | B |
| `orm.Encrypted[T]` column type for sensitive fields: table and column authenticated, never stored in plaintext, fails closed without an encrypter (`TestSecurity_A04_EncryptedColumnStoresNoPlaintext`, `TestSecurity_A04_EncryptedColumnFailsClosed`) | B |
| TLS: Go's secure defaults (TLS 1.2+, hybrid post-quantum key exchange in Go 1.26+); no option to disable verification without `InsecureSkipVerify` naming | P |
| `config.Secret` redacts secrets in logs, JSON and `%v` | P |

### A05:2025 — Injection

| Default | Phase |
| --- | --- |
| Query builder always uses bound parameters; identifiers (columns, tables) validated against model metadata, never interpolated from input | P |
| `WhereRaw`/`Raw` require placeholders; lint rule and docs flag string concatenation into raw SQL | P |
| API query conventions (`filter`, `sort`, `include`, `fields`) are **allowlists**; unknown values → 400 | A |
| `html/template` contextual auto-escaping for HTML, attributes, JS and URLs; `template.HTML` usage flagged in review | P |
| JSON responses via `encoding/json` (escapes `<`, `>`, `&`); Inertia page data escapes `/` inside `<script>` | P / B |
| Mail headers built with `net/mail` (no header injection); file names sanitised | A |
| No shell execution in the framework; `os/exec` usage in CLI generators never passes user input through a shell | P |
| Validator rejects invalid UTF-8 and control characters in string fields by default | P |
| Locale input (query, cookie, session, `Accept-Language`) is used only when it is a well-formed tag (`language[-Script][-REGION]`) of an available catalog; anything else falls back, so raw input never reaches headers, lookups or file paths (`TestSecurity_A05_LocaleInputRejected`) | B |

### A06:2025 — Insecure Design

| Default | Phase |
| --- | --- |
| Opinionated, reviewed flows generated by `install:auth` / `install:api` (reset tokens single-use and expiring, email verification signed, re-confirm password for sensitive actions) | A |
| Rate limits on login, password reset, token issuance by default | A |
| Request body size limits (`http.MaxBytesReader`) on every route; multipart limits; JSON depth/size limits | P |
| Transactions with rollback-on-error/panic (`db.Tx`) so multi-step operations are atomic | P |
| Threat model of each subsystem recorded in its doc before BETA; security review gate in [PLAN.md](../PLAN.md) (`B.5.2`) | B |

### A07:2025 — Authentication Failures

| Default | Phase |
| --- | --- |
| Session id regenerated on login and privilege change (anti-fixation); invalidated on logout | P / A |
| Cookies: `HttpOnly`, `Secure`, `SameSite=Lax`, `__Host-` prefix over HTTPS | P |
| Idle and absolute session timeouts | P |
| Credential check hashes a dummy password when the user does not exist (timing-safe, no user enumeration) | A |
| Generic error messages on login and password reset ("If the email exists…") | A |
| Password rules action (min length 12 by default, max 72 bytes for bcrypt) editable in user code | A |
| Login throttling per email + IP; remember-me tokens rotated on use | A |
| API tokens with expiry, abilities and revocation; `last_used_at` tracking | A |

| Social login: PKCE, signed single-use `state`/`nonce` cookie, OIDC ID-token verification, redirect URL from config only | A |

**Post-1.0:** TOTP 2FA, passkeys/WebAuthn.

### A08:2025 — Software or Data Integrity Failures

| Default | Phase |
| --- | --- |
| Signed and encrypted cookies (tampering detected) | P |
| Signed URLs and signed email-verification links (HMAC with expiry) | A |
| Queue payloads are JSON mapped to registered job kinds — **no reflection-based deserialization of arbitrary types** | B |
| CSRF protection with `http.CrossOriginProtection` (Fetch Metadata + Origin) on all unsafe methods; webhook bypasses must be declared | P |
| Webhook signature verification helper (`webhook.VerifyHMAC`) | A |
| Release artifacts with checksums, SBOM and signatures | P / B |
| Migrations tracked with checksums; a modified, already-applied migration is reported by `migrate:status` | P |

### A09:2025 — Security Logging and Alerting Failures

| Default | Phase |
| --- | --- |
| Structured `slog` request logs with request id, user id (when authenticated), route name, status, duration | P |
| Security events logged at `WARN` with a stable `event` attribute: `auth.login_failed`, `auth.locked`, `authz.denied`, `csrf.rejected`, `ratelimit.exceeded`, `token.revoked`, `session.invalid` | P / A |
| Secrets, passwords, tokens and `Authorization`/`Cookie` headers redacted from logs | P |
| Log injection prevented: `slog` attribute encoding (no raw string concatenation) | P |
| `events.Listen[security.Event]` hook to forward security events to alerting (email, Slack, SIEM) | A |
| OpenTelemetry tracing module correlates logs and traces | B |

**App responsibility:** ship logs to durable storage and configure alerts on the security events.

### A10:2025 — Mishandling of Exceptional Conditions

| Default | Phase |
| --- | --- |
| **Central error handler** (single place) maps errors to responses; handlers return `error` instead of writing partial responses | P |
| `Recover` middleware converts panics to 500, logs stack trace server-side, never to the client | P |
| **Fail closed**: authorization errors, missing policies, session store failures, rate limiter store failures and CSRF check errors deny the request | P / A |
| `db.Tx` rolls back on error or panic; no partial writes | P |
| Generic client messages (RFC 9457 problem details) without internals (CWE-209) | P |
| Missing/invalid parameters produce 400/422 with field errors, never zero-value defaults silently (CWE-234) | P |
| Timeouts on server, DB queries (`context`), outbound HTTP; rate limiting and quotas against resource exhaustion | P / A |
| Repeated identical errors aggregated in logs (sampling) to keep alerting useful | B |
| `errcheck`, `nilerr`, `errorlint`, `forcetypeassert` linters enforce handled errors in the framework itself | P |

## Security headers (defaults)

| Header | Value |
| --- | --- |
| `Content-Security-Policy` | starter: `default-src 'self'; script-src 'self' 'nonce-…'; style-src 'self' 'nonce-…'; object-src 'none'; base-uri 'self'; frame-ancestors 'none'; form-action 'self'` |
| `Strict-Transport-Security` | `max-age=63072000; includeSubDomains` (HTTPS only) |
| `X-Content-Type-Options` | `nosniff` |
| `X-Frame-Options` | `DENY` |
| `Referrer-Policy` | `strict-origin-when-cross-origin` |
| `Cross-Origin-Opener-Policy` | `same-origin` |
| `Permissions-Policy` | `camera=(), microphone=(), geolocation=()` |

All configurable in `config/http.go`; API routes skip HTML-only headers.

## Building blocks

| Package | Use |
| --- | --- |
| `httpclient.Safe(opts)` | outbound requests to user-supplied URLs: only public addresses (checked on the resolved IP at connect time, so DNS rebinding and redirects to internal hosts fail), http(s) only, timeouts, redirect and body limits (`TestSecurity_A01_SSRF*`) |
| `app.Crypt` / `crypt.New(key, previous...)` | encryption with key rotation, encrypted cookies, `orm.Encrypted[T]` ([subsystems.md](subsystems.md#encryption)) |
| `signed.New(key)` | signed URLs with expiry, constant-time verification, 403 middleware (`TestSecurity_A01_SignedURLTampering`) |
| `webhook.VerifyHMAC`, `webhook.Middleware` | incoming webhooks signed with HMAC-SHA256 (hex or base64, `sha256=` prefix), body size limit, 401 (`TestSecurity_A08_WebhookMiddleware`) |
| `security.Event` | every `web.SecurityEvent` (login failures, `authz.denied`, CSRF, rate limits, bad signatures) is also dispatched on `app.Events` for alerting: `events.Listen(app.Events, func(ctx, e security.Event) error {...})` |
| depguard `crypto-randomness` | `math/rand` is a lint error in `auth`, `session`, `csrf`, `signed`, `crypt`, `social`, `webhook` and `config` |

## Verification

- Every default above has a test named `TestSecurity_<Category>_<Behaviour>` (e.g. `TestSecurity_A01_StaticRejectsTraversal`), so `go test -run TestSecurity ./...` is the security regression suite.
- Fuzz targets for parsers that see untrusted input: router patterns, `.env`, validator tags, multipart, JSON binding, cookie decoding, `crypt` tokens (`FuzzDecrypt`).
- CI: `gosec`, `govulncheck`, CodeQL, OpenSSF Scorecard.
- BETA gate `B.5.2`: manual security review of CSRF, sessions, auth, uploads, SQL building and headers against this document.

## References

- OWASP Top 10:2025 — <https://top10.owasp.org/2025/>
- OWASP ASVS — <https://owasp.org/www-project-application-security-verification-standard/>
- Go security best practices — <https://go.dev/doc/security/best-practices>
- Filippo Valsorda, CSRF and `CrossOriginProtection` — <https://words.filippo.io/csrf/>
- RFC 9457 Problem Details — <https://www.rfc-editor.org/rfc/rfc9457>
