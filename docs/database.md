# Databases

Connections, the generic query builder, models, relations, migrations, factories and seeders. Decisions `D1`–`D5` in [brainstorming.md](brainstorming.md).

## Support matrix

| Database | Driver | Module | Phase |
| --- | --- | --- | --- |
| SQLite | `modernc.org/sqlite` (pure Go) | core | Prototype |
| PostgreSQL | `jackc/pgx/v5/stdlib` | `drivers/postgres` | ALPHA |
| MySQL 8 / MariaDB 11 | `go-sql-driver/mysql` | `drivers/mysql` | BETA |
| Redis | `redis/go-redis/v9` | `drivers/redis` | BETA |
| MongoDB | `go.mongodb.org/mongo-driver/v2` | `drivers/mongo` | BETA |

## Connections

```go
// config/database.go
type Database struct {
	Default     string `env:"DB_CONNECTION" default:"sqlite"`
	URL         string `env:"DATABASE_URL"  default:"file:storage/database.sqlite"`
	MaxOpen     int    `env:"DB_MAX_OPEN"   default:"10"`
	MaxIdle     int    `env:"DB_MAX_IDLE"   default:"5"`
	MaxLifetime time.Duration `env:"DB_MAX_LIFETIME" default:"30m"`
}
```

- `database.Manager` holds named connections (`app.DB.Conn("analytics")`); the default one is `app.DB.Default()`.
- Each connection is a `*database.DB` wrapping `*sql.DB` plus its `Dialect`; `db.SQL()` exposes the raw `*sql.DB` (sqlc, other libraries).
- Query observers (`database.QueryObserver`) see every statement run through a `DB` or its transactions (SQL, args, duration, error). `database.WithQueryObserver` can be passed several times and `db.AddQueryObserver` appends: all of them are notified in order. `db.SetQueryObserver` replaces them all. `vukem.WithQueryObservers(obs...)` adds observers to `app.DB()` next to devtools.
- Postgres and MySQL register their dialect when the user imports `drivers/postgres` or `drivers/mysql` and passes `postgres.Driver()` / `mysql.Driver()` to the database provider — explicit, no `init()`.

### PostgreSQL

```sh
go get github.com/AlexGalhardo/vukem/drivers/postgres
```

```go
// bootstrap/app.go
vukem.New(append([]vukem.Option{
	vukem.WithDatabaseDrivers(postgres.Driver()), // postgres:// and postgresql:// URLs
	// ...
}, opts...)...)
```

```sh
DATABASE_URL=postgres://app:secret@localhost:5432/app?sslmode=disable
```

Everything above the driver is dialect-neutral: the same models, queries, migrations and `SESSION_DRIVER=database` work on SQLite, Postgres and MySQL. `database/dbtest.Run(t, db)` is the conformance suite every dialect must pass (schema, migrations, ORM, transactions, database queue); `drivers/postgres` runs it against `postgres:17` and `drivers/mysql` against `mysql:8.4` and `mariadb:11.8`, with `task services:up` and `go test -tags=integration`. Each top-level subtest gets a context cancelled after `dbtest.SubtestTimeout` (2 minutes), so a stuck lock or a slow server fails that subtest instead of hanging the run.

### MySQL and MariaDB

```sh
go get github.com/AlexGalhardo/vukem/drivers/mysql
```

```go
// bootstrap/app.go
vukem.New(append([]vukem.Option{
	vukem.WithDatabaseDrivers(mysql.Driver()), // mysql:// and mariadb:// URLs
	// ...
}, opts...)...)
```

```sh
DATABASE_URL=mysql://app:secret@localhost:3306/app
DATABASE_URL=mysql://app:secret@db.internal:3306/app?tls=preferred
```

`mysql.DSN` turns the URL into a go-sql-driver DSN (percent-encode special characters in the password). Query parameters go to the driver unchanged and override these defaults:

| Parameter | Default | Why |
| --- | --- | --- |
| `parseTime`, `loc`, `time_zone` | `true`, `UTC`, `'+00:00'` | `DATETIME` scans into `time.Time`, always in UTC |
| `charset` | `utf8mb4` | Full Unicode |
| `timeout` | `10s` | Dial timeout; per-query limits come from `context.Context` |
| `clientFoundRows` | `true` | `UPDATE` counts matched rows, as on SQLite and Postgres (`orm.Update` relies on it) |
| `tls` | `false` for `localhost`/loopback, otherwise `true` (verified certificate) | Secure by default; set `tls=preferred`, `skip-verify` or a registered config name explicitly (e.g. a Docker network host) |

