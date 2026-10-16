# Testing

How the framework itself is tested, and the testing toolkit it ships to apps. Decisions `Q1`, `Q2` in [brainstorming.md](brainstorming.md).

## Policy (framework)

- **Every exported feature has tests.** A PR adding a feature without tests is not merged.
- **Coverage gate:** ≥ 80% per package in core (measured on `ubuntu/stable`), ≥ 90% for `router`, `validate`, `session`, `orm`, `schema`, `auth`, `config`.
- **Race detector** on in CI (`-race`); Windows contributors may run without it locally (needs cgo).
- **No network, no Docker** in unit tests. Integration tests are behind `//go:build integration`.
- **Assertions:** stdlib `testing` + `github.com/google/go-cmp/cmp`. No testify in core.
- Tests use `t.Context()`, `t.TempDir()`, `t.Setenv` only where unavoidable (prefer injected `getenv`), `t.Parallel()` where safe.

## Test types

| Type | Location | Command | Notes |
| --- | --- | --- | --- |
| Unit | `*_test.go` next to code | `go test ./...` | Table-driven subtests |
| Example | `example_test.go` | `go test ./...` | Doubles as pkg.go.dev docs |
| Golden | `testdata/*.golden` | `go test ./... -update` | Generators, codemods, error pages, CLI output |
| Fuzz | `Fuzz*` | `go test -fuzz=FuzzX ./router` | Router patterns, `.env` parser, validator tags, multipart |
| Integration | `//go:build integration` | `task test:integration` | Needs `DATABASE_URL`, `REDIS_URL`, `MONGO_URL` |
| End-to-end | `e2e/` | `task test:e2e` | `lumek new` → build → boot → HTTP checks, on all OSes |
| Benchmarks | `Benchmark*` with `b.Loop()` | `task bench` | Router, query builder, render |

Time-dependent code (sessions, rate limits, queues, scheduler) is tested with `testing/synctest` instead of sleeps.

## Toolkit for apps (`testing/` package, imported as `lumektest`)

### HTTP client

```go
func TestCreatePost(t *testing.T) {
	app := lumektest.New(t, bootstrap.New)            // boots app with APP_ENV=test, in-memory SQLite, migrations run
	user := lumektest.Create(t, app, factories.User)  // ALPHA

	app.Test(t).
		ActingAs(user).
		PostForm("/posts", url.Values{"title": {"Hello"}, "body": {"World"}}).
		AssertRedirectRoute("posts.index").
		AssertSessionHas("success")

	lumektest.AssertDatabaseHas(t, app, "posts", map[string]any{"title": "Hello"})
}
```

| Group | Methods |
| --- | --- |
| Requests | `Get`, `Post`, `PostJSON`, `PostForm`, `Put`, `Patch`, `Delete`, `WithHeader`, `WithCookie`, `WithSession`, `ActingAs(user, guard...)`, `HTMX()` (sets htmx headers), `Inertia()` |
| Status | `AssertOK`, `AssertCreated`, `AssertNoContent`, `AssertStatus(n)`, `AssertNotFound`, `AssertForbidden`, `AssertUnprocessable` |
| Redirects | `AssertRedirect(url)`, `AssertRedirectRoute(name, params...)` |
| Body | `AssertSee(text)`, `AssertDontSee`, `AssertJSON(v)`, `AssertJSONPath("data.title", "x")`, `AssertHeader` |
| Session | `AssertSessionHas`, `AssertSessionHasErrors("title")`, `AssertValid`, `AssertInvalid("title")` |
| HTMX | `AssertHXTrigger("post-created")`, `AssertFragment("post-list")` |
| Inertia | `AssertInertia(component)` with prop assertions |

Requests run in-process via `httptest` (no ports). Each assertion failure prints the request, status, headers and a trimmed body.

### Database

- Default test connection: SQLite in memory (one database per test via unique `file:<test>?mode=memory&cache=shared` name), migrations applied once per package and cloned, or a fresh migrate when cheaper.
- `lumektest.RefreshDatabase(t, app)` wraps the test in a transaction rolled back in `t.Cleanup` (Postgres/SQLite).
- Postgres integration: `CREATE DATABASE test_x TEMPLATE lumek_template` per package for speed.
- Assertions: `AssertDatabaseHas`, `AssertDatabaseMissing`, `AssertDatabaseCount`, `AssertSoftDeleted`.

### Fakes

Fakes replace services **on the app instance** (no globals):

```go
mail := lumektest.FakeMail(t, app)
q := lumektest.FakeQueue(t, app)
ev := lumektest.FakeEvents(t, app, events.UserRegistered{})
clock := lumektest.FakeClock(t, app, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))

// ... exercise code ...

mail.AssertSent[mails.Welcome](t, func(m mails.Welcome) bool { return m.To == "a@b.c" })
q.AssertPushed[jobs.SendInvoice](t, 1)
ev.AssertDispatched[events.UserRegistered](t)
clock.Advance(24 * time.Hour)
```

Available as their subsystems land: mail, queue, events, storage (in-memory `fs.FS`), cache, HTTP client (`FakeHTTP` with stray-request prevention), clock, hasher (fast params).

## CI matrix

| Job | Matrix |
| --- | --- |
| unit | `ubuntu`, `windows`, `macos` × Go `stable` (`oldstable` joins once Go 1.28 ships, since 1.27 is the minimum) |
| integration | `ubuntu` + services `postgres:17`, `redis:7`, `mongo:8` |
| e2e | `ubuntu`, `windows`, `macos` × `stable` |
| coverage | `ubuntu/stable` → Codecov, gate enforced by script |
