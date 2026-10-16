---
name: vukem-testing
description: Writing tests for a Vukem app with vukemtest (fluent HTTP client, fakes, database assertions). Use when adding features, fixing bugs or reviewing test coverage.
---

# Vukem testing

- Write the failing test first; run one package with `go test ./tests/...` or one test with `-run`.
- `vukem make:test Posts` scaffolds a test using the fluent client.
- Boot the app with `vukemtest.New(t, bootstrap.New)`: test environment, in-memory SQLite, migrations applied.
- Requests: `app.Test(t).ActingAs(user).PostForm("/posts", values).AssertRedirectRoute("posts.index")`; JSON with `PostJSON`, then `AssertStatus`, `AssertJSON…`.
- Database: `vukemtest.AssertDatabaseHas(t, app, "posts", map[string]any{"title": "Hello"})`; build rows with factories.
- Fakes instead of network: `vukemtest.FakeMail`, `FakeQueue`, `FakeEvents`, `FakeClock`; assert with `AssertSent`, `AssertPushed`, `AssertDispatched`.
- Tests use the standard library and go-cmp; no testify, no network, no `time.Sleep`.
