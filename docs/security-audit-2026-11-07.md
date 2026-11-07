# Security audit 2026-11-07

Scope: production Go code of the repository at `feat/http-json-v2` (PR #149), using the vendored
`security-audit` skill. Profile: `quick`, partial coverage (see [Coverage](#coverage)).

## Run status

This run is **incomplete and partial**. It was done by one reviewer reading source; no parallel hunter
waves, no coverage-critic pass and no separate verifier agents ran (the delegation call was not approved
by the environment). Findings were validated by the reviewer with a code-path argument and a local test,
not by an independent agent. The skill's CLI validators refuse to read files on Windows (no
`O_NOFOLLOW`), so both files were validated through the validators' library functions instead
(`validateDocument`), with no errors. A clean result here is not a claim that the rest of the repository
is free of defects.

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
  functions (`sideEffectFunc`). Fix commit: `fbee536d`.

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

## Coverage

Source-reviewed: `boost.go`, `web/response.go`, `web/ctx.go`, `api/idempotency.go`,
`httpclient/httpclient.go`, `storage/local.go`, `orm/query.go`, `auth/jwt/jwt.go`, `mail`,
`devtools/access.go`, `middleware/maintenance.go`, `social` redirect start, selected workflows. Units
for `session`, `auth` (beyond JWT), `upload`, `webhook`, `admin`, `broadcast`/`sse`, `contrib`,
`drivers`, `internal/cli` and the template packages are recorded as `deferred` in
[the coverage ledger](security-audit-2026-11-07.coverage.json); they were only sampled by grep, and
`admin` and `webhook` have extensive `TestSecurity_*` suites. No prior ledger existed.

Machine-readable records: [findings](security-audit-2026-11-07.findings.json),
[coverage](security-audit-2026-11-07.coverage.json).
