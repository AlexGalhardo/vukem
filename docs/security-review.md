# Security review (B.5.2)

BETA gate review of Vukem against its security contract ([security.md](security.md)), the [OWASP Top 10:2025](https://top10.owasp.org/2025/) and the [OWASP ASVS 5.0](https://owasp.org/www-project-application-security-verification-standard/) Level 2 requirements that apply to a web framework. Reviewed in October 2026 on `main` after v0.20.1.

## Scope

- Core module: `csrf`, `session`, `auth` (`auth/jwt`, `auth/totp`, `auth/hash`), `social` (OAuth 2.0 / OIDC), `signed`, `crypt`, `webhook`, `upload` (multipart, tus, presigned), `storage` (`os.Root`), `orm` / `schema` / `database` (query building, identifiers, raw SQL), `api` query conventions, `z`, `queue`, `middleware` (secure headers, CSP, CORS, body limits, method override, maintenance, request id), `router`, `web` (error rendering, redirects, client IP, binding), `view`, `vite`, `inertia`, `htmx`, `i18n`, `sse`, `ratelimit`, `httpclient`, `mail`, `devtools`, `admin`, `broadcast`, `apidocs` / OpenAPI, boot checks in `app.go`.
- Contrib modules: `contrib/graphql`, `contrib/grpc`, `contrib/stripe`.
- Drivers, as far as security rows reference them: `drivers/websocket`, `drivers/s3`, `drivers/mysql`, `drivers/otel`.
- Generated code: the `install:auth` (`--2fa`, `--social`), `install:api` and `install:billing` scaffolds under `internal/cli/scaffolds`, and the starter.
- Out of scope: the documentation site (`site/`), example apps, third-party dependencies beyond `govulncheck`.

## Method

1. **Contract check.** Every row of [security.md](security.md) was traced to the test that asserts it, or to the mechanism (lint rule, CI job, release config) that enforces it. Rows without one got a `TestSecurity_*` test; rows that promised more than the code does were corrected. Of the 74 Prototype and ALPHA rows, 73 name their test or mechanism and one (starter tool pinning) names a tracked gap (R-6); of the 50 BETA rows, 49 name a test and one (log aggregation) names a gap (R-7). Both gaps are closed since; every row now names its test or mechanism.
2. **Code review.** Five parallel passes over the scope (auth and crypto; data, uploads and SQL; the HTTP surface; outbound, realtime and contrib; the contract itself), each tracing untrusted input from the trust boundary to the sensitive sink before reporting, scoring with DREAD and checking for upstream guards.
3. **Fixes.** Each confirmed issue got a failing test first, a root-cause fix and its own `fix(<pkg>)` commit with a [security.md](security.md) row update.
4. **Static analysis.** `golangci-lint run ./...` (gosec, depguard, errcheck, …) and `govulncheck` v1.9.0 on the root module and every contrib, driver and example module (see [Tooling results](#tooling-results)).
5. **Fuzzing.** New property-based fuzz targets for every untrusted-input parser that lacked one, a local campaign of at least 10 minutes per target, and a weekly CI campaign of one hour per target (see [Fuzzing](#fuzzing)).

## ASVS 5.0 chapters

| Chapter | Result | Evidence |
| --- | --- | --- |
| V1 Encoding and Sanitization | pass after fixes | bound parameters and identifier allowlists (`orm`, `api`, `admin`); `html/template`; SSRF blocklist extended (F-07); mail and SSE header injection rejected (F-17); LIKE wildcards in admin search are R-10 |
| V2 Validation and Business Logic | pass with residual | `z` rejects invalid UTF-8 and control characters; uploads count every part (F-14); API `IN` lists unbounded (R-10) |
| V3 Web Frontend Security | pass after fixes | secure headers and nonce CSP; open redirects (F-01, F-04); CORS-simple JSON bypass (F-06); sandboxed storage responses (F-13); HSTS behind a proxy needs opt-in (R-11) |
| V4 API and Web Service | pass after fixes | GraphQL introspection bound (F-02), GET mutations refused, gRPC message cap; rate limit keys (F-10) |
| V5 File Handling | pass after fixes | content sniffing, random names, `os.Root`; Verify key binding (F-11); dot directories (F-13); bidi names (F-15); tus uploads are capability URLs (R-13) |
| V6 Authentication | pass after fixes | argon2id, dummy hash (`TestSecurity_A07_DummyHashOnUnknownUser`), generic errors; 2FA on API tokens (F-08); throttle key normalization and per-account limit (F-09) |
| V7 Session Management | pass with residual | regeneration on login, idle and absolute timeouts, `__Host-` cookies, logout deletes the server-side session; remember-me single use (F-16); stateless cookie sessions cannot be revoked server-side (R-2) |
| V8 Authorization | pass | unregistered policy = deny, admin abilities and policies, broadcast channel rules, devtools loopback-only |
| V9 Self-contained Tokens | pass | JWT algorithm pinned per key, `exp` required, weak keys refused (`FuzzVerify`), refresh rotation single winner |
| V10 OAuth and OIDC | pass with residual | PKCE S256, single-use `state`/`nonce`, ID token checks; stateless `UserFromToken` does not bind the audience (R-1) |
| V11 Cryptography | pass | AES-256-GCM with random nonces, HKDF purposes, key rotation, constant-time comparisons, no `math/rand` (depguard) |
| V12 Secure Communication | pass | Go TLS defaults, no `InsecureSkipVerify`, MySQL and S3 require TLS remotely, SMTP STARTTLS |
| V13 Configuration | pass after fixes | boot refusals; unknown `APP_ENV` now a boot error (F-21); debug, devtools, OpenAPI, GraphQL explorer and gRPC reflection off in production |
| V14 Data Protection | pass | `config.Secret` redaction, `orm.Encrypted`, admin audit redaction, `Cache-Control: no-store` on admin |
| V15 Secure Coding and Architecture | pass | no reflection deserialization in queues, no shell execution (gosec G204), stdlib-first dependency policy |
| V16 Security Logging and Error Handling | pass after fixes | WARN security events (`TestSecurity_A09_SecurityEventsAtWarn`); CWE-209 masking in GraphQL (F-03) and gRPC (F-05); maintenance panic (F-12); trace correlation (`TestProvider_CorrelatesRequestLogs`) and error aggregation (`TestSecurity_A09_RepeatedErrorsAggregated`), closing R-7 |
| V17 WebRTC | n.a. | the framework has no WebRTC |

## Findings

Severity follows DREAD (Critical 8–10, High 6–7.9, Medium 4–5.9, Low 1–3.9). All fixed findings are on the B.5.2 branch with a regression test.

| ID | Severity | Component | Finding | Status |
| --- | --- | --- | --- | --- |
| F-01 | Medium | `web.SafeRedirect` | `https://app//evil.com` became the scheme-relative `//evil.com` (open redirect, CWE-601) | fixed `56dd98e` |
| F-02 | High | `contrib/graphql` | introspection selections skipped every limit: a 1 KB fragment chain over `__Type` returned 7 MB, growing about 2.7× per level (unauthenticated DoS) | fixed `f04dd80` |
| F-03 | Medium | `contrib/graphql` | 4xx errors that wrap a cause (orm's unique-constraint conflict) sent driver text, table and constraint names to clients (CWE-209) | fixed `c3aa7e4` |
| F-04 | Medium | `web.SafeRedirect` | `/./\evil.com` passed the check and `http.Redirect` cleaned it into `/\evil.com` (open redirect) | fixed `bdf0fbe` |
| F-05 | Medium | `contrib/grpc` | a returned or wrapped `*connect.Error` with a server-fault code kept its text (database errors, downstream dial addresses) | fixed `30e98a9` |
| F-06 | Low | `middleware.JSONOnly`, `web.Ctx.IsJSON` | `text/plain; x=+json` (CORS-simple, no preflight) passed as JSON | fixed `c5cb0d6` |
| F-07 | Medium | `httpclient` | 6to4, local NAT64, Teredo, IPv4-compatible and -translated IPv6, site-local and the 6to4 relay were allowed (SSRF to loopback and metadata on NAT64 / 6to4 hosts) | fixed `4254b55` |
| F-08 | High | `install:api` + `install:auth --2fa` | `POST /api/v1/tokens` issued a 30-day `*` token for email and password alone, bypassing two-factor authentication | fixed `c34e04e` |
| F-09 | High | `install:auth` | the login throttle key lower-cased but did not trim the email while the schema trims it: unlimited guesses with `" a@b.co"`, `"a@b.co "`… from one IP; no per-account limit | fixed `1ef3674` |
| F-10 | Medium | `ratelimit` | `ByToken` was the default key and the API scaffold used it before authentication: rotating made-up bearer tokens were never limited and grew the store | fixed `994276b` (breaking) |
| F-11 | Medium | `upload.Verify` | the client-reported key was only path-cleaned: any object on the disk could be deleted or adopted as the caller's upload | fixed `31f6178` |
| F-12 | Low | `middleware.Maintenance` | an empty request path (absolute-form or `*` target) panicked in the bypass check (500 and stack in logs) | fixed `95a19ba` |
| F-13 | Low | `storage.Handler` | files under dot directories (`.git/config`) were served; uploaded HTML/XML ran on the app origin | fixed `88da87a` |
| F-14 | Low | `upload.Receive` | nameless multipart parts were not counted, so thousands of tiny parts bypassed `MaxFields` | fixed `5897d11` |
| F-15 | Low | `upload.SanitizeName` | bidi overrides and zero-width characters survived, disguising extensions | fixed `5077f1c` |
| F-16 | Low | `auth` remember-me | concurrent replays of one stolen cookie all logged in; a failed DELETE left the token reusable | fixed `9afea58` |
| F-17 | Low | `mail` | attachment `ContentType` was written raw into MIME part headers (header injection from upload metadata) | fixed `6396868` |
| F-18 | Low | `web.Ctx.IP` | only the first `X-Forwarded-For` line was read: behind a proxy that appends a line, the client chose its IP (rate limit and throttle evasion) | fixed `8188ae4` |
| F-19 | Medium | `install:auth` password reset | a reset kept API tokens issued with the old password, and concurrent resets with one link could both succeed | fixed `1ef3674` |
| F-20 | Low | `docs/security.md` | rows promised behaviour the code lacks (events `auth.locked` / `token.revoked`, 72-byte passwords, JSON depth limit, govulncheck pre-push and cron, starter tool pinning, log correlation and aggregation) | corrected `e31231c`; gaps R-6, R-7 |
| F-21 | Medium | `app` boot | an unknown `APP_ENV` (`prod`, `Production`, trailing space) skipped every production check (debug page, devtools, insecure cookies) | fixed `4824717` (breaking) |

The fuzzing campaign found one oracle bug (upper-case hex in `Stripe-Signature` is the same MAC, not a forgery), fixed in `349bf75` with the input kept as a regression seed.

## Residual risks

Accepted or deferred, with the reason and what an app can do meanwhile.

| ID | Severity | Risk | Mitigation |
| --- | --- | --- | --- |
| R-1 | Medium | `social.UserFromToken` (stateless login with a provider access token) does not check that the token was issued to this app's client id, so a token granted to another OAuth app logs in as its owner | use the redirect flow (`Redirect` / `User`), which verifies the ID token audience; binding requires per-provider token introspection (post-BETA) |
| R-2 | Medium | with the default `cookie` session driver, logout and password reset cannot revoke copies of the cookie: a stolen cookie stays valid until the idle (2 h) or absolute (24 h) timeout | use the `database` or `redis` session driver in production (logout then deletes the server-side session, `TestSecurity_A07_SessionInvalidatedOnLogout`) |
| R-3 | Low | a session store `Delete` failure after `Regenerate` / `Invalidate` is only logged, and a `Load` failure starts an empty session rather than denying | monitor `session` errors; the store being down signs users out, it never reuses data |
| R-4 | Low | `webhook.VerifyHMAC` / `webhook.Middleware` sign the body only, without a timestamp, so captured deliveries can be replayed | make webhook handlers idempotent on the event id (as `contrib/stripe` does) |
| R-5 | Low | the 2FA challenge is throttled per user and IP only, and forgot-password sends mail synchronously (timing reveals known emails) | add a per-user challenge limiter and queue the reset mail in the generated code |
| R-6 | Low | supply chain: `govulncheck` ran on every push and in `task ci` but not in the pre-push hook, a weekly cron or generated apps; starters did not pin tools with the `tool` directive | closed `47f0cef` (pre-push hook, weekly `vuln.yml` on every module) and `a56c203` (`vukem new` pins `govulncheck` with a `tool` directive) |
| R-7 | Low | logs carried no trace id from `drivers/otel` and repeated errors were not aggregated | closed `48a79fd` (`otel.LogHandler` adds `trace_id` / `span_id`) and `023820b` (`logging.Dedup` on the production logger, `LOG_DEDUP_WINDOW`) |
| R-8 | Low | `graphql.WSConfig.Persisted` is separate from `HandlerConfig.Persisted`: an allowlist on HTTP does not cover subscriptions unless repeated | configure both; fail-closed sharing is post-BETA |
| R-9 | Low | `signed.New` accepts any key length (the app wires a 32-byte key derived from `APP_KEY`, so only direct use is affected) | use `app.Signer`; rejecting short keys changes the constructor signature |
| R-10 | Low | admin search does not escape `%` / `_` and `api.Query` does not cap `IN` lists or sort terms (cost amplification) | limit request sizes; escaping helpers are planned |
| R-11 | Low | behind a TLS-terminating proxy HSTS needs `TrustForwardedProto`, and `TrustedProxies` cannot be set from the environment; the Inertia 409 location uses `r.Host` | configure the proxy options in code and send HSTS from the proxy |
| R-12 | Low | tus upload URLs are bearer capabilities (128-bit random ids) and `upload.Verify` keys are not bound to the presigning caller | keep upload URLs out of logs and Referer; store the presigned key per user before calling `Verify` |
| R-13 | Low | `storage.PutUploaded` accepted any content type when `UploadRules.Types` was empty, unlike `upload.Rules` | fixed in v0.28.0: an empty `Types` is an error (`TestSecurity_A05_PutUploadedRequiresTypes`) |
| R-14 | Low | `orm.Encrypted` binds table and column, not the row id, so a ciphertext can be moved between rows by someone with database write access | out of the threat model (database write access) |

**R-11 follow-up (2026-10-17, branch `fix/deploy`):** the proxy options are now configurable from the environment. `TRUSTED_PROXIES` (CIDRs or IPs) feeds `web.Services.TrustedProxies`, `TRUST_FORWARDED_PROTO` enables HSTS from `X-Forwarded-Proto`, honoured only from those peers, and `CLIENT_IP_HEADER` (e.g. `X-Real-IP`) reads the client IP from a single-IP proxy header, also only from those peers. Invalid values refuse the boot. The Inertia 409 location still uses `r.Host`; that part of R-11 is open.

## App responsibilities

Beyond the per-category lists in [security.md](security.md):

- Call `Authorize` in every handler that touches owned data and scope queries by tenant or user.
- Terminate TLS (or configure the proxy and `TrustedProxies`), set `APP_URL` to the `https` origin, and use a server-side session driver in production (R-2).
- Use `httpclient.Safe()` for any request to a user-supplied URL; the framework itself fetches none.
- Run `govulncheck` in CI, review dependency updates and protect CI secrets.
- Ship logs to durable storage and alert on the `security event` records and `security.Event` listeners.
- Keep webhook handlers idempotent (R-4) and bind direct-upload keys to their caller (R-12).

## Fuzzing

Fuzz targets assert security properties, not only the absence of panics: accepted redirects stay on the origin, forged cookies, JWTs, webhook and Stripe signatures never verify, sanitized names have no separators or format characters, storage paths stay inside the root, router redirects stay local.

Local campaign: each target ran for 10 minutes with `-parallel 1`, eight processes at a time on a 16-core Windows 11 machine. The second wave of the first batch spanned a host suspend, so Go's `elapsed` counter shows more than 10 minutes of wall time for the same fuzzing budget. Weekly CI ([`fuzz.yml`](../.github/workflows/fuzz.yml)) runs every target for one hour (`-fuzztime=1h`, also on `workflow_dispatch`) and uploads crashers as artifacts.

| Target | Package | Run | Execs | Result |
| --- | --- | --- | --- | --- |
| `FuzzParseEnv` | `config` | 10m | 4,872,675 | pass |
| `FuzzDecrypt` | `crypt` | 10m | 5,146,043 | pass |
| `FuzzWhereColumn` | `orm` | 10m | 2,129,751 | pass |
| `FuzzRoutePattern` | `router` | 10m | 3,666,953 | pass |
| `FuzzServePath` (new) | `router` | 10m | 3,249,128 | pass |
| `FuzzParseCron` | `schedule` | 10m | 2,087,103 | pass |
| `FuzzParse` | `z` | 10m | 1,552,506 | pass |
| `FuzzCanonical` (new) | `i18n` | 10m | 3,247,861 | pass |
| `FuzzParseAcceptLanguage` (new) | `i18n` | 10m | 3,964,991 | pass |
| `FuzzVerifyHMAC` (new) | `webhook` | 10m | 4,337,972 | pass |
| `FuzzVerify` (new) | `auth/jwt` | ≥ 10m (suspend) | 1,407,145 | pass |
| `FuzzSessionCookie` (new) | `session` | ≥ 10m (suspend) | 706,566 | pass |
| `FuzzClean` (new) | `storage` | ≥ 10m (suspend) | 4,243,850 | pass |
| `FuzzFormToMap` | `web` | 10m | 3,062,530 | pass |
| `FuzzSafeRedirect` (new) | `web` | 10m | 3,098,764 | pass (written with the F-01 fix) |
| `FuzzSanitizeName` (new) | `upload` | 10m | 3,045,483 | pass |
| `FuzzParseMetadata` (new, tus) | `upload` | 10m | 2,080,535 | pass |
| `FuzzParseClamReply` (new) | `upload` | 10m | 3,035,888 | pass |
| `FuzzReceive` (new, multipart) | `upload` | 10m | 2,292,518 | pass |
| `FuzzTusRequest` (new, tus headers) | `upload` | 10m | 526,091 | pass |
| `FuzzParseQuery` | `contrib/graphql` | ≥ 10m (suspend) | 1,564,704 | pass |
| `FuzzParseSchema` | `contrib/graphql` | ≥ 10m (suspend) | 1,453,829 | pass |
| `FuzzConstructEvent` (new) | `contrib/stripe` | 10m | 1,878,379 | pass (first run: oracle fix `349bf75`) |

## Tooling results

Run on the review branch (Windows 11, Go 1.27.2, golangci-lint with the repository `.golangci.yml`, which enables gosec).

| Module | `golangci-lint run ./...` (gosec included) | `govulncheck@v1.9.0 ./...` |
| --- | --- | --- |
| root (`github.com/AlexGalhardo/vukem`) | 0 issues | 0 affecting; `GO-2026-5932` in the required `golang.org/x/crypto@v0.58.0` is not called (no fixed version yet) |
| `contrib/graphql` | 0 issues | no vulnerabilities |
| `contrib/grpc` | 0 issues | 0 affecting; same `GO-2026-5932`, not called |
| `contrib/stripe` | 0 issues | no vulnerabilities |
| `drivers/mongo` | 0 issues | 0 affecting; same `GO-2026-5932`, not called |
| `drivers/mysql`, `drivers/otel`, `drivers/postgres`, `drivers/redis`, `drivers/s3`, `drivers/websocket` | 0 issues | no vulnerabilities |

`examples/blog` is outside `go.work` and was not scanned. Watch `GO-2026-5932` and bump `golang.org/x/crypto` once a fixed version exists.

## Verification

- `go test -run TestSecurity ./...` (root module and `contrib/graphql`, `contrib/grpc`, `contrib/stripe`) is the security regression suite.
- The generated-code findings (F-08, F-09, F-19) are asserted by generated tests (`TestAPITokenRequiresTwoFactor`, `TestLoginLogoutAndThrottle`, `TestPasswordReset`) that run inside `TestNewProjectEndToEnd` and `TestNewInertiaProjectEndToEnd`.
