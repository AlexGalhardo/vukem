# Security

What Vukem does **by default** to protect applications, mapped to the [OWASP Top 10:2025](https://top10.owasp.org/2025/), and what remains the application's responsibility. Reporting vulnerabilities in Vukem itself: [SECURITY.md](../SECURITY.md).

## Principles

1. **Secure by default, explicit to loosen.** Unsafe behaviour needs an opt-out with a visible name (`InsecureSkipX`, `AddInsecureBypassPattern`), never a missing opt-in.
2. **Fail closed.** If a check cannot be performed (store down, key missing, policy not registered), the request is denied.
3. **Refuse to boot when misconfigured.** Missing `APP_KEY`, debug in production, wildcard CORS with credentials: boot error, not a warning.
4. **Stdlib crypto only.** `crypto/*` and `golang.org/x/crypto`; no custom algorithms.
5. **Small dependency surface** so supply-chain risk stays auditable.

## OWASP Top 10:2025 mapping

### A01:2025 — Broken Access Control (includes SSRF)

| Default | Check |
| --- | --- |
| Routes are public unless wrapped in `auth.Required()`; `vukem route:list` shows the middleware of every route so unprotected routes are visible (`TestRoutesListsMiddlewareNames`, `TestCommands` (route:list)) | [x] |
| Policies resolved by Go type; **unregistered policy = deny** (fail closed), not allow (`TestSecurity_A01_UnregisteredPolicyDenies`) | [x] |
| `DenyAsNotFound()` to avoid leaking resource existence (IDOR enumeration) (`TestGatesAndPolicies`) | [x] |
| Query builder scopes (`Scope(ownedBy(user))`) and route-level `Authorize` make ownership checks one line (`TestCtxAuthorizeAndTemplateCan`, `TestAuthorize`) | [x] |
| Mass assignment impossible by construction: models are filled from explicit request structs (`Bind[T]`), never from raw maps (`TestBindFromTags`, `TestSecurity_A01_AdminMassAssignment`) | [x] |
| CORS deny-by-default; `*` with credentials refused at boot (`TestSecurity_A01_CORS`) | [x] |
| Static files and uploads served through `os.Root` (no `../` traversal); dotfiles and files under dot directories never served; `storage.Handler` responses carry a `sandbox` CSP so uploaded HTML/XML cannot script the app origin (inline PDF viewers may need a download link) (`TestSecurity_A01_StaticRejectsTraversal`, `TestSecurity_A01_StorageTraversal`, `TestSecurity_A01_StorageHandlerHidesDotPaths`) | [x] |
| Signed URLs (HMAC, expiry) for temporary downloads and email verification (`TestSecurity_A01_SignedURLTampering`) | [x] |
| Client IP (`c.IP()`, used by rate limits, login throttles and logs) reads `X-Forwarded-For` only from `TrustedProxies` peers, across every header line, right to left; a non-IP entry falls back to the peer address (`TestSecurity_A01_IPIgnoresSpoofedForwardedFor`) | [x] |
| Open redirects: `web.SafeRedirect` and `c.Back` accept only relative paths and same-host URLs, and check the resulting path before and after `http.Redirect` cleans it, so `//evil.com`, `/\evil.com`, `/./\evil.com` and `https://app//evil.com` fall back (`TestSecurity_A01_SafeRedirectBlocksOpenRedirects`, `FuzzSafeRedirect`) | [x] |
| The devtools dashboard (`/_vukem`) answers only a loopback peer with a loopback `Host` and no proxy headers (which also defeats DNS rebinding); others get a plain 404. `DEVTOOLS_INSECURE_ALLOW_REMOTE=true` lifts the check (`TestSecurity_A01_DevtoolsLoopbackOnly`) | [x] |
| **SSRF:** `httpclient.Safe()` dialer rejects loopback, private, link-local and metadata (`169.254.169.254`) addresses after DNS resolution, including IPv6 forms that embed or translate IPv4 (IPv4-mapped, -compatible and -translated, NAT64, 6to4, Teredo), ignores proxy environment variables, limits redirects and re-checks each hop; it is the client for app features that fetch user-supplied URLs (the framework itself fetches none) (`TestSecurity_A01_SSRFAddresses`, `TestSecurity_A01_SSRFLoopbackAndRedirect`) | [x] |
| GraphQL mutations never run over GET (405 with `Allow: POST`), so links and image tags cannot change state; POST requires `application/json` (`TestSecurity_A01_GraphQLMutationsRequirePOST`) | [x] |
| Idempotency keys are scoped to the caller, so a reused key never replays another user's response (`TestSecurity_A01_IdempotencyScopedToCaller`), and a key with an empty scope (anonymous or cookie session without `Scope`) is refused with 400 (`TestSecurity_A01_IdempotencyRefusesAnonymousScope`); a store outage refuses the request instead of risking a duplicate (`TestSecurity_A04_IdempotencyFailsClosed`) | [x] |
| `drivers/s3` presigned GET/PUT URLs sign the validated key and expire after 1 s to 7 days; anything outside that range is refused (`TestSecurity_A01_S3PresignExpiryBounded`) | [x] |
| `drivers/s3` `PresignPutWith` signs `Content-Type` and `Content-Length`: an upload with another type or size is rejected with 403 (`TestSecurity_A01_S3PresignPutBindsTypeAndSize`) | [x] |
| WebSocket upgrades (`drivers/websocket`) are same-origin only: a browser `Origin` outside the request host and the explicit `AllowedOrigins` allowlist gets 403 before the handler runs; `AllowedOrigins("*")` or a malformed pattern refuses every upgrade; accepting any origin needs `InsecureSkipOriginCheck()` (`TestSecurity_A01_WebSocketOriginChecked`) | [x] |
| Broadcast channels (`broadcast`, SSE and `drivers/websocket` transports) fail closed: private and presence channels need a matching rule and a user, public channels must be declared, a rule error or gate denial (including 404) answers 403, and an SSE request with one refused channel streams nothing; channel names are limited to 128 characters of `A-Z a-z 0-9 _ - = @ . :` (400 otherwise), so lists, topics and frames cannot be forged (`TestSecurity_A01_BroadcastPrivateChannelsAuthorized`, in both modules) | [x] |
| GraphQL subscriptions (`contrib/graphql`) authorize like the rest of the app: `graphql.ServeWS` runs on the same-origin `drivers/websocket` upgrade, `connection_init` goes through the `OnConnect` hook (an error closes with 4403) and no operation runs before `connection_ack` (4401); `FromBroadcast` subscribes with `broadcast.Server.Subscribe` in the operation's context, so a channel denied by its rule, or a client without a user, fails the subscription with `FORBIDDEN` and receives no events (`TestSecurity_A01_GraphQLSubscriptionsAuthorized`) | [x] |
| Admin panel (`admin`): every route requires the guard, the panel ability (`admin`) and the policy ability for the action (`viewAny`, `view`, `create`, `update`, `delete`); a missing ability, policy or authorizer is a 403, and guests are sent to login (`TestSecurity_A01_AdminRequiresAuthAndPolicy`, `TestSecurity_A01_AdminRequiresAbility`); its forms are CSRF-checked with the session-token fallback (`TestSecurity_A01_AdminCSRF`) | [x] |
| Admin forms write only `z` schema keys: the primary key, timestamps, hidden, read-only and unknown fields are never assigned; a schema key for the primary key or timestamps is a boot error (`TestSecurity_A01_AdminMassAssignment`) | [x] |

**App responsibility:** call `Authorize` in every handler that touches owned data; scope queries by tenant/user.

### A02:2025 — Security Misconfiguration

| Default | Check |
| --- | --- |
| Boot refuses: missing/short `APP_KEY`, an `APP_ENV` other than `development`, `test` or `production` (a typo would skip every production check), `APP_DEBUG=true` with `APP_ENV=production`, CORS wildcard + credentials, cookies without `Secure` in production (`TestSecurity_A02_BootRefusesMisconfiguration`, `TestSecurity_A01_CORS`, `TestSecurity_A02_ConfigChecks`) | [x] |
| `http.Server` always has `ReadHeaderTimeout`, `ReadTimeout`, `WriteTimeout`, `IdleTimeout`, `MaxHeaderBytes` (`TestSecurity_A02_ServerTimeouts`) | [x] |
| Security headers middleware on by default: `X-Content-Type-Options: nosniff`, `Referrer-Policy: strict-origin-when-cross-origin`, `X-Frame-Options: DENY` + CSP `frame-ancestors 'none'`, `Cross-Origin-Opener-Policy: same-origin`, `Strict-Transport-Security` (HTTPS only, configurable) (`TestSecurity_A02_SecureHeaders`, `TestPageWorksUnderTheDefaultSecureHeaders`) | [x] |
| Content-Security-Policy builder with per-request nonces (`{{ cspNonce }}`), starter ships a strict policy; Vite tags get the nonce (`TestSecurity_A02_ViteTagsCarryNonce`) | [x] |
| `boost:mcp` `database_query` accepts one `SELECT`/`WITH`/`EXPLAIN` statement without write keywords and runs it on a read-only connection (SQLite `query_only`, read-only transactions elsewhere) (`TestSecurity_A05_BoostQueryIsReadOnly`) | [x] |
| The Inertia page object is embedded as JSON with `<`, `>`, `&` and `/` escaped, so prop data cannot close its `<script>` (`TestSecurity_A05_InertiaPageScriptCannotBreakOut`) | [x] |
| The CSP allows the Vite dev server origin only while its hot file exists outside production; the origin must be a bare `http(s)://host:port` so it cannot inject directives (`TestSecureHeadersDevServer`, `TestSecurity_A05_ViteRejectsUnsafeHotURL`) | [x] |
| Server-sent event fields (`id`, `event`, `data`) cannot carry CR/LF into new fields (`TestSecurity_A05_SSEFieldInjection`) | [x] |
| Maintenance mode stores only the SHA-256 of the bypass secret; the bypass cookie is an HMAC (key derived from `APP_KEY`) bound to that secret, `HttpOnly`, `SameSite=Lax`, `Secure` in production; an unreadable state file keeps the app down (`TestSecurity_A01_MaintenanceBypassNeedsSecret`) | [x] |
| JWTs: algorithm pinned per key (no `none`, no HMAC/public-key confusion), `exp` required, weak keys refused (`TestSecurity_A04_JWTAlgorithmConfusion`); refresh tokens stored hashed, single use, reuse revokes the family (`TestSecurity_A07_RefreshConcurrentRotationSingleWinner`) | [x] |
| Stripe: the API key is never printed (`TestSecurity_A09_StripeKeyRedacted`); webhooks need a valid `Stripe-Signature` within ±5 minutes and each event id is processed once (`TestSecurity_A08_StripeWebhookSignature`) | [x] |
| `install:billing`: `POST /stripe/webhook` runs outside sessions and CSRF but only with a verified `Stripe-Signature`, each event once, and answers 503 without a signing secret; access comes from webhooks (Checkout `payment_status` gated), never the success page; `Subscribed`/`OnPlan` deny on a missing user or a database error; missing Stripe settings stop production at boot; the API base URL can only be overridden in tests (`TestSecurity_A08_BillingWebhookOutsideCSRFButSigned`) | [x] |
| The Vite `hot` file is ignored in production (`TestSecurity_A02_ViteHotFileIgnoredInProduction`) and must hold an `http(s)` URL (`TestSecurity_A05_ViteRejectsUnsafeHotURL`) | [x] |
| Debug error page only in development; production errors never include stack traces, SQL or config (`TestSecurity_A02_DebugPageOffInProduction`); the debug page redacts credentials and escapes error text and source (`TestSecurity_A09_DebugPageRedactsSecrets`) | [x] |
| Admin pages run under a strict CSP: embedded htmx and CSS, no inline scripts, styles or handlers, `Cache-Control: no-store`, and a strict policy when the app sent none (`TestSecurity_A02_AdminStrictHeaders`) | [x] |
| `vukem about` prints effective security settings (secrets redacted) to review deployments (`TestCommands` (about)) | [x] |
| Devtools (`vukem.WithDevtools()`, `DEVTOOLS=true`) is a boot error with `APP_ENV=production` (`TestSecurity_A02_DevtoolsRefusedInProduction`) | [x] |
| No default credentials, no sample admin user, `.env` git-ignored, `.env.example` without secrets (`TestSecurity_A02_StarterShipsNoSecrets`) | [x] |
| Destructive commands (`migrate:fresh`, `db:wipe`) require `--force` in production (`TestSecurity_A02_DestructiveCommandsNeedForce`) | [x] |
| `/openapi.json` (`vukem.ServeOpenAPI`) and the API reference UI (`apidocs.Serve`) are not served in production unless `OPENAPI_IN_PRODUCTION=true` (`TestSecurity_A02_OpenAPIOffInProduction`, `TestSecurity_A02_APIDocsOffInProduction`); the vendored Scalar bundle is pinned by SHA-256 and runs with telemetry and remote fonts off | [x] |
| GraphQL (`contrib/graphql`): in production the explorer page is not registered and `__schema` / `__type` introspection is rejected unless `InsecureExplorerInProduction` / `InsecureIntrospectionInProduction` opt in; the explorer is embedded (no CDN) and runs under its own nonce CSP (`TestSecurity_A02_GraphQLExplorerOffInProduction`) | [x] |
| gRPC server reflection (`grpc.Reflection`, `contrib/grpc`) is not served in production unless `GRPC_INSECURE_REFLECTION_IN_PRODUCTION=true` (`TestSecurity_A02_GRPCReflectionOffInProduction`) | [x] |

**App responsibility:** terminate TLS (or run behind a proxy that does), set `APP_URL` to the `https` origin, configure trusted proxies.

### A03:2025 — Software Supply Chain Failures

| Default | Check |
| --- | --- |
| Core depends only on the stdlib, `modernc.org/sqlite` and `golang.org/x/crypto`; heavy drivers are separate modules (opt-in) (enforced by `.golangci.yml` depguard `core` and the module split in `go.work`) | [x] |
| `go.sum` checksum database verification (Go default) — never disabled (process: Go default, no `GONOSUMDB`/`GOFLAGS` override in the repo or CI) | [x] |
| `govulncheck` v1.9.0 on every push and pull request, in `task ci` (`task vuln`), in the pre-push hook and weekly on every module (core, `drivers/*`, `contrib/*`, `examples/*`); generated apps pin it too (CI: `vuln` job in `ci.yml`; `lefthook.yml` pre-push `govulncheck`; `vuln.yml` weekly cron; `TestNewProjectEndToEnd`) | [x] |
| Dependabot for Go modules and GitHub Actions; actions pinned by commit SHA; OpenSSF Scorecard (process: `.github/dependabot.yml`, SHA-pinned `uses:` in every workflow, `scorecard.yml`) | [x] |
| Releases built by GoReleaser with checksums and **SBOM**; signed artifacts (cosign, keyless) (process: `.goreleaser.yaml` `checksum`, `sboms`, `signs`; `release.yml`) | [x] |
| htmx is vendored with a pinned version and checksum verified by a test; no CDN by default (`TestVendoredHTMX`) | [x] |
| `depguard` blocks driver and contrib modules and denied packages in core; other new dependencies go through the [dependency policy](architecture.md#dependency-policy) review (enforced by `.golangci.yml` depguard `core`) | [x] |
| Starters pin dependency versions in `go.mod` / `go.sum` and pin `govulncheck` v1.9.0 with a `tool` directive (`go tool govulncheck ./...`; the templ starter pins templ the same way) (`TestNewProjectEndToEnd`, `TestNewAPIProjectEndToEnd`, `TestNewTemplProjectEndToEnd`) | [x] |

**App responsibility:** keep running `govulncheck`, review dependency updates, protect CI secrets.

### A04:2025 — Cryptographic Failures

| Default | Check |
| --- | --- |
| Passwords: **argon2id** (m=64 MiB, t=1, p=4) with PHC strings; bcrypt supported; automatic rehash on login (`TestArgon2PHCFormat`, `TestPasswordRehashOnLogin`, `TestBcryptMigrationAndLimits`) | [x] |
| Randomness only from `crypto/rand` (`rand.Text()` for tokens); `math/rand` blocked in security packages by lint (enforced by `.golangci.yml` depguard `crypto-randomness`) | [x] |
| Session cookies and encrypted cookies: AES-256-GCM, keys derived with HKDF from `APP_KEY` (distinct `info` per purpose) (`TestSecurity_A08_TamperedCookieRejected`, `TestKeyDerive`, `TestSecurity_A04_AppCryptUsesDerivedKey`) | [x] |
| API tokens stored as SHA-256 hashes; plain token shown once (`TestSecurity_A04_TokensStoredHashed`); `RevokeFor(ctx, userID, id)` deletes only the owner's token, so a dashboard "revoke" cannot be used for IDOR (`TestSecurity_A01_TokenRevokeScopedToOwner`) | [x] |
| Constant-time comparisons (`subtle.ConstantTimeCompare`, `hmac.Equal`) for tokens, signatures, CSRF tokens (process: [security-review.md](security-review.md) audit of `==` on secrets; `FuzzVerifyHMAC`, `FuzzConstructEvent`) | [x] |
| `app.Crypt` (`crypt` package): AES-256-GCM with random nonces, key derived with HKDF from `APP_KEY`; versioned tokens carry a key id; additional data binds a token to its purpose; tampering, unknown keys and wrong additional data all fail with `crypt.ErrInvalid` (`TestSecurity_A04_TamperingRejected`, `TestSecurity_A04_AssociatedDataMismatchRejected`, `TestSecurity_A04_AppCryptUsesDerivedKey`) | [x] |
| Key rotation via `APP_PREVIOUS_KEYS` (decrypt-only old keys; a malformed one is a boot error) (`TestSecurity_A04_KeyRotation`, `TestSecurity_A04_AppCryptRotatesWithPreviousKeys`, `TestSecurity_A04_EncryptedColumnKeyRotation`) | [x] |
| Encrypted cookies (`app.Crypt.SetCookie`/`Cookie`): cookie name authenticated, `HttpOnly`, `SameSite=Lax` and `Secure` over TLS by default (`TestSecurity_A04_EncryptedCookieDefaults`, `TestSecurity_A04_EncryptedCookieCannotMoveBetweenNames`) | [x] |
| `orm.Encrypted[T]` column type for sensitive fields: table and column authenticated, never stored in plaintext, fails closed without an encrypter (`TestSecurity_A04_EncryptedColumnStoresNoPlaintext`, `TestSecurity_A04_EncryptedColumnFailsClosed`) | [x] |
| TLS: Go's secure defaults (TLS 1.2+, hybrid post-quantum key exchange in Go 1.26+); no option to disable verification without `InsecureSkipVerify` naming (enforced by `.golangci.yml` gosec G402) | [x] |
| `drivers/mysql`: without `tls=` in `DATABASE_URL`, every non-loopback host gets TLS with a verified certificate (`TestSecurity_A04_MySQLRemoteRequiresTLS`) | [x] |
| `config.Secret` redacts secrets in logs, JSON and `%v` (`TestSecurity_A04_SecretRedaction`) | [x] |
| `drivers/s3` refuses a plain `http://` endpoint unless `S3_INSECURE_HTTP=true` (local MinIO only) (`TestSecurity_A04_S3RequiresTLS`) | [x] |

### A05:2025 — Injection

| Default | Check |
| --- | --- |
| Query builder always uses bound parameters; identifiers (columns, tables) validated against model metadata, never interpolated from input (`TestSecurity_A05_QueryBuilderRejectsInjection`, `TestSecurity_A05_SchemaRejectsBadIdentifiers`, `FuzzWhereColumn`) | [x] |
| `WhereRaw` needs one bound argument per placeholder; gosec (G201/G202) flags SQL built by string concatenation (`TestSecurity_A05_QueryBuilderRejectsInjection`; enforced by `.golangci.yml` gosec) | [x] |
| API query conventions (`filter`, `sort`, `include`, `fields`) are **allowlists**; unknown values → 400 (`TestSecurity_A05_QueryConventionsRejectUnknownColumns`) | [x] |
| Admin list `sort`, `filter[...]` and search only reach SQL for allowlisted columns (unknown ones are ignored) and values are bound parameters (`TestSecurity_A05_AdminSortAndSearchAllowlisted`) | [x] |
| `html/template` contextual auto-escaping for HTML, attributes, JS and URLs; `template.HTML` usage flagged in review (`TestSecurity_A05_FragmentsEscapeAttributes`, `TestSecurity_A02_StarterViewsRespectCSP`) | [x] |
| JSON request detection (`middleware.JSONOnly`, `c.IsJSON`) parses the media type: only `application/json` and `application/*+json` count, so a CORS-simple `text/plain; x=+json` body cannot pass as JSON without a preflight (`TestSecurity_A05_JSONOnlyRejectsSimpleContentTypes`) | [x] |
| JSON responses via `encoding/json` (escapes `<`, `>`, `&`); Inertia page data escapes `/` inside `<script>` (`TestSecurity_A05_InertiaPageScriptCannotBreakOut`) | [x] |
| Mail headers built with `net/mail` (no header injection); file names sanitised; attachment content types parsed and re-formatted (`TestSecurity_A05_MailHeaderInjection`) | [x] |
| No shell execution in the framework; `os/exec` usage in CLI generators never passes user input through a shell (enforced by `.golangci.yml` gosec G204) | [x] |
| Validator rejects invalid UTF-8 and control characters in string fields by default (`TestString`) | [x] |
| Devtools mail previews render in an `<iframe sandbox srcdoc>` with the HTML escaped, under a page CSP without any script source, so mail HTML never runs inline (`TestSecurity_A05_DevtoolsMailPreviewIsSandboxed`) | [x] |
| Uploads (`upload`): the type is detected from the content (`http.DetectContentType`), never the client's `Content-Type`, and the file extension must match it; per-file and per-request sizes → 413, file and field counts bounded (nameless parts count too, `TestSecurity_A06_UploadNamelessPartsBounded`); stored names are random, so client file names (`../`, `\`, absolute paths) never reach a storage path; rejected requests leave no files (`TestSecurity_A05_UploadValidation`); `upload.Verify` (direct uploads) only opens or deletes keys `Presign` could have issued, a random name under `Rules.Dir` (`TestSecurity_A01_DirectVerifyOnlyIssuedKeys`) | [x] |
| Image uploads: `upload.Images` caps the declared width × height at 25 megapixels (`Rules.MaxPixels`), read from the JPEG/PNG/GIF/WebP header without decoding pixels, in `Receive`, `Save` and direct-upload `Verify`; a file of those types without a readable header is rejected and deleted (`TestSecurity_A05_UploadImagePixelLimit`) | [x] |
| Thumbnails (`contrib/imaging`): the decoder is chosen from the content, the declared dimensions are checked against `MaxPixels` before decoding, the source size is bounded by `MaxBytes`, and re-encoding strips metadata; failures write nothing (`TestSecurity_A05_ThumbnailRefusesBombsAndJunk`, `TestSecurity_A05_ThumbnailRefusesBombHeaders`, `TestThumbnailSniffsContentNotName`) | [x] |
| Locale input (query, cookie, session, `Accept-Language`) is used only when it is a well-formed tag (`language[-Script][-REGION]`) of an available catalog; anything else falls back, so raw input never reaches headers, lookups or file paths (`TestSecurity_A05_LocaleInputRejected`) | [x] |

### A06:2025 — Insecure Design

| Default | Check |
| --- | --- |
| Opinionated, reviewed flows generated by `install:auth` / `install:api` (reset tokens single-use, also across concurrent resets, and expiring; a reset ends remembered sessions and API tokens; email verification signed; re-confirm password for sensitive actions) (generated `TestPasswordReset`, `TestRegisterAndVerifyEmail`, `TestTwoFactorDisableNeedsPassword`, run by `TestNewProjectEndToEnd` / `TestNewInertiaProjectEndToEnd`) | [x] |
| Rate limits on login, password reset, token issuance by default (`TestSecurity_A07_AuthScaffoldDefaults`, generated `TestLoginLogoutAndThrottle`) | [x] |
| `ratelimit.Middleware` keys by client IP unless told otherwise and denies when its store fails (503); `ByToken` is meant for routes behind `auth.Required` (the `install:api` scaffold limits by IP first), so made-up bearer tokens cannot each get a fresh bucket (`TestSecurity_A06_RateLimitIgnoresUnverifiedTokens`, `TestSecurity_A06_RateLimitMiddlewareFailsClosed`) | [x] |
| Shared rate limits without Redis: `RATE_LIMIT_STORE=cache` counts fixed windows in the app cache (`CACHE_STORE=database` on SQLite, PostgreSQL or MySQL), whose `Increment` is atomic across servers, so N instances share one budget instead of N; a store error denies the request (503); an unknown `RATE_LIMIT_STORE`, or `cache` over the per-process memory cache, is a boot error, never a silent fallback to per-process limits; every window key is hashed, so no client key can forge another's bucket (`TestSecurity_A06_RateLimitWindowStoreFailsClosed`, `TestSecurity_A02_UnknownRateLimitStoreFails`, `TestSecurity_A02_RateLimitCacheNeedsSharedStore`, `TestSecurity_A07_RateLimitKeysDoNotCollide`, `TestRateLimitWindowSharedAcrossServers` in `drivers/postgres`) | [x] |
| Request body size limits (`http.MaxBytesReader`) on every route; multipart limits; form key nesting limited to 16 levels, JSON nesting bounded by `encoding/json` (`TestSecurity_A06_BodyLimit`, `TestSecurity_A06_InputRejectsOversizedForm`, `TestFormToMapDepthLimit`, `TestSecurity_A06_UploadNamelessPartsBounded`) | [x] |
| WebSocket messages are capped at 32 KiB (`websocket.ReadLimit`; larger closes with 1009), every write has a timeout, pings drop peers that stop answering, and app shutdown closes sockets with 1001 instead of waiting on them (`TestSecurity_A06_WebSocketReadLimit`) | [x] |
| Upload scanning fails closed: with `upload.Rules.Scanner` files stay in a dot-named quarantine path until scanned, infected files are deleted (422) and an unreachable scanner rejects the upload (503); tus and presigned-upload handlers refuse to boot without an `Authorize` hook (`TestReceiveScansBeforeFilesBecomeVisible`, `TestClamAVUnavailableFailsClosed`, `TestTusConfigIsValidatedAtBoot`, `TestDirectRequiresAuthorize`) | [x] |
| Transactions with rollback-on-error/panic (`db.Tx`) so multi-step operations are atomic (`TestTxCommitRollbackPanic`) | [x] |
| Threat model of each subsystem recorded in its doc before BETA; security review gate in [PLAN.md](../PLAN.md) (`B.5.2`) (process: [security-review.md](security-review.md)) | [x] |
| GraphQL operations are bounded before any resolver runs: depth 12, complexity 1000 (list arguments `first`/`last`/`limit` multiply cost), 30 aliases, 10000 tokens, 1 MiB body; introspection (`__schema`, `__type`) counts toward aliases and complexity, nests at most 16 levels and resolves at most 200000 fields, so fragment chains over `__Type` cannot grow the response exponentially (`TestSecurity_A06_GraphQLIntrospectionBounded`, `TestSecurity_A06_GraphQLIntrospectionFieldCap`); persisted-query allowlist mode runs only listed documents and fails closed without a store (`TestSecurity_A06_GraphQLDepthAndComplexityLimits`, `TestSecurity_A06_GraphQLAllowlistOnly`) | [x] |
| GraphQL subscriptions are bounded: every WebSocket and SSE document goes through the same parse, validation, limits and persisted-query checks as HTTP; a socket that does not send `connection_init` within 10s is closed (4408); one connection runs at most 32 operations (`TOO_MANY_REQUESTS` beyond); introspection is off over the socket unless `WSConfig.Introspection` (`TestSecurity_A06_GraphQLSubscriptionsBounded`) | [x] |

### A07:2025 — Authentication Failures

| Default | Check |
| --- | --- |
| Session id regenerated on login and privilege change (anti-fixation); invalidated on logout (`TestSecurity_A07_SessionRegeneratedOnLogin`, `TestSecurity_A07_SessionInvalidatedOnLogout`) | [x] |
| Cookies: `HttpOnly`, `Secure`, `SameSite=Lax`, `__Host-` prefix over HTTPS (`TestSecurity_A07_SessionCookieFlags`, `TestSecurity_A02_ConfigChecks`) | [x] |
| Idle and absolute session timeouts (`TestAbsoluteLifetimeAndTouch`, `TestSessionLifecycleAllStores`) | [x] |
| Credential check hashes a dummy password when the user does not exist (timing-safe, no user enumeration) (`TestSecurity_A07_DummyHashOnUnknownUser`) | [x] |
| Generic error messages on login and password reset ("If the email exists…") (`TestSecurity_A07_LoginErrorsDoNotRevealUsers`, generated `TestPasswordReset`) | [x] |
| Password rules in user code (`app/requests/auth.go`): at least 12 and at most 256 characters by default; argon2id has no 72-byte limit and `hash.Bcrypt` refuses longer passwords (`TestSecurity_A07_AuthScaffoldDefaults`, `TestBcryptMigrationAndLimits`) | [x] |
| Login throttling per email + IP (5 per minute) and per account from any IP (20 per 10 minutes), keyed by the email normalized like the login schema (trimmed, lower case) so spaces and case open no new buckets (generated `TestLoginLogoutAndThrottle`, run by `TestNewProjectEndToEnd` / `TestNewInertiaProjectEndToEnd`); remember-me tokens rotated on use and single use even under concurrent replay (`TestRememberMe`, `TestSecurity_A07_RememberTokenSingleUseUnderConcurrency`) | [x] |
| API tokens with expiry, abilities and revocation; `last_used_at` tracking (`TestTokensAndAbilities`) | [x] |

| Social login: PKCE, signed single-use `state`/`nonce` cookie, OIDC ID-token verification, redirect URL from config only (`TestSecurity_A07_SocialStateMismatch`, `TestSecurity_A07_SocialReplayAndPKCE`, `TestSecurity_A07_IDTokenChecks`) | [x] |
| TOTP 2FA (`auth/totp`, `install:auth --2fa`): a code works once per time step, also across concurrent requests (`TestValidateWindowAndReplay` and the generated `TestTwoFactorLogin`); recovery codes stored as hashes and single use (`TestRecoveryCodes`, generated `TestTwoFactorRecoveryCodes`); secret and hashes in `orm.Encrypted` columns, challenge/confirm/disable limited to 5 per minute per user + IP, disabling needs the password (`TestSecurity_A07_TwoFactorScaffoldDefaults`, generated `TestTwoFactorThrottle`, `TestTwoFactorDisableNeedsPassword`); the scaffold refuses to install when it cannot enforce the code step at login (`TestSecurity_A07_TwoFactorFailsClosedOnEditedLogin`); with `install:api`, `POST /api/v1/tokens` needs the code or a recovery code too, whichever was installed first (generated `TestAPITokenRequiresTwoFactor`, `TestTwoFactorPatchesMatchScaffolds`). Generated tests run in `TestNewProjectEndToEnd` and `TestNewInertiaProjectEndToEnd` | [x] |

**Post-1.0:** passkeys/WebAuthn.

### A08:2025 — Software or Data Integrity Failures

| Default | Check |
| --- | --- |
| Signed and encrypted cookies (tampering detected) (`TestSecurity_A08_TamperedCookieRejected`, `TestSecurity_A04_EncryptedCookieCannotMoveBetweenNames`) | [x] |
| Signed URLs and signed email-verification links (HMAC with expiry) (`TestSecurity_A01_SignedURLTampering`, generated `TestRegisterAndVerifyEmail`) | [x] |
| Queue payloads are JSON mapped to registered job kinds — **no reflection-based deserialization of arbitrary types** (`TestTimeoutPanicAndUnknownKind`, `TestRegisterAndDispatchErrors`) | [x] |
| CSRF protection with `http.CrossOriginProtection` (Fetch Metadata + Origin) on all unsafe methods; webhook bypasses must be declared (`TestSecurity_A01_CrossOriginProtection`) | [x] |
| Webhook signature verification helper (`webhook.VerifyHMAC`) (`TestSecurity_A08_WebhookMiddleware`, `FuzzVerifyHMAC`) | [x] |
| Standard Webhooks verification with a ±5 minute timestamp window against replays (`TestSecurity_A08_StandardWebhooks`); a malformed secret is a boot error; `webhook.Dedupe` runs each event id once and fails closed when its store is down (`TestSecurity_A08_WebhookDedupe`, `TestSecurity_A04_WebhookDedupeFailsClosed`) | [x] |
| Outgoing webhooks (`webhook.Sender`): every attempt is signed with the Standard Webhooks scheme (verifiable with `webhook.Standard`), sent through `httpclient.Safe` so user-supplied endpoint URLs never reach loopback, private or metadata addresses (`TestSecurity_A01_OutgoingWebhookSSRF`), only over https unless `InsecureAllowHTTP` (`TestSecurity_A04_OutgoingWebhookRequiresHTTPS`), never unsigned (a missing or malformed secret fails the attempt), without following redirects (3xx is final); attempts store an error category, never the resolved address or TLS detail; jobs carry the endpoint id, not its URL or secret, and logs and errors omit URL, secret and payload (`TestSecurity_A09_OutgoingWebhookLogsNoSecrets`) | [x] |
| Release artifacts with checksums, SBOM and signatures (process: `.goreleaser.yaml`, `release.yml`) | [x] |
| Migrations tracked with checksums; a modified, already-applied migration is reported by `migrate:status` (`TestMigratorChangedAndMissing`, `TestMigrateCommands`) | [x] |

### A09:2025 — Security Logging and Alerting Failures

| Default | Check |
| --- | --- |
| Structured `slog` request logs with request id, user id (when authenticated), route name, status, duration (`TestLogger`) | [x] |
| Security events logged at `WARN` with a stable `event` attribute: `auth.login_failed`, `auth.remember_rejected`, `authz.denied`, `csrf.rejected`, `ratelimit.exceeded`, `session.invalid`, `url.signature_invalid`, `webhook.signature_invalid`, `broadcast.denied`, `idempotency.key_reused`, `idempotency.unscoped` (`TestSecurity_A09_SecurityEventsAtWarn`, `TestSecurity_A08_TamperedCookieRejected`, `TestSecurity_A01_CrossOriginProtection`) | [x] |
| Secrets, passwords, tokens and `Authorization`/`Cookie` headers redacted from logs (`TestSecurity_A09_LoggerNeverLogsCredentials`) | [x] |
| `drivers/s3` keeps the secret key and session token in `config.Secret`; formatting a config or disk and S3 errors never include them or a request signature (`TestSecurity_A09_S3NeverLeaksCredentials`) | [x] |
| Log injection prevented: `slog` attribute encoding (no raw string concatenation) (`TestRequestID`, `TestSecurity_A05_SSEFieldInjection`) | [x] |
| The admin audit log (`admin_audit_logs`: actor, resource, record, action, before/after of changed columns) never stores sensitive columns (`json:"-"`, secret-like names, `Sensitive(...)`): they are recorded as redacted, without values, and never shown or echoed into forms (`TestSecurity_A09_AdminAuditOmitsSensitive`) | [x] |
| `events.Listen[security.Event]` hook to forward security events to alerting (email, Slack, SIEM) (`TestSecurityEventsReachTheBus`) | [x] |
| OpenTelemetry tracing module (`drivers/otel`) traces requests, propagates `traceparent` and adds `trace_id` / `span_id` to log records logged with the span's context (`otel.LogHandler`, installed on `app.Log` by the provider) (`TestMiddleware_ServerSpanPerRequest`, `TestMiddleware_ExtractsTraceparentAndBaggage`, `TestLogHandler`, `TestProvider_CorrelatesRequestLogs`) | [x] |
| Trace spans (`drivers/otel`) record the route pattern, never the raw path or query string (`TestSecurity_A09_OTelSpansOmitPathAndQuery`) | [x] |
| Devtools shows SQL as a normalized shape (literals replaced by `?`) and bound arguments as types and lengths, never values; job and event records carry kinds and types, not payloads (`TestSecurity_A09_DevtoolsRedactsQueryArgs`) | [x] |

**App responsibility:** ship logs to durable storage and configure alerts on the security events.

### A10:2025 — Mishandling of Exceptional Conditions

| Default | Check |
| --- | --- |
| **Central error handler** (single place) maps errors to responses; handlers return `error` instead of writing partial responses (`TestErrorHandlerProblemJSON`, `TestErrorHandlerAfterWrite`) | [x] |
| `Recover` middleware converts panics to 500, logs stack trace server-side, never to the client (`TestSecurity_A10_RecoverHidesPanic`) | [x] |
| **Fail closed**: authorization errors, missing policies, rate limiter store failures and CSRF check errors deny the request; a session store failure drops the session (the user is signed out) instead of reusing data (`TestSecurity_A01_UnregisteredPolicyDenies`, `TestSecurity_A06_RateLimitMiddlewareFailsClosed`, `TestSecurity_A01_CrossOriginProtection`, `TestSecurity_A04_IdempotencyFailsClosed`) | [x] |
| `db.Tx` rolls back on error or panic; no partial writes (`TestTxCommitRollbackPanic`) | [x] |
| Generic client messages (RFC 9457 problem details) without internals (CWE-209) (`TestSecurity_A10_InternalErrorsNotLeaked`) | [x] |
| Missing/invalid parameters produce 400/422 with field errors, never zero-value defaults silently (CWE-234) (`TestBindJSONErrors`, `TestParseJSONSuccessAndProblem`) | [x] |
| Timeouts on server, DB queries (`context`), outbound HTTP; rate limiting and quotas against resource exhaustion (`TestSecurity_A02_ServerTimeouts`, `TestTimeout`, `TestLimitsAndAllowedServer`, `TestSecurity_A06_RateLimitMiddlewareFailsClosed`) | [x] |
| Repeated identical errors aggregated in logs to keep alerting useful: in production the default logger writes the first ERROR of a kind, counts repeats for `LOG_DEDUP_WINDOW` (1m) and writes one `repeated` summary with the count (`logging.Dedup`) (`TestSecurity_A09_RepeatedErrorsAggregated`, `TestDedup`, `TestDedupConcurrent`) | [x] |
| RPC errors (`contrib/grpc`): unknown errors and panics reach clients as `Internal` "internal error", the cause and stack only in the server log, and a returned or wrapped `*connect.Error` with a server-fault code (Internal, Unknown, DataLoss, Unavailable) keeps its code but not its text (`TestSecurity_A10_GRPCInternalErrorsHidden`, `TestSecurity_A10_GRPCConnectErrorsMasked`); request messages are capped at 4 MiB (`ResourceExhausted` beyond, `TestSecurity_A10_GRPCMessageSizeLimited`) | [x] |
| `errcheck`, `nilerr`, `errorlint`, `forcetypeassert` linters enforce handled errors in the framework itself (enforced by `.golangci.yml`) | [x] |
| GraphQL resolver errors that are not client-safe (plain errors, 5xx, panics, serialization failures) are reported as `Internal server error` with code `INTERNAL_SERVER_ERROR` and logged; their text appears only with `WithDebug(true)`; a 4xx error that wraps a cause (orm's unique-constraint conflict wraps the driver error) gets only its status text (`TestSecurity_A10_GraphQLInternalErrorsMasked`, `TestSecurity_A10_GraphQLClientErrorsMasked`) | [x] |

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
| `webhook.Sender`, `webhook.NewSecret` | outgoing webhooks: Standard Webhooks signatures, `httpclient.Safe` delivery from a queue job, retries with backoff, 410 stops, `SQLDeliveryLog` of attempts (`TestSecurity_A01_OutgoingWebhookSSRF`, `TestSecurity_A04_OutgoingWebhookRequiresHTTPS`, `TestSecurity_A09_OutgoingWebhookLogsNoSecrets`) |
| `security.Event` | every `web.SecurityEvent` (login failures, `authz.denied`, CSRF, rate limits, bad signatures) is also dispatched on `app.Events` for alerting: `events.Listen(app.Events, func(ctx, e security.Event) error {...})` |
| depguard `crypto-randomness` | `math/rand` is a lint error in `auth`, `session`, `csrf`, `signed`, `crypt`, `social`, `webhook` and `config` |

## Verification

- Every default above names the test that asserts it (`TestSecurity_<Category>_<Behaviour>` where the row is security-specific, e.g. `TestSecurity_A01_StaticRejectsTraversal`) or the mechanism that enforces it (lint rule, CI job, release config), so `go test -run TestSecurity ./...` is the security regression suite.
- Fuzz targets for parsers that see untrusted input (router patterns and request paths, `.env`, JSON and form binding, multipart, tus headers, upload names, cookie sessions, JWTs, `crypt` tokens, storage paths, locale tags, webhook and Stripe signatures, GraphQL documents) run for an hour each every week ([`fuzz.yml`](../.github/workflows/fuzz.yml)).
- CI: `gosec` (via golangci-lint), `govulncheck`, CodeQL, OpenSSF Scorecard.
- BETA gate `B.5.2`: [security review](security-review.md) against this document and ASVS 5.0 Level 2, with findings, residual risks and fuzzing results.

## References

- OWASP Top 10:2025 — <https://top10.owasp.org/2025/>
- OWASP ASVS — <https://owasp.org/www-project-application-security-verification-standard/>
- Go security best practices — <https://go.dev/doc/security/best-practices>
- Filippo Valsorda, CSRF and `CrossOriginProtection` — <https://words.filippo.io/csrf/>
- RFC 9457 Problem Details — <https://www.rfc-editor.org/rfc/rfc9457>
