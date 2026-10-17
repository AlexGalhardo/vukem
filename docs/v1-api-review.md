# v1.0 API review (A11)

Second pre-freeze review of the exported API, done on `v0.25.0` before `v1.0.0` locks it under the [stability policy](stability.md). The [first review](api/review.md) (B.5.1, on `v0.20.1`) set the naming conventions and is not repeated here; this one covers the surface added since (`v0.21.0`–`v0.25.0`), re-checks the whole core module mechanically, and prepares the [`apidiff` gate](tooling.md#api-compatibility) for the `v1` tags. Drivers and contrib modules were checked briefly (doc comments, panics, deprecations).

## Method

- **Doc comments:** a throwaway `go/ast` scanner listed every exported function, method of an exported type, type, constant and variable whose doc comment is missing or does not start with its name (grouped `const`/`var` blocks documented by the group comment are accepted, as in `go doc`). It also listed every exported function or method that calls `panic` directly, every `Must*` and `New*` function and every exported variable. It walked the core module and every `drivers/*` and `contrib/*` module, skipping `internal/`, `testdata/`, examples and tests.
- **Lint:** golangci-lint ran with revive's `exported` and `package-comments` rules unmasked (see [the gate](#doc-comment-gate)).
- **New surface:** `apidiff -m` compared the core at `v0.20.1` with `v0.25.0`: 9 incompatible changes (all already shipped and documented in the [1.0 upgrade guide](upgrade/1.0.md) or the changelog) and about 320 additions, each read for naming against the [first review's conventions](api/review.md#conventions-the-review-confirmed).
- **Modules:** `task api:check -- -major v0 -report …` compared every module with its latest `v0` tag. Before the removals below: no change in any module since its tag. After them: 3 incompatible changes in the core (the removed aliases), none elsewhere.

## Doc comments

The scanner found 12 exported identifiers whose doc comment `go doc` renders badly or not at all. All are fixed in this branch; none needed an API change.

| Identifier | Issue | Fix |
| --- | --- | --- |
| `queue.JobDispatched`, `queue.JobProcessed`, `queue.JobFailed` | doc started with `Name:` instead of a sentence | rewritten as `JobX reports that …` |
| `upload.ErrFileTooLarge`, `ErrFieldTooLarge`, `ErrTooManyFields`, `ErrNotMultipart` | same | rewritten as `ErrX reports …` |
| `social.ErrUnknownProvider` | doc was a note about the whole group (`Their Key …`) | note moved to the group comment, so `ErrUnknownProvider`, `ErrState` and `ErrAccessDenied` share it |
| `mail.ResendError.Error`, `stripe.Error.Error` | no doc comment | documented |
| `drivers/s3.MinPartSize`, `i18n.SourceHeader` | trailing comment only | keep: the group comment documents the block |

Every package has a `// Package x` comment. Struct fields and interface methods were not scanned: `go doc` shows them inside their type, and they are reviewed with it.

### Doc-comment gate

`.claude/rules/go.md` requires a doc comment starting with the name on every exported identifier, but the `comments` exclusion preset in `.golangci.yml` silently dropped revive's `exported` and `package-comments` findings, so nothing enforced it. This branch removes the preset; the only finding it exposed was the starter package `internal/cli/starter/routes`, whose package comment lives in its `.go.tmpl` files and is now excluded by path. A missing or misnamed doc comment on new API now fails `task lint`. revive accepts `Name: …`, so the scanner remains the stricter check for a later review.

## Naming

The additions since `v0.20.1` follow the conventions, with these notes:

| Identifiers | Observation | Decision |
| --- | --- | --- |
| `vukem.APIMiddlewareWith`, `DefaultMiddlewareWith`, `ServeOpenAPIWith` | `XWith(opts)` suffix, not a `With*` option | keep: the options-struct variant of `APIMiddleware`, `DefaultMiddleware`, `ServeOpenAPI`, read like `signal.NotifyContext`; new variants follow the same suffix |
| `vukem.WithCacheStoreFunc`, `WithJobsFunc`, `WithQueueDriverFunc`, `WithSessionStoreFunc` | `With*Func` lazy variants of `With*` options | keep: consistent |
| `queue.Queue.SetClock`, `queue.Database.SetClock`, `queue.Memory.SetClock` vs `s3.WithClock`, `orm.WithClock` | setters next to `With*` options for the same concern | keep: `Set*` is the convention for wiring a long-lived object after construction (`database.DB.SetQueryObserver`, `events.Bus.SetQueue`, `App.SetSessionStore`); the queue clock behaviour itself is tracked separately (rifa) |
| `database.DB.SetQueryObserver`/`AddQueryObserver`, `queue.Queue.SetObserver`/`AddObserver`, `events.Bus.SetObserver` | `Set` replaces, `Add` chains | keep: same pair everywhere; `Query` names what a database observer sees |
| `signed.Signer.WithPreviousKeys`, `social.Social.WithPreviousKeys` | `With*` method, not an option | keep: a fluent builder that configures and returns the receiver before first use (added with key rotation); it mutates the receiver rather than copying it like `http.Request.WithContext`, and its doc says to call it before first use |
| `auth.SetUser` | `Set*` function where context helpers are `WithX` | keep: it writes the request's `web.Ctx` cache (pairs with `auth.User`), it does not derive a `context.Context` |
| `config.MinKeyLen`, `signed.MinKeyLen`, `social.MinKeyLen` | repeated constant | keep: each package documents its own minimum (all 32 bytes today) |
| `session.StoredSession`, `session.Info` | mild stutter | keep: `StoredSession` is the store record, `Info` the list item |
| Error sentinels and types | every exported error variable is `Err*`, every error type ends in `Error` (`errname` enforces it) | - |
| Constructors | `New` for the primary type, `NewX` for the others (`ratelimit.NewMemory`, `ratelimit.NewWindow`, `webhook.NewSender`, `webhook.NewSQLDeliveryLog`) | - |

No exported mutable variable was added: the only exported non-error variables are still `database.SQLite`, `database.Postgres` and `database.MySQL` (kept in the first review).

## Panics vs errors

Every exported function or method that panics on misconfiguration or misuse (as opposed to re-panicking a recovered panic). "Error variant" is the call that reports the same condition as an error.

| Function | Panics when | Error variant | Decision |
| --- | --- | --- | --- |
| `uuid.MustParse` | invalid UUID | `uuid.Parse` | keep: `Must*` + error pair |
| `schedule.MustParseCron` | invalid expression | `schedule.ParseCron` | keep: `Must*` + error pair |
| `schedule.Task.Cron`, `Every`, `HourlyAt`, `DailyAt`, `WeeklyOn`, `MonthlyOn` | invalid expression, duration, time or day | `schedule.ParseCron` for expressions | keep: fluent DSL evaluated at boot; panics now documented on each method |
| `i18n.Middleware` | `MiddlewareConfig.Validate` fails | `i18n.NewMiddleware` | **proposal B2**: the only middleware constructor that panics; `csrf.Middleware`, `middleware.CORS`, `webhook.HMAC` and `webhook.Standard` return `(middleware, error)` |
| `router.Router.ServeHTTP` | the routes fail to compile | `router.Router.Handler` | keep: `App` calls `Handler` at boot; documented |
| `api.Register` | malformed `"METHOD /path"`, `ValidateResponse` on a non-struct output | none | keep: route registration panics like `http.ServeMux.Handle`; now documented |
| `jwt.NewGuard` | `Verifier.Err()` reports a misconfiguration | `Verifier.Err` | keep: documented; the check can run before the guard is built |
| `auth.NewSessionGuard` | `*U` implements `SessionVersioner` but `U` does not | none | keep: a type error caught at boot, documented |
| `signed.New`, `social.New`, `signed.Signer.WithPreviousKeys`, `social.Social.WithPreviousKeys` | a key shorter than `MinKeyLen` | none | keep for v1; an additive error-returning constructor can come in a 1.x minor if apps need it (proposal B4) |
| `config.Key.Derive` | zero key, or `n` above the HKDF limit | `App.DeriveKey` | keep: documented; `IsZero` checks first |
| `crypt.Encrypter.Seal` | nil `Encrypter` (no `APP_KEY`) | `crypt.New` reports the missing key | keep: documented |
| `database.Tx.AfterCommit` | `Tx` not started by `DB.Tx` | none | keep: programmer error, documented |
| `z.Enum`, `z.EnumOf` | no values | none | keep: schema DSL; now documented |
| `z.Struct`, `z.FromTags`, `z.For` | `T` is not a struct, unknown tag rule, key without field, type mismatch, recursion | none | keep: schema DSL evaluated at boot; `For` now documents it |
| `z.SliceSchema.Default`, `z.StringSchema.Preprocess` | default of the wrong type, nil func | none | keep: documented (`Default` now) |
| `orm` generics (`QueryOf`, `Create`, `TableOf`, `FieldsOf`, …) | invalid model (not a struct, bad table or column name, no primary key, relation without foreign key, unknown tag option) on first use | none | keep: now documented in the package comment |

Drivers and contrib modules panic only to re-raise recovered panics (`drivers/otel` middleware). `export` panics with `http.ErrAbortHandler` to abort a broken stream, which `net/http` handles.

Rule for new API: constructors and middleware that can be misconfigured by runtime input (environment, files) return an error; DSLs evaluated once at boot with literal arguments (routes, schedules, schemas, models) may panic, and their doc comment says when. Where both forms are useful, the panicking one is `MustX` next to `X` returning an error.

## Deprecated aliases

| Identifier | Deprecated in | Releases since | Decision |
| --- | --- | --- | --- |
| `orm.BeforeCreater`, `orm.AfterCreater` | `v0.21.0` (`//go:fix inline`) | 5 (`v0.21.1`–`v0.25.0`) | removed in this branch |
| `mail.WithAttachments` | `v0.21.0` (`//go:fix inline`) | 5 | removed in this branch |
| `stripe.Client.Do` | core `v0.25.0` changelog; no `contrib/stripe` tag contains it yet (latest `contrib/stripe/v0.2.0`) | 0 | keep for now: **proposal B1** |

No other `Deprecated:` comment exists outside generated code. The [1.0 upgrade guide](upgrade/1.0.md) now tells apps to run `go fix ./...` on `v0.21`–`v0.25` before bumping, since the aliases are gone in `v1.0.0`.

Any alias still present when `v1.0.0` is tagged stays for all of `v1` ([stability policy](stability.md#deprecation)): removing it later needs `/v2`.

## The v1 gate

`internal/tools/apicheck` already implements the gate; nothing in it needs to change for `v1.0.0`. How it works once the tags exist:

1. **Before tagging:** run `task api:check -- -major v0 -head v1.0.0 -report docs/api/apidiff-v0.25.0-to-v1.0.0.md` on the release commit and check that every incompatible entry is listed in the [1.0 upgrade guide](upgrade/1.0.md). Commit the report with the release.
2. **Tag every published module together:** `v1.0.0` for the core and `drivers/<name>/v1.0.0`, `contrib/<name>/v1.0.0` for each nested module, after their `go.mod` files require core `v1.0.0` (release order in [tooling.md](tooling.md#versioning)). A module without a `v1` tag is skipped with a `::notice`, so it is not protected until its first `v1` tag (proposal B5).
3. **After tagging:** the `apidiff` job (`go run ./internal/tools/apicheck -major v1`) compares each module's working tree with its highest stable `v1.x.y` tag (pre-releases and build metadata ignored), both type-checked with `GOWORK=off`, and fails with one `::error` per incompatible identifier. The baseline moves forward automatically: each new `v1` tag becomes the comparison base, so there is no baseline file to update.
4. **Renames** in `v1` keep the old name as a deprecated alias or wrapper with `//go:fix inline`; apidiff sees no change. Additions are always allowed.
5. **`/v2`:** a major bump changes the module path, so the gate is run with `-major v2` for that module once `v2.0.0` exists; the CI step hard-codes `-major v1` and must be updated then.

Known limits, recorded so the owner can decide (none blocks `v1.0.0`):

- **No exception list.** The [stability policy](stability.md#what-is-not-covered) lets security fixes break an unsafe API in a minor, but the gate cannot tell them apart; such a change must be merged by an admin over the failed check, with the reason in the PR and the changelog's **Security** section. An allowlist file (`docs/api/exceptions.txt`, checked by `apicheck`) could replace that (proposal B6).
- **Deleted modules are not reported.** Modules are discovered from the working tree, so deleting a published module's directory passes the gate.
- **Behaviour is not checked.** apidiff only sees types and signatures; documented behaviour stays a review responsibility.

## Proposed breaking changes (owner decision)

None of these is applied in this branch.

| ID | Change | Why | Cost | Recommendation |
| --- | --- | --- | --- | --- |
| B1 | Remove `stripe.Client.Do` (deprecated for `Call`) before `contrib/stripe/v1.0.0` | otherwise it is frozen for all of `v1` | the "two releases" rule is not met: no stripe tag ships the deprecation; apps on `Do` must run `go fix` first | remove at `v1.0.0`, after one `contrib/stripe` `v0` release that carries the deprecation |
| B2 | `i18n.Middleware` returns `(func(http.Handler) http.Handler, error)`; `NewMiddleware` becomes a deprecated `//go:fix inline` wrapper | the only panicking middleware constructor; `csrf.Middleware` and `middleware.CORS` already return errors | breaks apps calling `i18n.Middleware` directly (in the repository: `docs/subsystems.md` and an example test; the `App` wiring does not use it) | apply before `v1.0.0` |
| B3 | `middleware.SecureHeaders` returns an error instead of serving a stricter policy when `Validate` fails | consistency with `CORS` | breaks every app wiring it by hand; the current fail-stricter behaviour is safe | keep; no change |
| B4 | `signed.New` and `social.New` return an error | runtime keys come from `APP_KEY` | breaks callers; `App.DeriveKey` already fails on a missing key before them | keep; add error-returning constructors in a 1.x minor only if needed (additive) |
| B5 | Decide which nested modules get `v1.0.0`: `contrib/imaging` (`v0.1.0`), `drivers/mysql` (`v0.1.1`) and `drivers/websocket` (`v0.1.1`) have had one release | a `v1` tag freezes them | staying on `v0` leaves them outside the gate, documented as experimental | tag all modules `v1.0.0` together unless one has known API debt |
| B6 | Add an exception list to `apicheck` for security-driven breaks | the gate has no sanctioned override | new tooling, small | add in a 1.x minor when the first case appears |
| B7 | Merge `queue.*.SetClock` into a `queue.WithClock` option | one way to set a clock | breaks tests that call `SetClock`; owned by the queue work in progress | keep for v1 |

## Commits

- `docs(queue)`, `docs(upload)`, `docs(social)`, `docs(mail)`, `docs(stripe)`: doc comment fixes.
- `docs(api)`, `docs(z)`, `docs(schedule)`, `docs(orm)`: document panics.
- `refactor(orm)!`, `refactor(mail)!`: remove the deprecated aliases; `docs(upgrade)`: migration note.
- `build(lint)`: enforce exported doc comments with revive.
