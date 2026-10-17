# Testing

How the framework itself is tested, and the testing toolkit it ships to apps. Decisions `Q1`, `Q2` in [brainstorming.md](brainstorming.md).

## Policy (framework)

- **Every exported feature has tests.** A PR adding a feature without tests is not merged.
- **Coverage gate:** ≥ 80% per package in core (measured on `ubuntu/stable`), ≥ 90% for `router`, `validate` (package `z`), `session`, `orm`, `schema`, `auth`, `config`. `go run ./internal/tools/covergate coverage.out` checks it (also in `task cover`); `cmd/`, `internal/` and the conformance helpers (`cachetest`, `queuetest`, `storagetest`, `dbtest`) are exempt.
- **Race detector** on in CI (`-race`); Windows contributors may run without it locally (needs cgo).
- **No network, no Docker** in unit tests. Integration tests are behind `//go:build integration`.
- **Assertions:** stdlib `testing` + `github.com/google/go-cmp/cmp`. No testify in core.
- Tests use `t.Context()`, `t.TempDir()`, `t.Setenv` only where unavoidable (prefer injected `getenv`), `t.Parallel()` where safe.
- SQLite files go in `testutil.TempDir(t)` (`internal/testutil`), not `t.TempDir()`: on Windows, Defender and the indexer briefly hold just-written files, so removing the directory can fail with "directory is not empty"; the helper retries the removal for a few seconds. Close the database in a `t.Cleanup` registered after the directory is created.

## Test types

| Type | Location | Command | Notes |
| --- | --- | --- | --- |
| Unit | `*_test.go` next to code | `go test ./...` | Table-driven subtests |
| Example | `example_test.go` | `go test ./...` | Doubles as pkg.go.dev docs |
| Golden | `testdata/*.golden` | `go test ./... -update` | Generators, codemods, error pages, CLI output |
| Fuzz | `Fuzz*` | `go test -fuzz=FuzzX ./router` | Router patterns, `.env` parser, validator tags, multipart |
| Integration | `//go:build integration` | `task test:integration` | Needs `DATABASE_URL`, `MYSQL_URL`, `MARIADB_URL`, `REDIS_URL`, `MONGO_URL` |
| End-to-end | `e2e/` | `task test:e2e` | `vukem new` → build → boot → HTTP checks, on all OSes |
| Benchmarks | `Benchmark*` with `b.Loop()` | `task bench` | Router, `web.Ctx`, ORM; comparisons with net/http, chi and echo in `internal/bench` ([performance.md](performance.md)) |

Time-dependent code (sessions, rate limits, queues, scheduler) is tested with `testing/synctest` instead of sleeps.

## Toolkit for apps (`testing/` package, imported as `vukemtest`)

### HTTP client

```go
func TestCreatePost(t *testing.T) {
	app := vukemtest.New(t, bootstrap.New)            // boots app with APP_ENV=test, in-memory SQLite, migrations run
	user := vukemtest.Create(t, app, factories.User)  // ALPHA

	app.Test(t).
		ActingAs(user).
		PostForm("/posts", url.Values{"title": {"Hello"}, "body": {"World"}}).
		AssertRedirectRoute("posts.index").
		AssertSessionHas("success")

	vukemtest.AssertDatabaseHas(t, app, "posts", map[string]any{"title": "Hello"})
}
```

`vukemtest.New` fails the test on any build, boot or migration error. `vukemtest.Build(t, build, opts...)` applies the same test defaults and returns `(*vukemtest.App, error)` instead, so a test can assert that a bad configuration fails closed (`errors.Is` works on the wrapped error).

| Group | Methods |
| --- | --- |
| Requests | `Get`, `Post`, `PostJSON`, `PostForm`, `Put`, `Patch`, `Delete`, `WithHeader`, `WithCookie`, `WithSession`, `ActingAs(user, guard...)`, `HTMX()` (sets htmx headers), `Inertia()` |
| Status | `AssertOK`, `AssertCreated`, `AssertNoContent`, `AssertStatus(n)`, `AssertNotFound`, `AssertForbidden`, `AssertUnprocessable` |
| Redirects | `AssertRedirect(url)`, `AssertRedirectRoute(name, params...)` |
| Body | `AssertSee(text)`, `AssertDontSee`, `AssertJSON(v)`, `AssertJSONPath("data.title", "x")`, `AssertHeader` |
| Session | `AssertSessionHas`, `AssertSessionHasErrors("title")`, `AssertFlash(key, v)` (web and Inertia flash), `AssertValid`, `AssertInvalid("title")` |
| HTMX | `AssertHXTrigger("post-created")`, `AssertFragment("post-list")` |
| Inertia | `AssertInertia(component)` with prop and flash (`FlashEquals`, `MissingFlash`) assertions |