One `mysql` dialect covers MySQL 8.0.16+ and MariaDB 10.6+ (tested on MySQL 8.4 and MariaDB 11.8), so it uses only SQL both accept:

- Inserts read the id from `LastInsertId` (MySQL has no `RETURNING`); upserts use `ON DUPLICATE KEY UPDATE … VALUES()`; the database queue reserves jobs in a transaction with `FOR UPDATE SKIP LOCKED`.
- Column types: `BIGINT AUTO_INCREMENT` ids, `DATETIME(6)` timestamps, `JSON`, `CHAR(36)` UUIDs, `LONGTEXT` text, `LONGBLOB` binary, `VARCHAR(255)` enums with a `CHECK`. Text, JSON and binary defaults become expression defaults (`DEFAULT ('x')`).
- Foreign keys added with `Table` are a separate `ADD FOREIGN KEY` clause (MySQL ignores inline `REFERENCES`); `DropIndex` names the table; `migrate:fresh` drops tables with foreign key checks off on one connection.
- String comparisons follow the column collation, which is case-insensitive by default (`utf8mb4_0900_ai_ci`): `Where("email", x)` and unique indexes ignore case, and `ILIKE` is sent as `LIKE`. Session, cache and queue tables use binary keys so ids always compare exactly.
- DDL commits implicitly, so a failed migration is not rolled back as it is on SQLite and Postgres; keep migrations small. `Text` columns cannot be `Unique` or indexed (use `String`).

### SQLite defaults

Applied on every new connection:

```sql
PRAGMA journal_mode = WAL;
PRAGMA busy_timeout = 5000;
PRAGMA foreign_keys = ON;
PRAGMA synchronous = NORMAL;
PRAGMA temp_store = MEMORY;
```

- Two pools: a **writer** with `SetMaxOpenConns(1)` and a **reader** pool. The query builder routes `SELECT` to the reader and everything else (and all transactions) to the writer.
- Write transactions use `BEGIN IMMEDIATE` to avoid `SQLITE_BUSY` upgrades.
- Tests use `file::memory:?cache=shared` or a file in `t.TempDir()`.
- Known SQLite quirk: an unknown double-quoted identifier is read as a string literal, so `t.Index("misspelled")` in a migration creates a useless index on a constant instead of failing. Postgres and MySQL reject it; run the migrations of apps that deploy on them against that database in CI (`TEST_DATABASE_URL`).

