# Security audit 2026-11-07

Scope: production Go code of the repository at `feat/http-json-v2` (PR #149), using the vendored
`security-audit` skill. Profile: `quick`, partial coverage (see [Coverage](#coverage)).

## Run status

This run is **partial**. One reviewer read the source sequentially: parallel hunter agents were not
available (the delegation call was not approved by the environment), so there were no hunter waves, no
coverage-critic pass and no separate verifier agents. Phase 5 was a re-derivation by the same reviewer
from source, without the earlier notes; it found that the first fix for A05-1 could be bypassed with a
comment between a function name and its parenthesis, and that was fixed too. The skill's CLI validators
refuse to read files on Windows (no `O_NOFOLLOW`), so both JSON files were validated through the
validators' library functions (`validateDocument`) with no errors. A clean result for a unit is limited
to the paths listed in its ledger entry.

## Summary

| Severity | Confirmed | Fixed |
| --- | --- | --- |
| Critical | 0 | 0 |
| High | 0 | 0 |
| Medium | 0 | 0 |
| Low | 1 | 1 |
| Informational | 0 | 0 |

| ID | Severity | CWE | Location | Title | Fix |
| --- | --- | --- | --- | --- | --- |
| A05-1 | Low | CWE-668 | `boost.go:438` | boost `database_query` read-only guard lets side-effecting SELECTs through | fixed |

## Findings

### A05-1: read-only guard of `database_query` is a keyword denylist

- Severity: low (likelihood low, impact low). Confidence: medium.
- File: `boost.go:438` (`boostServer.databaseQuery`), patterns at `boost.go:420`.
- Impact: the boost MCP tool is meant to run one read-only statement for an AI agent. Statements that
  start with `SELECT` and contain no write keyword pass the guard, and a read-only transaction does not
  stop them: `SELECT ... INTO OUTFILE`, `SELECT LOAD_FILE(...)` (MySQL), `SELECT pg_read_file(...)`,
  `SELECT pg_terminate_backend(...)`, `SELECT pg_sleep(...)` (PostgreSQL), `SELECT SLEEP(...)`. Effects
  are bounded by the privileges of the database account and need a prompt-injected agent; the tool is
  development-only.
- Reproduction: send each statement through the `database_query` tool
  (`TestSecurity_A05_BoostQueryRefusesSideEffectingSelects`). Before the fix none of them returned the
  read-only refusal; on the SQLite test database they reached the engine and failed there only because
  the functions do not exist.
- Fix: add `into` to the write keywords and refuse calls to file, backend-control, sleep and extension
  functions (`sideEffectFunc`). Fix commits: `fbee536d`, and a follow-up after the Phase 5 re-check that
  matches the function name alone, because `pg_sleep/**/(600)` skipped the first pattern.

## Dropped candidates

- Open redirect via `Ctx.Redirect` and HTMX `HX-Location`: the target is chosen by the caller; user
  input goes through `SafeRedirect`, which rejects `//`, `/\` and cleaned variants.
- TLS redirect server host: redirects to `APP_URL`, never the request `Host`.
- Client IP spoofing: `X-Forwarded-For` is read only from trusted proxies, right to left.
- Idempotency replay across callers: scope is required and keyed by principal or token digest.
- SSRF in `httpclient.Safe`: address check runs at dial time on every connection, redirects included.
- SQL injection in the ORM: columns are validated against model metadata and quoted; operators
  allow-listed; values bound.
- Mail header injection: addresses go through `mail.ParseAddress`, subject and tags are checked.
- JWT algorithm and key confusion: each key accepts only its own algorithm; comparisons are
  constant-time.
- Workflow injection: actions are pinned by SHA, event values pass through `env`.
- Session fixation: a cookie id is accepted only when the store already holds it, and login regenerates it.
- Remember-me: tokens are single use (the DELETE decides the winner), hashed at rest, bound to the session
  version and compared in constant time.
- Account takeover by social sign-in: the scaffold links by email only when the provider verified it
  (Microsoft uses `xms_edov`, GitHub the primary verified address) and ends the old account's access.
- Password reset: 130-bit single-use token, hashed at rest, constant-time compare, all access revoked.
  The known-account path does two extra queries before queueing the mail, a timing difference that is
  not reliably measurable and has no demonstrated impact.
- GIF decompression bomb: `image/gif` refuses a frame larger than the screen (checked locally); PNG, JPEG
  and WebP are bounded by the pixel check on the decoded header.
- GraphQL fragment amplification: fragment costs are memoized and complexity, depth and aliases are capped.
- `engine.Sanitized` on Windows (device names, `name:stream`): the result stays inside the run directory
  and no cross-boundary effect was shown.
- Upload: content is sniffed, size capped while streaming, names sanitized and never used as paths.
- Webhooks: HMAC compare is constant time, `Standard` bounds replays with a timestamp tolerance.

## Coverage

Source-reviewed (each is a `covered` unit in
[the coverage ledger](security-audit-2026-11-07.coverage.json) with its paths): `boost.go`,
`web/response.go`, `web/ctx.go`, `api/idempotency.go`, `httpclient`, `storage/local.go`, `orm/query.go`,
`auth/jwt`, `mail`, `devtools/access.go`, `middleware/maintenance.go`, `session/manager.go`, the
remember-me store, the auth-core and auth-social scaffolds, `social` verification, `upload` save,
`webhook`, `admin` guards, `broadcast` subscription, GraphQL limits, imaging decode, `engine`, view and
inertia script embedding, htmx and vite tag builders, the scaffolded Dockerfile and selected workflows.

Still `deferred`, with no review beyond grep for SQL and format-string injection: the internals of
`drivers/*` (postgres, redis, mongo, mysql, s3 signing) and of `contrib/*` modules other than GraphQL,
imaging and engine (grpc, cloudflare, qrcode, stripe client). Other packages were not opened and are not
in the ledger. No prior ledger existed.

## Related change

HS-3 (not a vulnerability): `jwt.VerifyOptions.Nonce`, an opt-in nonce check, added in commit `89934d31`.

Machine-readable records: [findings](security-audit-2026-11-07.findings.json),
[coverage](security-audit-2026-11-07.coverage.json).