Requests run in-process via `httptest` (no ports). Each assertion failure prints the request, status, headers and a trimmed body.

### Database

- Default test connection: SQLite in memory, one private database per `vukemtest.New` call (unique `file:<test>_<random>?mode=memory&cache=shared` name), with the registered migrations run on it (`Options.SkipMigrations` turns that off). Isolation comes from the private database, not from transactions.
- `app.RefreshDatabase(t)` (a method on `*vukemtest.App`) drops every table and runs the migrations again (`Migrator.Fresh`), for a test that needs a clean database mid-way. There is no per-test transaction wrapper: the app's handlers run their own transactions on the shared pool, and SQLite's single writer connection would deadlock on a held outer transaction.
- Postgres: `pgtest.Database(t, adminURL)` (module `drivers/postgres`, package `pgtest`) creates an empty `pgtest_<random>` database per test from an admin URL (`TEST_DATABASE_URL`, then `POSTGRES_URL`), returns its URL for `vukemtest.Options.Env["DATABASE_URL"]` and drops it in `t.Cleanup` after terminating its connections. An unreachable server fails the test; apps opt in with `testing.Short`, an env check or a build tag.
- Assertions: `AssertDatabaseHas`, `AssertDatabaseMissing`, `AssertDatabaseCount`, `AssertSoftDeleted`.

### Fakes

Fakes replace services **on the app instance** (no globals), through the same options production code uses:

```go
outbox := mail.NewArray()
jobs := queue.NewFake()
clock := vukemtest.NewFakeClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))

app := vukemtest.New(t, bootstrap.New, vukemtest.Options{
	Clock: clock.Now,
	AppOptions: []vukem.Option{
		vukem.WithMailTransport(outbox),
		vukem.WithQueueDriver(jobs),
		vukem.WithCacheStore(cache.NewMemory(1000)),
	},
})

// ... exercise code ...

sent := outbox.SentTo("a@b.c")             // []mail.Message
invoices := queue.Dispatched[jobs.SendInvoice](jobs)
clock.Advance(24 * time.Hour)
```

| Subsystem | Fake | Notes |
| --- | --- | --- |
| Mail | `mail.NewArray()` | `Sent()`, `SentTo(addr)`, `Reset()` |
| Queue | `queue.NewFake()` | `queue.Dispatched[J](fake)`; `Work(ctx, WorkOptions{StopWhenEmpty: true})` runs jobs |
| Events | `events.NewFake()` (an `events.Dispatcher`) | `events.Dispatched[E](fake)` |
| Cache | `cache.NewMemory(n)` | LRU + TTL, same `cache.Store` contract (`cache/cachetest`) |
| Storage | `storage.NewMemory(base)` | same `storage.Disk` contract (`storage/storagetest`) |
| Rate limiting | `ratelimit.NewMemory(limit, window)` | drive time with `testing/synctest` |
| Social login | `social.Fake(name, callback, user)` | with `vukemtest.FakeSocial` |
| Password hashing | `hash.Fake()` | fast parameters |
| Clock | `vukemtest.NewFakeClock(t0)` | `Now`, `Advance`, `Set`; pass `clock.Now` as `vukemtest.Options.Clock`; sessions (cookie, `memory`, `database`) expire by it |

## CI matrix

| Job | Matrix |
| --- | --- |
| unit | `ubuntu`, `windows`, `macos` × Go `stable` (`oldstable` joins once Go 1.28 ships, since 1.27 is the minimum) |
| integration | `ubuntu` + services `postgres:17`, `mysql:8.4`, `mariadb:11.8`, `redis:7`, `mongo:8` |
| e2e | `ubuntu`, `windows`, `macos` × `stable` |
| coverage | `ubuntu/stable` → Codecov, gate enforced by `internal/tools/covergate` |
