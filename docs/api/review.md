# Public API review (B.5.1)

Pre-freeze review of every exported identifier of the core module and of the published modules (`drivers/*`, `contrib/*`), before `v1.0.0` locks them under the [stability policy](../stability.md). The machine-checked side is the [apidiff report](apidiff-v0.20.1-to-v1.0.0.md); the [API compatibility gate](../tooling.md#api-compatibility) keeps the result frozen.

## Method

- Inventory: a throwaway `go/ast` program listed the exported functions, types, constants, variables and methods of exported types in every non-internal package (61 packages, 2066 identifiers; struct fields not counted) and every selector that references them anywhere in the repository, including `internal/cli` scaffolds and stubs (`*.tmpl`), examples, docs and the site.
- Heuristics flagged: identifiers never referenced outside their package, stutter (`pkg.PkgThing`), `Get` prefixes, mixed-case acronyms, `context.Context` not first, exported mutable variables, option constructors not named `With*`, and `context.Background()` in library code (missing ctx).
- Each flag got a decision. Before 1.0 only what cannot be fixed compatibly later was changed: renames keep a deprecated alias marked `//go:fix inline`, so `go fix ./...` migrates callers; removals are limited to mutable globals and identifiers exported by accident. Additive improvements are left for 1.x minors.

## Conventions the review confirmed

These are the rules the "keep" decisions below rely on; new API follows them.

- **Constructors:** `New` for a package's primary type, `NewX` when a package builds several (`mail.NewSMTP`, `cache.NewMemory`).
- **Options:** constructor options are `With*` (`vukem.WithRoutes`, `database.WithDriver`, `s3.WithClock`). Per-call modifiers passed to a single call read as a DSL and are bare nouns: `queue.Delay`, `api.Sorts`, `inertia.Defer`, `web.Partial`, `graphql.Cost`, `websocket.PingInterval`, `sse.Heartbeat`, `auth.Remember`.
- **Primary types** may repeat the package name, as `template.Template` does: `cache.Cache`, `queue.Queue`, `schema.Schema`, `session.Session`, `inertia.Inertia`, `devtools.Devtools`, `vite.Vite`, `social.Social`.
- **Context helpers** come in pairs: `WithX(ctx, v)` and `XFrom(ctx)` / `X(ctx)` (`web.WithSession`/`web.SessionFrom`, `vite.NewContext`/`vite.FromContext`, `web.WithIssueLocalizer`/`web.IssueLocalizer`).
- **Escape hatches** return the underlying client (`database.DB.SQL`, `redis.Client.Redis`, `mongo.Client.Raw`, `websocket.Conn.Unwrap`). Third-party types in a driver's API tie that driver module, not the core, to the dependency's major version.
- **Test helpers** live in `vukemtest`, the `*test` conformance packages and explicit fakes (`hash.Fake`, `social.Fake`, `queue.NewFake`, `events.NewFake`, `stripe.SignForTest`); they are public on purpose.

## Packages

Exported identifiers at review time (functions, types, constants, variables and methods of exported types).

| Package | Exported | Public surface |
| --- | --- | --- |
| `vukem` | 104 | App kernel: `New` + `With*` options, lifecycle (`Boot`, `Run`, `Shutdown`), lazy service accessors, commands, providers, runners, health checks, `Main`/`RunMain` for `cmd/` |
| `admin` | 49 | Admin panel builder: `Panel`, `Resource` DSL, ability/action/input constants used by resource definitions |
| `api` | 24 | Typed JSON handlers (`Register`, `Op`), envelopes and pagination (`Collection`, `Page`, `Cursor`), query options, idempotency and deprecation middleware |
| `apidocs` | 7 | Scalar API reference page: `Serve`, `Register`, `Command`, pinned asset hash and CSP |
| `auth` | 49 | Guards (session, token), user providers, tokens, remember-me store, request helpers (`User`, `Required`, `Guest`, `Verified`) |
| `auth/hash` | 10 | `Hasher` interface, argon2id/bcrypt constructors, `Verify` for both formats, `DefaultArgon2Params` |
| `auth/jwt` | 38 | Signing/verification per algorithm, JWKS (static, remote, published), JWT guard, rotating refresh tokens |
| `auth/totp` | 14 | TOTP codes, secrets, `otpauth://` URIs and QR, recovery codes |
| `authz` | 18 | `Gate`, policies, `Result` (allow, deny, deny as 404) |
| `broadcast` | 37 | Channel server (public, private, presence), subscriptions, SSE transport, listener adapters |
| `cache` | 37 | `Cache` facade, `Store` interface, memory and database stores, locks, generic `Get`/`Remember` |
| `cache/cachetest` | 1 | `Run`: store conformance suite for drivers |
| `config` | 30 | Env loading (`Load`, `Source`), `Secret`, `Key`, struct filling, environment names |
| `contrib/graphql` | 99 | Code-first schema builder, execution and limits, persisted queries, dataloader, subscriptions over WebSocket, error codes of the GraphQL and graphql-ws specs |
| `contrib/grpc` | 14 | Connect handlers on the router: `Mount`, error mapping, health, opt-in reflection, validation, auth middleware |
| `contrib/grpc/grpctest` | 5 | In-process HTTP/2 test servers |
| `contrib/stripe` | 45 | Minimal Stripe client mirroring Stripe's verbs (`Create*`, `Get*`, `Update*`, `Cancel*`), webhook verification and handler, typed errors |
| `crypt` | 15 | AES-GCM `Encrypter` with key rotation, sealed strings and encrypted cookies |
| `csrf` | 5 | Middleware, token accessor, field and header names |
| `database` | 44 | `DB`/`Tx`/`Executor`, dialects, drivers, connection `Manager`, query observer, `RunInTx` |
| `database/dbtest` | 1 | `Run`: dialect conformance suite for drivers (fixtures now unexported) |
| `devtools` | 33 | `/_vukem` dashboard: recorder, its record types and observer hooks |
| `drivers/mongo` | 22 | Typed collections (`Collection[T]`, `Coll[T]`), `Open`, raw escape hatches |
| `drivers/mysql` | 3 | `Driver`, `DSN`, URL error |
| `drivers/otel` | 11 | Tracing provider, middleware, propagator option |
| `drivers/postgres` | 1 | `Driver` |
| `drivers/redis` | 37 | Client with cache, session, queue and rate-limit adapters, pub/sub |
| `drivers/s3` | 26 | S3 `Disk` with presigning and multipart limits |
| `drivers/websocket` | 45 | `Serve`/`Handle`/`Handler`, `Conn`, per-call options, broadcast protocol frames, close codes |
| `errs` | 34 | `HTTPError`, sentinel HTTP errors, `ValidationError`, stack capture |
| `events` | 24 | `Bus`, typed listeners (`On`, `OnAsync`, `OnQueued`), fake |
| `factory` | 30 | Model factories and the deterministic `Faker` |
| `htmx` | 39 | Request detection, response headers, out-of-band and `hx-partial` helpers, header names |
| `httpclient` | 6 | SSRF-safe client (`Safe`, `Allowed`, `Get`) and its errors |
| `i18n` | 30 | Bundles, locale matching, plural categories, middleware, context helpers |
| `inertia` | 61 | Inertia v2 protocol: render, prop options (defer, merge, once, scroll), history, SSR, header names |
| `logging/telegram` | 14 | `slog.Handler` that forwards records to Telegram |
| `mail` | 38 | `Mailable`, `Attachable`, `Mailer`, transports (SMTP, Resend, log, array), preview |
| `middleware` | 25 | Standard `func(http.Handler) http.Handler` middleware and their configs |
| `openapi` | 23 | OpenAPI 3.1 document generation, response validation, TypeScript/Zod and Go clients |
| `orm` | 110 | Generic `Query[T]`, writes, relations, hooks, soft deletes, JSON/encrypted columns, rules |
| `queue` | 75 | `Queue`, drivers (sync, memory, database), job options, worker, failed jobs, fake |
| `queue/queuetest` | 2 | Driver conformance suite |
| `ratelimit` | 9 | `Limiter`, memory store, key functions, middleware |
| `router` | 58 | `Router`/`Group`/`Route`, resources and their handler interfaces, URL generation |
| `schedule` | 34 | Cron parser, `Schedule`, fluent `Task` frequencies, overlap control |
| `schema` | 55 | Migration DSL (`Schema`, `Table`, `Column`), `Migrator` |
| `security` | 1 | `Event`: dependency-free security audit record shared by packages that emit it |
| `session` | 40 | `Manager`, `Session`, stores (cookie, memory, SQL) |
| `signed` | 7 | Signed, expiring URLs and their middleware |
| `social` | 49 | OAuth2/OIDC login, built-in providers, fake provider |
| `sse` | 21 | `Serve`, `Stream`, `Hub` with a pluggable `Broker`, shutdown signal |
| `storage` | 28 | `Disk` interface, local and memory disks, upload rules, temporary URLs, file handler |
| `storage/storagetest` | 2 | Disk conformance suites (incl. path traversal) |
| `upload` | 40 | Streaming multipart `Receive`, rules, virus scanning, direct-to-storage presign, tus |
| `view` | 7 | `html/template` engine with layouts and blocks |
| `vite` | 11 | Manifest and dev-server tags, React refresh, context helpers |
| `vukemtest` | 71 | Test app, HTTP client, response assertions, database assertions, fake clock |
| `web` | 120 | `Ctx`, `HandlerFunc`, binding and validation, rendering, errors, sessions and services seams |
| `webhook` | 4 | HMAC signing and verification middleware |
| `z` | 174 | Zod-like schemas, issues and codes, struct tags, JSON Schema export |

## Findings

| Identifier | Issue | Decision | Commit |
| --- | --- | --- | --- |
| `orm.BeforeCreater`, `orm.AfterCreater` | naming: not English, and `router.Creator` spells it right | rename to `BeforeCreator`, `AfterCreator`; deprecated aliases with `//go:fix inline` | `refactor(orm): rename create hook interfaces to BeforeCreator and AfterCreator` |
| `mail.WithAttachments` | naming: `With*` is reserved for options, it reads like one at the call site | rename to `mail.Attachable` (pairs with `Mailable`); deprecated alias with `//go:fix inline` | `refactor(mail): rename WithAttachments to Attachable` |
| `hash.DefaultArgon2` | exported mutable variable: assigning it weakens `hash.Default` process-wide | remove; `hash.DefaultArgon2Params()` returns a copy, pinned by `TestSecurity_A04_Argon2Defaults` | `refactor(hash)!: replace DefaultArgon2 var with DefaultArgon2Params` |
| `dbtest.Author`, `dbtest.Book`, `dbtest.Meta`, `dbtest.Tag` | exported by accident: fixtures of the conformance suite, unused by every driver, starter and example | unexport (`author`, `book`, `meta`, `tag`: the ORM derives `author_id`, `book_id`, `tag_id` from the type name) | `refactor(dbtest)!: unexport the conformance fixture models` |
| `database.SQLite`, `database.Postgres`, `database.MySQL` | exported mutable variables | keep: `Dialect` values of empty structs, the `binary.LittleEndian` pattern; used across docs and scaffolds | - |
| `session.NewSQLStore(*sql.DB, ...)` with `WithDollarPlaceholders`, `WithMySQL` | inconsistent signature: other database stores take `*database.DB` and read its dialect | keep: a constructor taking `*database.DB` can be added in a 1.x minor without breaking this one | - |
| `App.DB`, `App.Cache`, `App.Queue`, `App.Sessions`, `App.Mailer`, `App.Migrator` | missing ctx | keep: memoized lazy accessors shared by every caller, so no single caller's context may cancel their setup | - |
| `schema.New(ctx, ex)` | ctx stored in a struct | keep: `Schema` is a short-lived builder for one migration step, like `http.Request` carrying its context | - |
| `session.Session.GetString`, `GetInt64`; `stripe.Client.Get*`; `vukemtest.Client.GetJSON` | naming: `Get` prefix | keep: typed variants of `Session.Get`; Stripe's own verbs; HTTP `GET` | - |
| `websocket.AllowedOrigins`, `PingInterval`, `PingTimeout`, `ReadLimit`, `Subprotocols`, `WriteTimeout`, `InsecureSkipOriginCheck`; `sse.Heartbeat` | naming: options without `With` | keep: per-call options of `Serve`/`Handle`/`Handler` (convention above) | - |
| `graphql.MaxBatch`, `BatchWait`, `Cost`; `queue.Delay`, `Retries`, `Timeout`, `OnQueue`; `api.Fields`, `Filters`, `Includes`, `PerPage`, `Sorts`; `web.Block`, `web.Partial`; `auth.Remember` | naming: options without `With` | keep: per-call modifiers | - |
| `cache.Cache`, `queue.Queue`, `schema.Schema`, `session.Session`, `inertia.Inertia`, `devtools.Devtools`, `vite.Vite`, `social.Social` | stutter | keep: primary type of the package | - |
| `telegram.NewHandler` | naming: `New` expected for a single type | keep: matches `slog.NewTextHandler` and `slog.NewJSONHandler` | - |
| `redis.Client.Redis`, `mongo.Client.Raw`, `mongo.Coll.Raw`, `websocket.StatusCode` and `Status*`, `grpc` connect types, `otel.Provider.TracerProvider` | third-party types in the public API | keep: deliberate escape hatches; drivers are separate modules, so a dependency major bump means a driver major, never a core one | - |
| `web.IssueLocalizer`, `web.BodyLimitOf` | should be internal? (no use outside `web`) | keep: getter halves of `WithIssueLocalizer` and `LimitBody`, needed by custom input handling | - |
| `web.EventDispatcher`, `web.ValidationHook`, `web.WithValidationHook`, `web.WithIssueLocalizer`, `web.Services` | exported for framework wiring | keep: seams the core and `i18n` use across package boundaries; documented | - |
| `database.NewManager`, `factory.NewFaker`, `openapi.NewSchemas`, `i18n.Canonical`, `i18n.WithBundle`, `inertia.IsPrefetch`, `vite.NewContext`, `graphql.Explorer` | should be internal? (no use in the repository) | keep: constructors and helpers for app code; lack of in-repo callers is not an accident | - |
| `stripe.SignForTest`, `vukemtest.FakeSocial`, `hash.Fake`, `social.Fake`, `queue.NewFake`, `events.NewFake` | test helpers in production packages | keep: public on purpose for app tests | - |
| `queue.Inline` and `queue.Sync.Inline` | redundant? | keep: marker interface the queue uses to detect inline drivers | - |
| `auth.TokenCan` and `auth.Token.Can` | redundant? | keep: `TokenCan` resolves the token from the request and guard, `Token.Can` checks a loaded token | - |
| `router.Wrap` and `router.Adapt` | redundant? | keep: opposite directions (`http.Handler` to `web.HandlerFunc` and back) | - |
| `router.Middleware` and `middleware.Middleware` | duplicate type | keep: both are aliases of `func(http.Handler) http.Handler`, so no conversion is ever needed | - |
| `htmx.Partial`, `htmx.IsPartial` and `web.Partial`, `web.Ctx.IsPartial` | redundant? | keep: `htmx` works on `*http.Request` and HTML, `web` on `Ctx` rendering | - |
| `api.Op` and `api.Operation` | inconsistent naming? | keep: `Op` is the user's description, `Operation` the generated record on `router.RouteInfo.Doc` | - |

No mixed-case acronyms and no exported function with `context.Context` in a position other than first were found.

**Decisions:** 24 findings — 2 rename (3 identifiers), 1 remove, 1 unexport (4 identifiers), 20 keep.

## Breaking changes

Both are listed in the [1.0 upgrade guide](../upgrade/1.0.md):

- `hash.DefaultArgon2` removed: use `hash.DefaultArgon2Params()`.
- `dbtest.Author`, `dbtest.Book`, `dbtest.Meta`, `dbtest.Tag` unexported: drivers only call `dbtest.Run`, which is unchanged.

## apidiff review

[apidiff-v0.20.1-to-v1.0.0.md](apidiff-v0.20.1-to-v1.0.0.md) compares the core with `v0.20.1` and each published module with its latest tag. Every entry is accounted for above:

- Core: 5 incompatible changes (the two breaking changes), 4 compatible additions (`DefaultArgon2Params`, `Attachable`, `BeforeCreator`, `AfterCreator`). The deprecated aliases do not appear because their names and types are unchanged.
- `drivers/*` and `contrib/*`: no API changes since their tags.

Regenerate it with `task api:check -- -major v0 -head v1.0.0 -report docs/api/apidiff-v0.20.1-to-v1.0.0.md`.