Apps that never open a SQLite database can leave the driver out with the `vukem_nosqlite` build tag (`go build -tags vukem_nosqlite`, or `vukem_nosqlite` in the project's `.buildtags`, which `vukem new --minimal` writes). `database.Open` then refuses SQLite URLs with an error naming the tag. Tests (`vukemtest`) keep using in-memory SQLite, so run them without the tag.

## Query builder

Generic, built on `database/sql`, dialect-aware placeholders (`?` vs `$1`), always parameterised.

```go
// Select
users, err := orm.QueryOf[models.User](db).
	Where("active", true).
	WhereIn("role", []string{"admin", "editor"}).
	OrderBy("created_at", orm.Desc).
	Limit(20).
	Get(ctx)                                  // []User

user, err := orm.QueryOf[models.User](db).Find(ctx, 42) // orm.ErrNotFound → 404 in handlers
n, err := orm.QueryOf[models.User](db).Where("active", true).Count(ctx)
ok, err := orm.QueryOf[models.User](db).Where("email", e).Exists(ctx)
emails, err := orm.QueryOf[models.User](db).Pluck[string](ctx, "email") // Go 1.27 generic method

// Streaming with range-over-func
for u, err := range orm.QueryOf[models.User](db).Each(ctx) {
	if err != nil { return err }
	process(u)
}

// Writes
err = orm.Create(ctx, db, &user)            // fills ID, timestamps
err = orm.Update(ctx, db, &user)            // all columns (explicit)
_, err = orm.QueryOf[models.User](db).Where("id", id).Update(ctx, orm.Set{"name": "x"})
err = orm.Delete(ctx, db, &user)            // soft delete if model opts in
err = orm.CreateMany(ctx, db, users)        // multi-row INSERT, batched; atomic on db and tx only

// Row locks (inside a transaction; no-op on SQLite)
num, err := orm.QueryOf[models.Number](tx).Where("id", id).LockForUpdate().First(ctx)

// Transactions
err = db.Tx(ctx, func(tx *database.Tx) error {
	if err := orm.Create(ctx, tx, &order); err != nil { return err }
	return orm.Create(ctx, tx, &item)
}) // commit on nil, rollback on error or panic
```

Inside `db.Tx`, use `tx` for every query (pass it as the `database.Executor`). SQLite has a single writer connection, so a write through `db` while the transaction holds it waits forever; `database.RunInTx(ctx, ex, fn)` joins the caller's transaction when `ex` is already a `*database.Tx`. `tx.AfterCommit(func(ctx context.Context) { ... })` runs work once the transaction commits (in registration order) and drops it on rollback or panic; a hook registered after the `db.Tx` function returned never runs, a panicking hook is logged while the others still run (`db.Tx` returns nil: the commit happened), and calling it on a `Tx` not started by `db.Tx` panics; `queue.DispatchTx` uses it on drivers without transactional storage.

| Group | Methods |
| --- | --- |
| Where | `Where(col, val)`, `Where(col, op, val)`, `OrWhere`, `WhereIn`, `WhereNotIn`, `WhereNull`, `WhereNotNull`, `WhereBetween`, `WhereRaw(sql, args...)`, grouped `WhereGroup(func(q))` |
| Shape | `Select(cols...)`, `OrderBy`, `Latest()`, `Limit`, `Offset`, `WithTrashed()`, `OnlyTrashed()` |
| Read | `Get`, `First`, `Find`, `Count`, `Exists`, `Pluck[V]`, `Each`, `Chunk(n, fn)` |
| Lock | `LockForUpdate()`, `ForShare()`, then optional `SkipLocked()` / `NoWait()`; needs a `*database.Tx` (`orm.ErrLockOutsideTx` otherwise); all four are no-ops on SQLite, where `NoWait` never fails |
| Paginate | `Paginate(ctx, page, perPage)` → `Page[T]{Items, Total, Page, PerPage, LastPage}`; `CursorPaginate(ctx, cursor, perPage, dir)` → `CursorPage[T]` |
| Scopes | `Scope(fns ...func(Query[T]) Query[T])` |
| Raw | `WhereRaw(sql, args...)` for conditions; `db.QueryContext` for anything else |

Column names passed as strings are validated against the model's cached column set at query build time (unknown column → error before hitting the database). Raw SQL is the explicit escape hatch.

Raw SQL shared between dialects is written once with `?` and passed through `database.Rebind(ex.Dialect(), query)`: on PostgreSQL the n-th `?` becomes `$n`, elsewhere the query is unchanged. `?` inside string literals, quoted identifiers, dollar-quoted bodies and comments is left alone, and `??` is a literal `?` on PostgreSQL (jsonb operators). `DB.ExecContext`/`QueryContext` never rebind on their own, so queries already written with `$n` keep working.

The orm uses the same scanner (`internal/sqlscan`): `WhereRaw` counts placeholders with the rules `Rebind` renumbers with, so the two cannot disagree, and it rejects a fragment that ends inside a literal, identifier or comment (a trailing `--` comment too). It also rejects explicit `$n` parameters on PostgreSQL, which would bind another condition's value once the statement is renumbered; without an executor, `WhereRaw` lexes with SQLite's rules. Statements with a WHERE clause (select, `Query.Update`, `Query.Delete`) fail closed with an error if they end inside a literal or comment.

Dialect rules: every dialect skips `'…'` and `"…"`, `--` and `/* */` comments; PostgreSQL adds `E'…'`, nested comments and `$tag$` bodies (a backtick is an operator there); MySQL adds `` `…` ``, backslash escapes, `#` comments and needs whitespace after `--`; SQLite adds `` `…` `` and `[…]` identifiers. The lexer assumes the servers' default settings: `standard_conforming_strings` on (PostgreSQL), no `ANSI_QUOTES` or `NO_BACKSLASH_ESCAPES` (MySQL). MySQL executes the contents of `/*! */` and `/*+ */` comments, but they are skipped like any comment, so placeholders inside them are not supported. `Rebind` does not check for `$n` mixed with `?`: write one style per query.

## UUIDs

Package `uuid` (stdlib only) generates and parses RFC 9562 UUIDs for public ids:

```go
id := uuid.NewV7()               // time-ordered: sorts by creation millisecond, random inside one
key := uuid.NewV4()              // fully random
u, err := uuid.Parse(r.PathValue("id")) // canonical 36-char form only; errors.Is(err, uuid.ErrInvalid)
```

- Randomness comes from `crypto/rand` (122 random bits for v4, 74 for v7); a v7 UUID reveals its creation time (`u.Time()`).
- `uuid.UUID` is a `[16]byte`: JSON and query strings use the canonical lowercase text (`encoding.TextMarshaler`); SQL writes the text (`driver.Valuer`: PostgreSQL `uuid`, SQLite/MySQL text) and scans text or 16-byte binary columns. NULL needs a `*uuid.UUID`; an empty string never parses as the nil UUID.
- `t.UUID("public_id").Unique()` in a migration matches it.

## Models

Plain structs. Mapping is computed **once per type** with reflection and cached; no reflection in the hot path beyond scanning.

```go
type User struct {
	orm.Model                       // ID int64, CreatedAt, UpdatedAt
	orm.SoftDeletes                 // DeletedAt sql.Null[time.Time] (opt-in)
	Name     string
	Email    string `db:"email"`
	Password string `db:"password" json:"-"`
	Settings orm.JSON[Settings]     // JSON column via Scanner/Valuer
}

func (User) Table() string { return "users" } // optional; default snake_case plural
```

- Conventions: table = snake_case plural of type name; column = snake_case of field; primary key `id`.
- Optional interfaces: `Table() string`, `Connection() string`, `PrimaryKey() string`.
- Hooks via interfaces: `BeforeCreate(ctx) error`, `AfterCreate`, `BeforeUpdate`, `BeforeDelete`…
- ULID/UUID keys: embed `orm.ULIDModel` instead of `orm.Model`.
- No Active Record: models never hold a DB handle; persistence functions take the executor (`db` or `tx`).
- **Strict mode** (default in development/test): accessing a relation that was not eager-loaded returns `orm.ErrNotLoaded`.

### Encrypted columns

`orm.Encrypted[T]` stores `T` as JSON encrypted with the connection's encrypter: `app.DB()` passes `app.Crypt`; elsewhere use `database.Open(ctx, url, database.WithEncrypter(enc))`.

```go
type Patient struct {
	orm.Model
	Name      string
	Diagnosis orm.Encrypted[string] `json:"-"` // TEXT column
}

p := Patient{Name: "Ada", Diagnosis: orm.Encrypted[string]{V: "flu"}}
err := orm.Create(ctx, db, &p)           // stores ciphertext
got, err := orm.QueryOf[Patient](db).Find(ctx, p.ID) // got.Diagnosis.V == "flu"
```

- Sealed on `Create`, `Update` and `Query.Update`; opened by queries, eager loads and `Pluck`. `"orm:<table>.<column>"` is the additional data, so a ciphertext copied to another column (or table) is rejected; renaming the table or column needs a re-encryption.
- Fails closed: without an encrypter, writes and reads return `orm.ErrNoEncrypter` and nothing is stored. Passing an `Encrypted` value to `database/sql` directly (`Value`/`Scan`) is an error, so plaintext never reaches the database by accident.
- Key rotation: values sealed with a key from `APP_PREVIOUS_KEYS` still decrypt; saving the model re-encrypts every encrypted column with the current key.
- Not queryable: every encryption is randomized, so `Where("diagnosis", ...)` cannot match. NULL reads as the zero value.
- JSON encoding uses the plain value (for API output); tag the field `json:"-"` to keep it out of responses.

## Relations

```go
type Post struct {
	orm.Model

	UserID   int64
	User     orm.BelongsTo[User]   `json:"user,omitzero"`               // posts.user_id → users.id
	Comments orm.HasMany[Comment]  `json:"comments,omitzero"`           // comments.post_id
	Tags     orm.ManyToMany[Tag]   `json:"tags,omitzero"`               // pivot post_tag(post_id, tag_id)
	Cover    orm.HasOne[Image]     `json:"cover,omitzero" orm:"fk:owner_id"`
}

posts, err := orm.QueryOf[Post](db).With("User", "Comments", "Tags").Get(ctx)
user, err := posts[0].User.Get()   // *User (nil when the key points nowhere)
tags, err := posts[0].Tags.Get()   // []Tag
_, err = plain.User.Get()          // orm.ErrNotLoaded: relations are never lazy (strict mode)
```

| Relation | Convention | Override (`orm` tag) |
| --- | --- | --- |
| `BelongsTo[T]` | column `<field>_id` on this model → `T`'s primary key | `fk:author_id` |
| `HasOne[T]` / `HasMany[T]` | column `<model>_id` on `T` → this primary key | `fk:owner_id` |
| `ManyToMany[T]` | pivot `<a>_<b>` (singular snake names, sorted), columns `<model>_id`, `<t>_id` | `pivot:taggings,fk:post_id,rk:tag_id` |

- `With` works for `Get`, `First`, `Find`, `Paginate` and `CursorPaginate` (not `Each`): one query per relation batched with `IN` (chunks of 500), two for many-to-many. N rows with two relations cost 3 queries, which the conformance suite asserts on SQLite and Postgres.
- Soft-deleted related rows are not loaded. Unknown relation names are query errors.
- Relation fields are not columns; with `json:",omitzero"` they appear in JSON only when loaded.
- Pivot writes: `orm.Attach(ctx, db, &post, "Tags", 1, 2)` (skips ids already linked), `orm.Detach(ctx, db, &post, "Tags", 2)` (no ids: all), `orm.Sync(ctx, db, &post, "Tags", 1, 3)` (exact set, in a transaction).
- Nested eager loading (`"Comments.User"`) is not supported yet; load the second level with its own query.

## Migrations

```go
// database/migrations/20261016120000_create_users_table.go
package migrations

func init() {} // none — migrations are listed in migrations/registry.go (edited by make:migration)

var CreateUsersTable = schema.Migration{
	ID: "20261016120000_create_users_table",
	Up: func(s *schema.Schema) error {
		return s.Create("users", func(t *schema.Table) {
			t.ID()
			t.String("name")
			t.String("email").Unique()
			t.String("password")
			t.Timestamp("email_verified_at").Nullable()
			t.Timestamps()
		})
	},
	Down: func(s *schema.Schema) error { return s.Drop("users") },
}
```

- `registry.go` holds `var All = []schema.Migration{...}`, appended by `make:migration` (codemod) — no `init()` registration.
- Column types: `ID`, `BigInteger`, `Integer`, `String(n)`, `Text`, `Boolean`, `Decimal(p,s)`, `Float`, `Date`, `Timestamp`, `Timestamps`, `SoftDeletes`, `JSON`, `UUID`, `ULID`, `Enum(vals)`, `ForeignID("user_id").Constrained().CascadeOnDelete()`; modifiers `Nullable`, `Default`, `Unique`, `Index`, `Comment`.
- `s.Table(name, fn)` alters (add/rename/drop column, indexes); SQLite table rebuild handled by the dialect where `ALTER` is unsupported.
- `t.Check(name, expr)` adds a named CHECK constraint (in `Create` on every dialect; in `s.Table` on PostgreSQL and MySQL only, SQLite fails the migration); `t.DropCheck(name)` removes one (MySQL 8.0.19+ / MariaDB 10.2+).
- `t.Index(cols...)` / `t.UniqueIndex(cols...)` return an `*Index`; `.Where(expr)` makes it partial (`CREATE [UNIQUE] INDEX … WHERE expr`) on PostgreSQL and SQLite. MySQL/MariaDB have no partial indexes, so `Where` fails the migration there instead of creating a full index.
- `s.Raw(sql)` for anything else.
- Tracking table `migrations(id, batch, ran_at)`; each migration runs in a transaction where the dialect allows DDL in transactions.
- Commands: `migrate`, `migrate:rollback [--step N]`, `migrate:reset`, `migrate:fresh [--seed]`, `migrate:status`, `make:migration create_posts_table` (name prefix `create_`/`add_`/`alter_` picks the stub).

## Factories and seeders

```go
// database/factories/user_factory.go — vukem make:factory User
var User = factory.New(func(f *factory.Faker) models.User {
	return models.User{Name: f.Name(), Email: f.Email()}
})

admins, err := factories.User.Count(3).State(func(u *models.User) { u.Role = "admin" }).Create(ctx, db)
draft := factories.User.MakeOne() // not persisted
same := factories.User.Seed(42).Count(5).Make() // reproducible data

// Related models: AfterCreate runs inside Create's transaction.
withPosts := factories.User.AfterCreate(func(ctx context.Context, ex database.Executor, u *models.User) error {
	_, err := factories.Post.Count(2).State(func(p *models.Post) { p.UserID = u.ID }).Create(ctx, ex)
	return err
})
```

- `factory.Faker` (no dependency): `Name`, `FirstName`, `LastName`, `Username`, `Email` (reserved example domains), `Word(s)`, `Sentence`, `Paragraph`, `Slug`, `URL`, `UUID`, `Int`, `Float`, `Bool`, `Time`, `factory.Pick(f, ...)`, `Seq()`. It uses `math/rand/v2` and must never generate secrets.
- Factories are values: `State`, `Count`, `Seed`, `AfterCreate` return copies.
- Seeders implement `vukem.Seeder` (`Seed(ctx, db) error`); `vukem make:seeder Users` creates one and registers it in `database/seeders/registry.go`, which the app passes to `vukem.WithSeeders(seeders.All()...)`. `vukem db:seed` runs them in order; tests call `app.Seed(ctx)`. Seeders that need config, the logger or provider services implement `vukem.AppSeeder` (`SeedApp(ctx, app) error`, or `vukem.AppSeederFunc`) and are passed to `vukem.WithAppSeeders`; both kinds run in one list in option order, after `db:seed` booted the app. A seeder's `Name() string`, if any, names it in output and errors; `vukem.NamedAppSeeder(name, fn)` names a function.
- `vukem db:wipe` drops every table. Both commands need `--force` in production.

## Database validation rules

`z` schemas check shape and format without I/O. Rules that need the database run after parsing and return the same `422` validation error (flashed for forms):

```go
in, err := web.Parse(c, requests.RegisterSchema)
if err != nil {
	return err
}

if err := orm.Validate(c.Context(), db,
	orm.Unique[models.User]("email", in.Email),                          // soft-deleted rows count, like the index
	orm.Unique[models.User]("username", in.Username).Ignore(current.ID), // updates keep their own value
	orm.Exists[models.Team]("id", in.TeamID).Field("team_id").Message("Pick a valid team."),
); err != nil {
	return err
}
```

The built-in messages follow the request locale (`validation.unique`, `validation.exists`, with `{field}` from `validation.attributes.<column>`; see [i18n](subsystems.md#i18n)); a `Message` override is used as is.

Empty values are skipped (`required` belongs to the schema). Columns are validated against the model, so input never chooses a column.

## MongoDB

`go get github.com/AlexGalhardo/vukem/drivers/mongo` (official driver v2). A separate, typed API, not the SQL builder:

```go
m, err := mongo.Open(ctx, os.Getenv("MONGO_URL"), os.Getenv("MONGO_DATABASE")) // empty MONGO_DATABASE: database from the URI path
app.AddHealthCheck("mongo", m.Ping)                                              // reported by /up?checks=1

users := mongo.Collection[models.User](m, "users")
u, err := users.FindOne(ctx, bson.D{{Key: "email", Value: e}}) // mongo.ErrNotFound when missing
for u, err := range users.Find(ctx, bson.D{{Key: "active", Value: true}}, options.Find().SetLimit(50)) { ... }
id, err := users.Insert(ctx, u)
matched, modified, err := users.UpdateOne(ctx, filter, bson.D{{Key: "$set", Value: bson.D{{Key: "name", Value: n}}}})
totals, err := mongo.Aggregate[Total](ctx, users, pipeline)
```

Also `All`, `Count`, `InsertMany`, `UpdateMany`, `ReplaceOne`, `DeleteOne`, `DeleteMany` and `Raw()` for indexes and change streams. **Never pass decoded request JSON as a filter**: operators such as `$where` or `$ne` would come from the client; build filters from validated fields.

- **Database name.** `Open(ctx, uri, db)` uses `db` when it is non-empty, else the URI path (`mongodb://host:27017/app`); with neither, `Open` fails. Managed providers often give a URL without a path (Railway's `MONGO_URL` is `mongodb://mongo:<password>@mongodb.railway.internal:27017`), so set `MONGO_DATABASE=app` instead of editing the URL.
- **authSource.** A database in the URI path is also the default `authSource`: appending `/app` to a provider URL makes the driver authenticate against `app` instead of `admin`, and login fails. Keep the path empty and pass the database name, or add `?authSource=admin` (`mongodb://user:pw@host:27017/app?authSource=admin`).
- **Boot without the server.** `Open` pings the primary, so the app does not boot while MongoDB is down; a malformed URI always fails. `mongo.OpenLazy(uri, db)` validates the URI and database name without contacting the server, which lets the process start and report the outage through `m.Ping` in a readiness check (`app.AddHealthCheck`). The client starts its topology-monitoring goroutines immediately (even before the server is up), so close it on shutdown: `app.OnShutdown(m.Close)`. Server selection waits 10 s unless the URI sets `serverSelectionTimeoutMS`.

## Redis

`drivers/redis` provides cache, session and queue stores, a shared rate limiter, locks and pub/sub; it works with Redis 7+ and Valkey 8+. See [subsystems.md](subsystems.md#redis).
