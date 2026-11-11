# Databases

Connections, the generic query builder, models, relations, migrations, factories and seeders. Decisions `D1`–`D5` in [brainstorming.md](brainstorming.md).

Where to look:

| Topic | Section |
| --- | --- |
| Why the ORM is shaped this way | [Design principles](#design-principles) |
| Drivers and connection settings | [Support matrix](#support-matrix), [Connections](#connections) |
| Reading and writing rows | [Query builder](#query-builder), [Models](#models), [Relations](#relations) |
| Schema changes | [Migrations](#migrations), [Tables the framework owns](#tables-the-framework-owns) |
| Test and demo data | [Factories and seeders](#factories-and-seeders) |
| Rules that need the database | [Database validation rules](#database-validation-rules) |
| Non-SQL stores | [MongoDB](#mongodb), [Redis](#redis) |
| What is missing and what other ORMs taught us | [Not supported yet](#not-supported-yet), [Lessons from other ORMs](#lessons-from-other-orms) |

## Design principles

The ORM is SQL-first and typed. These principles decide what gets added and what is refused; a feature that breaks one needs a decision in [brainstorming.md](brainstorming.md) first.

- **You can predict the SQL.** One builder call chain produces one statement, and `Query.SQL()` shows it. Eager loading is the documented exception: one extra `IN` query per relation, never one per row.
- **The builder covers SQL instead of hiding it.** Methods are named after the SQL they emit (`Where`, `OrderBy`, `LockForUpdate`). Raw SQL (`WhereRaw`, `db.QueryContext`, `s.Raw`) is a supported path, not a failure of the builder.
- **Typed results, validated names.** Results are `T`, `[]T` or a generic `V`; a column name is checked against the model before the statement reaches the database, so input never chooses a column.
- **Nothing implicit.** No Active Record (a model never holds a connection), no lazy loading (`orm.ErrNotLoaded`), no automatic transaction around a write, no query run by a getter.
- **Writes say what they write.** A filtered `Update` or `Delete` with `Limit`, `Offset` or `OrderBy` is an error rather than a wider write than the code reads as.
- **Destructive changes need consent.** `migrate:reset`, `migrate:fresh`, `db:seed` and `db:wipe` need `--force` in production; nothing drops data as a side effect of another command.
- **One model, every dialect.** The same models, queries and migrations run on SQLite, PostgreSQL and MySQL, and [`database/dbtest`](#postgresql) is the conformance suite a dialect must pass. Where a dialect cannot do something, the call fails with an error instead of doing something weaker.
- **Stdlib underneath.** Everything runs on `database/sql`; `db.SQL()` hands the same pool to sqlc or any other library.

## Support matrix

| Database | Driver | Module | Phase |
| --- | --- | --- | --- |
| SQLite | `modernc.org/sqlite` (pure Go) | core | Prototype |
| PostgreSQL | `jackc/pgx/v5/stdlib` | `drivers/postgres` | ALPHA |
| MySQL 8 / MariaDB 11 | `go-sql-driver/mysql` | `drivers/mysql` | BETA |
| Turso | `turso.tech/database/tursogo` (pure Go) | `drivers/turso` | Experimental |
| Redis | `redis/go-redis/v9` | `drivers/redis` | BETA |
| MongoDB | `go.mongodb.org/mongo-driver/v2` | `drivers/mongo` | BETA |

## Connections

`app.DB()` opens the default connection on first use from `vukem.DatabaseConfig`, adds a `db` health check, passes `app.Crypt` as the encrypter (`orm.Encrypted` columns) and closes it on shutdown:

| Variable | Default | Meaning |
| --- | --- | --- |
| `DATABASE_URL` | `file:storage/database.sqlite` | Connection URL; its scheme picks the driver (`file:`/`sqlite:` built in, `postgres://`, `mysql://` and `mariadb://` from `WithDatabaseDrivers`) |
| `DB_MAX_OPEN` | `10` | Open connections (the reader pool on SQLite) |
| `DB_MAX_IDLE` | `5` | Idle connections kept |

There is no `DB_CONNECTION` (the URL scheme decides) and no lifetime variable; pass a `database.Config` yourself when you open a connection with `database.Open(ctx, url, database.WithConfig(cfg))`.

- Each connection is a `*database.DB` wrapping `*sql.DB` plus its `Dialect`; `db.SQL()` exposes the raw `*sql.DB` (the writer pool on SQLite) for sqlc or other libraries.
- More connections: open them with `database.Open` and keep them where your code can reach them; `database.NewManager(db)` with `Add(name, db)`, `Conn(name)`, `Default()` and `Names()` is a small registry for that. The app itself only manages `app.DB()`.
- Query observers (`database.QueryObserver`) see every statement run through a `DB` or its transactions (SQL, args, duration, error). `database.WithQueryObserver` can be passed several times and `db.AddQueryObserver` appends: all of them are notified in order. `db.SetQueryObserver` replaces them all. `vukem.WithQueryObservers(obs...)` adds observers to `app.DB()` next to devtools. Nil observers, typed nil pointers included, are ignored. A panicking observer is logged (observer type and panic value, never SQL or args) and skipped: the statement still returns its result or rows, and the observers after it still run.
- A `database.Dialect` says how SQL is spelled; `database.Capabilities` (from `database.CapabilitiesOf(d)`, implemented by a dialect through `database.Capable`) says what it supports, and the core branches on that instead of on `Dialect.Name()`: `Family` (`sqlite`, `postgres` or `mysql`, the SQL grammar), `Returning`, `MaxParams`, `RowLocks`, `ILike`, `LimitForOffset`, `TransactionalDDL`, `AdvisoryLock`, `PartialIndexes`, `IndexMethods` and `AlterConstraints`. `database.Open` rejects a driver whose dialect does not implement `Capable`, and the ORM fails closed on one. A SQLite-compatible engine plugs in as a `database.Driver` whose dialect reports `Family: "sqlite"`. `Driver.IsUniqueViolation` lets a driver say which of its errors are unique violations. Add a field to `Capabilities` and this list changes together.
- Postgres and MySQL are available once the app passes `postgres.Driver()` / `mysql.Driver()` to `vukem.WithDatabaseDrivers` (or `database.WithDriver` to `database.Open`) — explicit, no `init()`.

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

A `postgres://` URL to a host that is not loopback or a unix socket verifies TLS by default (`sslmode=verify-full`); put an explicit `sslmode=` in the URL to change it, for example `?sslmode=disable` on a trusted private network.

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
| `clientFoundRows` | `true` | `UPDATE` counts matched rows, as on SQLite and Postgres (`orm.Update` relies on it); `mysql.DSN` refuses `clientFoundRows=false` |
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

- Two pools: a **writer** with `SetMaxOpenConns(1)` and a read-only **reader** pool (`DB_MAX_OPEN`). `QueryContext`/`QueryRowContext` go to the reader (a `RETURNING` statement to the writer); `ExecContext` and every transaction go to the writer.
- Write transactions use `BEGIN IMMEDIATE` to avoid `SQLITE_BUSY` upgrades.
- An in-memory database (`:memory:` or `mode=memory`) lives in one connection shared by reads and writes, without WAL. Tests use `:memory:` or a file in `t.TempDir()`.
- Known SQLite quirk: an unknown double-quoted identifier is read as a string literal, so `t.Index("misspelled")` in a migration creates a useless index on a constant instead of failing. Postgres and MySQL reject it; run the migrations of apps that deploy on them against that database in CI (`TEST_DATABASE_URL`).

- SQLite reports no column type for `MIN`/`MAX`/`COALESCE` or a subquery over a timestamp column, so the driver returns the stored text and a bare `Scan` into `time.Time` fails. `database.ScanTime(&dest)` (`*time.Time`, `**time.Time`, `*sql.NullTime`, `*sql.Null[time.Time]`) parses the driver's and SQLite's text forms (dates, date-times with `T` or a space, `Z` or an offset; not time-only text or unix integers) and passes a `time.Time` from any dialect through; zoned text comes back in a fixed zone, so compare with `Equal`; the ORM wraps its time fields with it on SQLite. String destinations stay raw (no global `_texttotime`: it would turn `MAX(day)` over text dates into RFC 3339 strings).

Apps that never open a SQLite database can leave the driver out with the `vukem_nosqlite` build tag (`go build -tags vukem_nosqlite`, or `vukem_nosqlite` in the project's `.buildtags`, which `vukem new --minimal` writes). `database.Open` then refuses SQLite URLs with an error naming the tag. Tests (`vukemtest`) keep using in-memory SQLite, so run them without the tag.

### Turso (experimental)

Turso is a SQLite-compatible engine. `drivers/turso` keeps a **local database file** and, when you give it a Turso Cloud database, syncs that file with it. It depends on a pre-1.0 SDK (`turso.tech/database/tursogo`, exact version) and may change in any release; the module is marked experimental.

```sh
go get github.com/AlexGalhardo/vukem/drivers/turso
```

```go
// bootstrap/app.go
tursoOpts, err := turso.OptionsFromEnv()
if err != nil {
	return nil, err
}

db := turso.New(tursoOpts)

base := []vukem.Option{
	vukem.WithDatabaseDrivers(db.Driver()), // turso:// URLs
	vukem.WithRunner("turso-sync", db),     // background sync
	// ...
}
```

| Variable | Example | Meaning |
| --- | --- | --- |
| `DATABASE_URL` | `turso://storage/database.db` | The local file, relative to the working directory. Absolute: `turso:///var/data/app.db`; Windows: `turso:///C:/data/app.db`. |
| `TURSO_DATABASE_URL` | `libsql://myapp-org.turso.io` | The Turso Cloud database. Empty: a plain local file with no sync. |
| `TURSO_AUTH_TOKEN` | (secret) | The database token, required with `TURSO_DATABASE_URL`. It never appears in an error or a log line. |
| `TURSO_SYNC_INTERVAL` | `5s` | Pause between background syncs; `-1s` turns them off. |

`TURSO_DATABASE_URL` must be `libsql://` or `https://`; `http://` is accepted only for a loopback host. With a remote database the app pulls on open (a failed pull is a warning when the local file already exists), pushes and pulls every interval, and pushes once more when the connection closes, so a short command such as `migrate` leaves its changes in the cloud. `(*turso.Database).Sync(ctx)` syncs on demand. Create the database and the token with the Turso CLI:

```sh
turso db create myapp
turso db show myapp --url
turso db tokens create myapp
```

What does not work:

- `db:backup` and `db:restore` refuse: only the built-in SQLite has a file snapshot.
- One connection for reads and writes, no reader/writer split. Inside a transaction use only the transaction's executor: a query on the database itself waits for that connection and blocks until its context ends, so give such queries a deadline. Close the database after the sync runner has stopped, so the final push does not race a background sync.
- `Schema.Dialect()` returns `"turso"`, not `"sqlite"`: migration code must test the family (`database.CapabilitiesOf(db.Dialect())`), not the name.
- No migration lock: with several instances, run `migrate` in one place (a release phase) and let the others pull.
- Conflicts are "last push wins" (the upstream's rule), so several instances must not write the same row.
- Libraries ship for linux and darwin (amd64, arm64) and windows/amd64 only; windows/arm64 and other platforms are not supported.
- `vukemtest` keeps using the in-memory SQLite of the built-in driver.
- A local file that is deleted (an ephemeral deploy) is rebuilt from the cloud when `TURSO_DATABASE_URL` is set; without it the data is gone, as with any SQLite file.

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
err = orm.Update(ctx, db, &user)            // all columns (explicit); a trashed row is ErrNotFound
err = orm.UpdateOnly(ctx, db, &user, "name") // only these columns (and updated_at)
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
| Where | `Where(col, val)`, `Where(col, op, val)`, `WhereIn`, `WhereNotIn`, `WhereNull`, `WhereNotNull`, `WhereBetween`, `WhereRaw(sql, args...)`, grouped `WhereGroup(func(q))`; each has an `OrWhere*` twin (`OrWhere`, `OrWhereIn`, `OrWhereNotIn`, `OrWhereNull`, `OrWhereNotNull`, `OrWhereBetween`, `OrWhereRaw`, `OrWhereGroup`) |
| Shape | `Select(cols...)`, `OrderBy`, `Latest()`, `Limit`, `Offset`, `WithTrashed()`, `OnlyTrashed()` |
| Read | `Get`, `First`, `Find`, `Count`, `Exists`, `Pluck[V]`, `Each`, `Chunk(ctx, n, fn)` |
| Write | `Update(ctx, orm.Set{...})`, `Delete(ctx)` on the filtered rows; both return an error when the query has `Limit`, `Offset` or `OrderBy` (they used to ignore them and write every match): `Pluck` the keys and `WhereIn` them to write a bounded set |
| Lock | `LockForUpdate()`, `ForShare()`, then optional `SkipLocked()` / `NoWait()`; needs a `*database.Tx` (`orm.ErrLockOutsideTx` otherwise); all four are no-ops on SQLite, where `NoWait` never fails |
| Paginate | `Paginate(ctx, page, perPage)` → `Page[T]{Items, Total, Page, PerPage, LastPage}`; `CursorPaginate(ctx, cursor, perPage, dir)` → `CursorPage[T]` (keyset = the query's `OrderBy` columns plus the primary key in `dir`, or the primary key alone; cursor values decoded into the columns' Go types; keys must be NOT NULL); `MaxPerPage(n)` |
| Scopes | `Scope(fns ...func(Query[T]) Query[T])` |
| Join | `InnerJoin[J](left, op, right)`, `LeftJoin[J](...)`, `JoinRelation(name)`, `LeftJoinRelation(name)`; columns are validated and written with their table name |
| Aggregate | `Distinct()`, `GroupBy(cols...)`, `Having(orm.Sum("col"), ">", 100)`, `HavingRaw`; terminals `Sum[V]`, `Avg`, `Min[V]`, `Max[V]`; `Count` counts distinct rows after `Distinct()` |
| Projection | `SelectAs(col, alias)`, `SelectExpr(orm.Sum("col"), alias)`, then `Scan[R](ctx)` into any struct |
| Subquery | `WhereExists(sub)`, `WhereNotExists(sub)`, `WhereInQuery(col, sub)`, `WhereNotInQuery`, `WhereColumn(left, op, right)`, and `sub.WhereOuter[Outer](col, op, outerCol)` to correlate |
| Relation filters | `WhereHas[R](relation, fn)`, `WhereDoesntHave[R](relation, fn)`, `WithCount(relations...)`, `WithWhere[R](relation, fn)` |
| Raw | `WhereRaw(sql, args...)` for conditions; `orm.Raw[R](ctx, ex, sql, args...)` and `orm.RawEach[R]` for typed SELECTs; `db.QueryContext` for anything else |

`OrWhere*` conditions join with `OR` and `AND` binds tighter, as in Laravel: `Where(a).OrWhere(b).Where(c)` is `a OR (b AND c)`; use `WhereGroup` / `OrWhereGroup` to group. The ORM's own conditions (soft delete, the key of `Find`, `Chunk` and cursor pagination) wrap your `OR` filter in parentheses first, so they always apply to all of it. `ILIKE` is native on PostgreSQL; on MySQL and SQLite it is sent as `LIKE`, which is case-insensitive there only for ASCII letters (and, on MySQL, by the column collation).

The ORM needs MySQL to report matched rows for `UPDATE` (`clientFoundRows=true`, the default of `drivers/mysql`): with `false`, an update that changes nothing looks like a missing row (`ErrNotFound`), so `mysql.DSN` refuses it at boot (`ErrInvalidURL`: remove `clientFoundRows=false` from the URL). That includes connections wrapped with `database.FromSQL`, whose DSN the framework does not see.

### Transactions

`db.Tx(ctx, fn)` commits when `fn` returns nil and rolls back on an error or a panic. `db.TxWith(ctx, database.TxOptions{Isolation: sql.LevelSerializable, ReadOnly: true}, fn)` takes an isolation level and a read-only flag (a level the driver does not support is its error, wrapped as `database: begin: ...`). On SQLite a read-only transaction of a file database runs on the reader pool, so it never waits for the single writer, and the reader opens the file read-only; an in-memory database has one connection for both, so the transaction runs with `PRAGMA query_only` instead. A write inside it fails either way. A `Tx` is not safe for concurrent use, and neither is `Savepoint`.

- `database.RunInTx(ctx, ex, fn)` joins the caller's transaction when `ex` is a `*database.Tx`: everything is all or nothing. `CreateMany`, `Sync` and the queue use it.
- `database.Nested(ctx, ex, fn)` opens a transaction on a `*database.DB` and a **savepoint** on a `*database.Tx`: an error or panic from `fn` undoes only `fn`'s work and the outer transaction stays usable. `tx.Savepoint(ctx, fn)` is the same on a `Tx`. The savepoint name is generated (`vk_sp_<n>`), never taken from the caller, and `AfterCommit` hooks registered inside a rolled-back savepoint are dropped.
- On MySQL, DDL commits implicitly, which ends the transaction: run schema changes as migrations, not inside a savepoint. Serializable isolation can fail at commit with a serialization error on PostgreSQL; the error comes back as it is and nothing retries it.

```go
err := db.Tx(ctx, func(tx *database.Tx) error {
	if err := orm.Create(ctx, tx, &order); err != nil { return err }
	if err := tx.Savepoint(ctx, func(sp *database.Tx) error {
		return orm.Create(ctx, sp, &optionalAudit)
	}); err != nil {
		slog.WarnContext(ctx, "audit skipped", "err", err) // the order still commits
	}
	return nil
})
```

### Optimistic locking

Mark one `int64` column `orm:"version"` and `Update` / `UpdateOnly` stop losing concurrent changes:

```go
type Article struct {
	orm.Model
	Title   string
	Version int64 `orm:"version"`
}
```

`Create` (and `CreateMany`, `Upsert`) start the version at 1. An `Upsert` that updates an existing row bumps its version too (and writes the new one back into the struct, with `RETURNING` or one extra `SELECT` on MySQL), so a reader from before it gets `ErrStale`. `Update` adds `"version" = "version" + 1` to the `SET` and `AND "version" = ?` (the version the struct holds) to the `WHERE`, then increments the struct's field. When no row matches, one more query tells the two cases apart: the row exists, so it changed since it was read (`orm.ErrStale`, HTTP 409), or it is gone (`orm.ErrNotFound`, 404).

- The version has to travel with the payload: a form that does not send it back gets a struct with version 0, so `Update` fails with `ErrStale`.
- `Query.Update(ctx, orm.Set{...})` does not validate the version: it increments it without comparing (unless `version` is in the `Set`); `Delete` does not compare it either. The version column cannot be written through `UpdateOnly`.

### Typed columns

Optional codegen on top of the string API, which stays the base: `codegen:models` writes one variable per table with a typed reference for every column, and `Filter`, `OrFilter`, `FilterJoined` and `Sort` turn them into the same `Where*` / `OrderBy` calls, so there is one validation of the column and one SQL generator.

```go
// bootstrap/app.go: the app lists its models (core cannot discover them)
vukem.WithModels(orm.ModelOf[models.User](), orm.ModelOf[models.Post]())
```

```bash
./app codegen:models               # app/models/cols/cols_gen.go
./app codegen:models -check        # CI: fails when the file is out of date
```

```go
// before (still valid)
users, err := orm.QueryOf[models.User](db).
	Where("active", true).Where("age", ">=", 18).OrderBy("created_at", orm.Desc).Get(ctx)

// after
users, err := orm.QueryOf[models.User](db).
	Filter(cols.Users.Active.Eq(true), cols.Users.Age.Gte(18)).
	Sort(cols.Users.CreatedAt.Desc()).Get(ctx)
```

The compiler rejects `cols.Users.Email.Eq(42)` (the column holds a `string`) and `Filter(cols.Users.Email.Eq("x"))` on a `Post` query. `FilterJoined(cols.Users.Name.Eq("Ada"))` filters a joined model's column once it is joined. A column of a generic type (`orm.JSON[T]`, `orm.Encrypted[T]`, `sql.Null[T]`) is generated as `any`, which gives up the value type check for that column. `Eq` with a nil pointer is `= NULL`, which never matches: use `IsNull`. A generated file lists every non-standard-library package under an explicit import alias (the last path element, or the one before a `/vN` suffix); a model in `package main` cannot be imported and is an error. `-check` ignores CRLF line endings and needs `-o` to name the file.

### Joins and projections

Joins name the other model as a type parameter, so its table and columns are checked against its metadata; nothing typed by hand becomes an identifier. The result stays `[]T`: read other tables' columns with `SelectAs` and `Scan[R]`, which maps result columns to the fields of any struct by name (a column the struct has no field for, or a repeated one, is an error, so a misspelt alias shows up in a test).

```go
type AuthorTotal struct {
	Name  string
	Total int64
}

rows, err := orm.QueryOf[Post](db).
	InnerJoin[User]("users.id", "=", "posts.user_id").
	SelectAs("users.name", "name").
	SelectExpr(orm.Sum("posts.views"), "total").
	GroupBy("users.name").
	Having(orm.Sum("posts.views"), ">", 100).
	Scan[AuthorTotal](ctx)
```

```sql
SELECT "users"."name" AS "name", SUM("posts"."views") AS "total"
FROM "posts" INNER JOIN "users" ON "users"."id" = "posts"."user_id"
GROUP BY "users"."name" HAVING SUM("posts"."views") > $1
```

- With a join, the model's own columns and its soft delete filter are qualified with the table. A joined model with `SoftDeletes` adds `deleted_at IS NULL` to its `ON`. Joining a table twice (self joins) is an error: use `orm.Raw`. `Update` and `Delete` refuse joins; filter with `WhereHas` or `WhereInQuery`.
- A join on a "many" relation repeats the rows of `T`: use `Distinct()` or `WhereHas` (an `EXISTS` subquery that never repeats rows and pages correctly). `Paginate` counts the joined rows unless the query is `Distinct()`; `CursorPaginate` refuses joins and `GroupBy`.
- `Get`, `First`, `Each` and `Count` refuse a query with `SelectAs`, `SelectExpr` or `GroupBy`: read it with `Scan`.
- `Sum` of no rows is zero (`COALESCE(SUM(col), 0)`, so use it on numeric columns); `Min`, `Max` and `Avg` of no rows are `orm.ErrNotFound`. Pick `V` to match the column: `SUM` and `AVG` of a `DECIMAL` column come back as text or float depending on the driver, so use `float64` or `string` there. A join on a "many" relation repeats rows before they are aggregated, so a `Sum` over it counts each parent once per child: aggregate over a `GroupBy`, or use `WhereHas`. Encrypted columns cannot be grouped or aggregated.
- A subquery renders its own SQL and is embedded in the outer statement, which is numbered once. `WhereInQuery` needs a subquery that selects one column; MySQL rejects a `Limit` inside `IN` and its error is returned as it comes. `WhereOuter` writes both sides with their table names, so the two models must be different tables.
- `orm.Raw[R]` rebinds `?` with `database.RebindChecked` (an unterminated literal, or `?` mixed with `$1` on PostgreSQL, is an error), reads one column into a scalar `R` or several into a struct, and never includes the whole query text in an error. Encrypted columns are not supported in `R`.

### Upsert

```go
err := orm.Upsert(ctx, db, &user, orm.OnConflict{Columns: []string{"email"}, Update: []string{"name"}})
err = orm.UpsertMany(ctx, db, users, orm.OnConflict{Columns: []string{"email"}, DoNothing: true})
```

One statement: `INSERT ... ON CONFLICT (cols) DO UPDATE SET ... RETURNING id` on PostgreSQL and SQLite, `INSERT ... ON DUPLICATE KEY UPDATE col = VALUES(col)` on MySQL and MariaDB. `Columns` is required; `Update` empty means every writable column except the key, `created_at` and `Columns`; `updated_at` is always refreshed. The generated key of the inserted or the existing row is written back (with `DoNothing` and an existing row it stays zero); `UpsertMany` writes no keys and batches under the dialect's parameter limit in one transaction. A clash on another unique index is `orm.ErrConflict`.

- **No hooks run**: `Upsert` and `UpsertMany` call none of `BeforeCreate`, `AfterCreate`, `BeforeUpdate` or `AfterUpdate`, because the database, not the ORM, decides between insert and update.
- MySQL and MariaDB have no conflict target: any unique index of the table triggers the update, so make `Columns` the columns of the unique index you mean. The generated id of an existing row comes from `LAST_INSERT_ID(id)` in the update clause (the ORM needs `clientFoundRows=true`, the `drivers/mysql` default).
- **Soft deletes**: `Upsert` never modifies or revives a row in the trash. `deleted_at` is never among the updated columns, the update is skipped for a trashed row, and the call returns `orm.ErrConflict` (`row is trashed; Restore it first`); call `orm.Restore` first. An encrypted column cannot be a conflict column.
- `UpsertMany`: two models with the same value in the conflict columns in one batch fail on PostgreSQL (a row cannot be affected twice); `Columns` must not be the generated key; one trashed match rolls the whole batch back.

`orm.Update` writes every column of the struct, so it overwrites what another writer changed since the struct was read; `orm.UpdateOnly(ctx, ex, &m, cols...)` writes only the named columns plus `updated_at` (unknown, key, `created_at` and `orm:"readonly"` columns are errors) and runs the same hooks. Both skip a row in the trash (a model with `orm.SoftDeletes` whose `deleted_at` is set): they return `orm.ErrNotFound`; change a trashed row with `QueryOf[T](ex).OnlyTrashed().Where(...).Update(ctx, orm.Set{...})`. A unique violation is `orm.ErrConflict`, detected by the driver error code (PostgreSQL SQLSTATE `23505`, SQLite result codes, MySQL error 1062), never by searching the message text; executors that wrap a `*database.DB` fall back to the text the built-in drivers produce.

`orm.Restore` returns `orm.ErrNotFound` when no trashed row matches the key (the record is live or missing). `Chunk(ctx, n, fn)` reads batches by keyset (`WHERE pk > last ORDER BY pk LIMIT n`), so `fn` may delete or update the rows it receives without making the next batch skip rows, as OFFSET paging would. The query must not have its own `OrderBy`, `Limit` or `Offset`, and a `Select` must keep the primary key; `Chunk` returns an error otherwise. To walk rows in another order, use `Each` with `OrderBy`.

`Paginate` and `CursorPaginate` clamp `perPage` to 1..`orm.DefaultMaxPerPage` (100), so a `per_page` passed through from a request cannot load a whole table in one call; the returned `PerPage` is the value used. `Query.MaxPerPage(n)` changes the bound for one query (`n < 1` is a query error). `api.Query` sets it to the `api.PerPage(def, max)` maximum, and admin list pages to `Resource.PerPage`.

Column names passed as strings are validated against the model's cached column set at query build time (unknown column → error before hitting the database). Raw SQL is the explicit escape hatch.

Raw SQL shared between dialects is written once with `?` and passed through `database.Rebind(ex.Dialect(), query)`: on PostgreSQL the n-th `?` becomes `$n`, elsewhere the query is unchanged. `?` inside string literals, quoted identifiers, dollar-quoted bodies and comments is left alone, and `??` is a literal `?` on PostgreSQL (jsonb operators). `DB.ExecContext`/`QueryContext` never rebind on their own, so queries already written with `$n` keep working.

The orm uses the same scanner (`internal/sqlscan`): `WhereRaw` counts placeholders with the rules `Rebind` renumbers with, so the two cannot disagree, and it rejects a fragment that ends inside a literal, identifier or comment (a trailing `--` comment too). It also rejects explicit `$n` parameters on PostgreSQL, which would bind another condition's value once the statement is renumbered; without an executor, `WhereRaw` lexes with SQLite's rules. Statements with a WHERE clause (select, `Query.Update`, `Query.Delete`) fail closed with an error if they end inside a literal or comment.

Dialect rules: every dialect skips `'…'` and `"…"`, `--` and `/* */` comments; PostgreSQL adds `E'…'`, nested comments and `$tag$` bodies (a backtick is an operator there); MySQL adds `` `…` ``, backslash escapes, `#` comments and needs whitespace after `--`; SQLite adds `` `…` `` and `[…]` identifiers. The lexer assumes the servers' default settings: `standard_conforming_strings` on (PostgreSQL), no `ANSI_QUOTES` or `NO_BACKSLASH_ESCAPES` (MySQL). MySQL executes the contents of `/*! */` and `/*+ */` comments, but they are skipped like any comment, so placeholders inside them are not supported. `Rebind` does not check for `$n` mixed with `?` or for unterminated SQL: write one style per query.

`database.RebindChecked(d, query)` returns `(string, error)` and fails closed instead: `ErrUnterminatedSQL` when the query ends inside a literal, quoted identifier, comment (a trailing `--` comment needs its newline) or dollar-quoted body, in any dialect, and `ErrMixedPlaceholders` when a PostgreSQL query has both `?` and `$n` (a query with only `$n` is returned unchanged). Use `RebindChecked` for queries assembled at runtime (concatenation, optional clauses), where an unbalanced quote would swallow the SQL appended after it; `Rebind` is enough for constant queries you have run in a test. The errors quote at most the first 80 bytes of the query, so a literal holding a secret is not copied whole into them; what of the query to log is the caller's call.

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

## ULIDs

Package `ulid` (stdlib only) generates ULIDs: 128-bit ids whose 26-character text form sorts by creation time. Use one where the id is handed to clients and rows are read newest-first — a sequential integer leaks how many rows a table holds, and a random `uuid.NewV4` key destroys index locality.

```go
ids := ulid.NewGenerator()   // one per app, wired like any other service
id := ids.New()              // "01KAB2M4R1R4Q5WJ2B6PM9NZ3K": 26 chars, sorts by creation millisecond
at, ok := ulid.TimeOf(id)    // the millisecond it was stamped with, UTC
if !ulid.Valid(r.PathValue("id")) { ... } // refuse an impossible id before the query
```

- Layout: a 48-bit Unix timestamp in milliseconds (the first 10 characters) and 80 bits from `crypto/rand` (the last 16), rendered in Crockford base32 — digits and upper-case letters without I, L, O or U, in ascending ASCII order, so the text sorts like the number it holds. The timestamp is visible to anyone holding the id.
- Ids are **strictly increasing per generator**, including several in the same millisecond (the random tail is incremented, not redrawn) and when the clock steps backwards, so an id never sorts before one already handed out. A `*ulid.Generator` is safe for concurrent use; its zero value is ready to use. `At(t)` stamps an explicit time, which is how tests pin it.
- There is no package-level generator: staying monotonic needs state, and the framework keeps no package-level mutable state. Hold the generator in whatever owns the writes (a service, a repository).
- `ulid.Valid` accepts only the canonical upper-case form `New` returns, so a `CHAR(26)` key has exactly one text form; the `ulid` [validation rule](validation.md) is the case-insensitive one, for ids from elsewhere.
- An id is a plain `string`: no `driver.Valuer` or `sql.Scanner` is involved, and it works as it is in a model field, a URL and a `Where` clause. `t.ULID("id")` in a migration makes the matching column (`CHAR(26)` everywhere).
- A time the 48-bit field cannot hold (before 1970, past year 10889) is clamped, not silently encoded as another time. `ulid.Length` is 26.

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

- Conventions: table = snake_case plural of type name; column = snake_case of field (`naming.Snake`: `UserID` → `user_id`, but a plural acronym splits, `ExternalIDs` → `external_i_ds`, so tag it `db:"external_ids"`); primary key `id`.
- Optional interfaces: `Table() string` (`orm.Tabler`) and `PrimaryKey() string` (`orm.Keyer`). The `db:"name"` tag renames a column, `db:"-"` skips a field, and `orm:"readonly"` keeps a column out of `Create`, `CreateMany` and `Update` while still reading it (generated or trigger-maintained columns; `Create` does not read the value back).
- Composite keys: `PrimaryKeys() []string` (`orm.CompositeKeyer`, wins over `Keyer`). `Update`, `Delete`, `ForceDelete` and `Restore` filter on every key column and `Update` never writes them; the key is never generated. APIs that address a row by one value (`Query.Find`, `Query.Chunk`, relations, `Attach`, `Reseal`, `Unique.Ignore`) return an error, `CursorPaginate` adds every key column as tie-breaker, `admin.NewResource` panics. `orm.PrimaryKeysOf[T]()` lists the columns. When a single-model write matches more than one row (a model declaring part of a composite key), it returns an error wrapping `orm.ErrKeyNotUnique`; the rows are already written, so a transaction is what rolls them back.
- Hooks: implement `BeforeCreate(ctx) error`, `AfterCreate`, `BeforeUpdate`, `AfterUpdate` or `BeforeDelete` on `*T` (`orm.BeforeCreator`, `orm.AfterCreator`, `orm.BeforeUpdater`, `orm.AfterUpdater`, `orm.BeforeDeleter`); an error aborts the write.
- Primary keys: an integer key (as in `orm.Model`) is assigned by the database, and `Create` reads it back (`RETURNING`, or `LastInsertId` on MySQL). For UUID or ULID keys, declare the fields yourself instead of embedding `orm.Model` (`ID uuid.UUID` or `ID string` with the `db:"id"` tag, plus `CreatedAt`/`UpdatedAt` for timestamps) and set the key before `Create`, e.g. with `uuid.NewV7()` or `ulid.NewGenerator().New()`.
- `Create` never invents a key it cannot read back: a non-integer key is inserted exactly as given, including an empty string, which the primary key constraint then rejects on the second row. A string key could be a ULID, a UUID or a format the app chose, and guessing one would be a behaviour nobody asked for; set it where the id is decided, or in a `BeforeCreate(ctx) error` hook on the model when every call site would otherwise repeat it.
- No Active Record: models never hold a DB handle; persistence functions take the executor (`db` or `tx`).
- Relations are never lazy, in every environment: reading one that was not eager-loaded with `With` returns `orm.ErrNotLoaded`.

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
- Key rotation: values sealed with a key from `APP_PREVIOUS_KEYS` still decrypt; saving the model re-encrypts every encrypted column with the current key. `orm.Reseal[T](ctx, db, batch)` walks the table by primary key (`orm.DefaultResealBatch` rows per query, soft-deleted rows included) and re-seals only stale values, compare-and-set; `vukem.ResealCommand(orm.Reseal[A], orm.Reseal[B])` registers it as `./app crypt:reseal [-batch n]`.
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
- Relation keys of a type that implements `driver.Valuer` (such as `uuid.UUID`) are matched by their column form, so `With`, `Attach` and `Sync` work with UUID keys stored as text or as the PostgreSQL `uuid` type; a `BINARY(16)` key is not supported.
- Nested eager loading: `With("Comments.User")` loads `Comments`, then `User` of every loaded comment (3 queries for N posts); `With("Comments", "Comments.User")` does not load `Comments` twice. Each level loads all its rows, with no limit per parent.
- `WithWhere[R](relation, fn)` narrows a relation's rows with the `Where` conditions and `OrderBy` of the query `fn` builds (`Limit`, `Offset`, joins, locks and projections are errors).
- `WithCount("Comments")` fills the `int64` field tagged `orm:"count:Comments"` (not a column) with one `GROUP BY` query per relation; it supports `HasMany` and `ManyToMany`.

## Migrations

```go
// database/migrations/20261016120000_create_users_table.go
package migrations

import "github.com/AlexGalhardo/vukem/schema"

func m20261016120000CreateUsersTable() schema.Migration {
	return schema.Migration{
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
		Down: func(s *schema.Schema) error { return s.DropIfExists("users") },
	}
}
```

- `database/migrations/registry.go` holds `func All() []schema.Migration`, which `make:migration` appends to; `bootstrap/app.go` passes it with `vukem.WithMigrations(migrations.All())` — no `init()` registration. `make:migration create_posts_table` writes a `Create` stub, `add_x_to_posts_table` (also `alter_`, `change_`, `update_…_table` and `…_from_`/`_in_`/`_on_…_table`) an alter stub, any other name an empty one.
- Column types: `ID`, `BigInteger`, `Integer`, `SmallInteger`, `String(name, n...)` (default 255), `Text`, `Boolean`, `Decimal(p, s)`, `Float`, `Date`, `Timestamp`, `Timestamps`, `SoftDeletes`, `JSON`, `UUID`, `ULID`, `Binary`, `Enum(vals...)`, `ForeignID`; modifiers `Nullable`, `Default` (Go value or `schema.Expr`), `Unique`, `Index`, `Primary`, `StoredAs(expr)` (stored generated column, `GENERATED ALWAYS AS (expr) STORED`; no `Default`; SQLite cannot add one to an existing table); foreign keys `Constrained(table...)` with `CascadeOnDelete`, `NullOnDelete` or `RestrictOnDelete`. There is no column comment modifier.
- `ForeignID("category_id").Constrained()` references `categories(id)`: the table is the column name without `_id`, pluralised with the ORM's table naming (`user_id` → `users`, `address_id` → `addresses`). Pass the table when it differs: `Constrained("people")`.
- `s.Table(name, fn)` alters a table with plain `ALTER TABLE` statements: add, rename and drop columns, add and drop indexes, add checks, and `Column.Change()` to alter an existing column's type, nullability and default (PostgreSQL: one `ALTER COLUMN` per aspect, no `USING`; MySQL: `MODIFY COLUMN`, which under a non-strict `sql_mode` rewrites existing NULLs to the type's zero value when the column becomes NOT NULL, so keep it strict or fill them first; `Unique`, `Index` and `Constrained` cannot be combined with it; SQLite has no `ALTER COLUMN`). SQLite cannot drop a column that has an index, a `UNIQUE` constraint or a foreign key, nor add or drop a check on an existing table: use `s.Rebuild(table, fn, opts...)`, which replaces the table with the full definition `fn` builds (as `Create` does) and keeps the rows, copying the columns present in both definitions by name (`schema.RenamedFrom(from, to)` renames one). It follows the SQLite procedure for [other kinds of schema changes](https://www.sqlite.org/lang_altertable.html): foreign keys off, one transaction, a table under a temporary name, `INSERT ... SELECT`, `DROP TABLE`, `RENAME`, the indexes again, `PRAGMA foreign_key_check` before the commit, foreign keys on. Foreign keys have to be off because `DROP TABLE` would otherwise delete the rows first and fire `ON DELETE CASCADE` on the child tables, and the pragma is a no-op inside a transaction; so a migration that rebuilds must set `NoTransaction: true` (Rebuild owns the transaction). A new `NOT NULL` column without a default on a table with rows, or a foreign key violation after the copy, fails before anything is committed. Indexes the old table had must be declared again; the `AUTOINCREMENT` counter is kept (an id that was used is not reused); views are not recreated, and a table with triggers is refused unless you pass `schema.DropTriggers()` (the old table takes them with it; write them again afterwards). Back up the database (`db:backup`) before such a migration in production.

```go
func m20261201090000DropLegacyFromUsers() schema.Migration {
	return schema.Migration{
		ID:            "20261201090000_drop_legacy_from_users",
		NoTransaction: true, // Rebuild manages its own transaction on SQLite
		Up: func(s *schema.Schema) error {
			if s.Dialect() != "sqlite" {
				return s.Table("users", func(t *schema.Table) { t.DropColumn("legacy_code") })
			}

			return s.Rebuild("users", func(t *schema.Table) {
				t.ID()
				t.String("name")
				t.String("email").Unique()
				t.Timestamps()
			})
		},
	}
}
```

- A migration that fails half-way on a database that commits DDL as it runs (MySQL, or any `NoTransaction` migration) says how many statements already ran: `schema: migration <id> failed after N statement(s); this database commits DDL as it runs, so they were not rolled back: ...`.
- `schema.Inspect(ctx, ex)` (and `schema.InspectTable`) reads tables, columns, primary keys, indexes and foreign keys from the catalog on every dialect, into a `schema.Snapshot`. `ColumnInfo.Kind` names the builder method that writes the type (`""` when none does); a type several methods share maps to the most common one (on SQLite `INTEGER` is `BigInteger` and `TEXT` is `Text`; MariaDB's JSON is `LONGTEXT`, so `Text`).
- `vukem migrate:check [--json]` replays the registered migrations on a model of the schema, without a database, and reports operations that cannot apply (a table created twice, a column added twice, a drop or rename of a column that does not exist, an index name used twice); `Schema.Raw` is opaque to it. When the database answers it also lists pending migrations that sort before the last one that ran (`out of order`, from another branch). It exits 1 on any finding. `migrate:status` marks those migrations `Pending (out of order)`; `migrate` still runs them.
- `vukem db:pull [-o dir] [-table name]... [-models] [-mark-applied]` writes one baseline migration from the existing database (a `Create` per table, referenced tables first, `Down` with `DropIfExists`) and, with `-models`, a struct per table in `app/models/<name>.go` (never overwriting). The framework's own tables and `migrations` are skipped. A column type the builder has no method for, and an index on an expression, are left out with a warning on stderr; a table or column name that is not a plain identifier is an error naming it, and an existing migration file is never overwritten. `-mark-applied` records the migration as applied once it is registered (rebuild and run it again if it is not).
- `vukem migrate:diff [-name n] [-o dir] [--delete item]... [--rename table.old:table.new]... [-check]` compares the models of `vukem.WithModels` with the database and writes a migration stub; it never applies anything. A missing table or column is created; a column the models no longer have is dropped only with `--delete table.column` (or turned into a rename with `--rename`); a table without a model is only a warning, and dropped with `--delete table`. Without consent for a destructive item nothing is written and it exits 1:

```text
migrate:diff: consent required for 1 destructive change(s):
  drop column users.nickname      pass --delete users.nickname
                                  or   --rename users.nickname:users.<new>
```

  On SQLite, dropping a column that has an index is written as one `s.Rebuild` with the full definition that carries every change of that table (other drops, added columns as nullable, renames through `schema.RenamedFrom`), never followed by `s.Table`. The definition comes from the models, so a database default, a CHECK, a composite foreign key, an expression index and triggers are not reproduced: the file lists them in a comment above the `Rebuild` and the command warns on stderr. `-check` writes nothing and exits 1 on any difference. It does not compare the length of strings, indexes or foreign keys (the models do not carry them), and a renamed table is a drop plus a create.

- In production `vukem migrate` refuses a pending migration that would `DROP TABLE` or `DROP COLUMN` (read from the statements it generates, `Schema.Raw` included, also inside a combined `ALTER TABLE`; a table rebuilt in place is not a drop, but a `Rebuild` that leaves out a column the table has counts) unless `--allow-destructive` is given (`schema.Destructive`). `TRUNCATE` and `DELETE FROM` are not gated, and a `HasTable` guard around a drop is not evaluated (the drop is reported).
- `t.Primary(cols...)` (or `Column.Primary()`) emits a `PRIMARY KEY (...)` table constraint for UUID and composite keys: `Create` only, at least one declared column, not with `ID()`, not twice.
- `t.Check(name, expr)` adds a named CHECK constraint (in `Create` on every dialect; in `s.Table` on PostgreSQL and MySQL only, SQLite fails the migration); `t.DropCheck(name)` removes one (MySQL 8.0.19+ / MariaDB 10.2+).
- `t.Index(cols...)` / `t.UniqueIndex(cols...)` return an `*Index`; `.Where(expr)` makes it partial (`CREATE [UNIQUE] INDEX … WHERE expr`) on PostgreSQL and SQLite. MySQL/MariaDB have no partial indexes, so `Where` fails the migration there instead of creating a full index. `.Using(method)` picks a PostgreSQL index method (`btree`, `hash`, `gin`, `gist`, `brin`, `spgist`; `CREATE INDEX … USING gin (col)`), e.g. `t.Index("tags").Using("gin")` on a `JSON` (`JSONB`) column; an unknown method, or any method on SQLite or MySQL, fails the migration. Operator classes such as `jsonb_path_ops` and expression indexes still need `s.Raw`.
- `s.Raw(sql, args...)` for anything else (data migrations, views, triggers). With args, `?` placeholders are rebound for the dialect with `database.RebindChecked` (`$n` on PostgreSQL), so one data migration runs everywhere; a query mixing `?` and `$n` or ending inside a literal fails. Without args the statement runs as written, so PostgreSQL's jsonb `?` operators in DDL keep working.
- Tracking table `migrations(id, batch, checksum, ran_at)`. Each migration runs in its own transaction (MySQL commits DDL implicitly, so a failed one is not rolled back there), unless `Migration.NoTransaction` (for `CREATE INDEX CONCURRENTLY`, `VACUUM`; recorded after `Up` succeeds; on PostgreSQL the run fails, without recording, while the current schema holds an `INVALID` index no backend is building, which a failed `CREATE INDEX CONCURRENTLY` leaves and a retried `IF NOT EXISTS` would skip: `TestMigrateRefusesInvalidIndexAfterNoTransaction`). `Up`, `Rollback`, `Reset` and `Fresh` hold a lock for the whole run: `pg_advisory_lock` (key: FNV-64a of `vukem:<table>`) or `GET_LOCK('vukem:<table>')` on a pinned connection; failing to take it fails the run; SQLite takes none. The checksum is the SHA-256 of the SQL `Up` generates; `migrate:status` shows `Ran (CHANGED since applied)` when it no longer matches and `Ran (missing from code)` for recorded ids without a migration.
- Commands: `migrate`, `migrate:rollback [--step N]`, `migrate:reset`, `migrate:fresh`, `migrate:status [--json]`, `migrate:check [--json]`, `migrate:diff`, `db:pull`, `make:migration <name>`; `migrate` also takes `--allow-destructive` (production). `migrate:reset` and `migrate:fresh` need `--force` in production; `migrate:fresh` has no `--seed`: run `vukem db:seed` after it.
- `schema.Record(dialect, up)` returns the statements a migration's `Up` generates for a dialect without a database (the SQL the checksum hashes), so DDL can be reviewed or diffed in a test. Queries are not run while recording: `s.HasTable` and `s.HasColumn` report `false`, so `Record` shows the statements for an empty database.

## Tables the framework owns

Five tables belong to vukem's own database-backed stores, not to the app. Check this list before naming a table: `jobs` is the obvious name for a domain table too, and the framework took it first.

| Table | Owner | Rename with | Migration id |
| --- | --- | --- | --- |
| `jobs` | database queue (`QUEUE_CONNECTION=database`) | `QUEUE_TABLE` | `20261018000000_vukem_create_queue_tables` |
| `failed_jobs` | database queue | `QUEUE_FAILED_TABLE` | same migration |
| `sessions` | database session store (`SESSION_DRIVER=database`) | `session.WithTable` | `20261018000001_vukem_create_sessions_table` |
| `cache` | database cache store (`CACHE_STORE=database`) | `CACHE_TABLE` | `20261018000002_vukem_create_cache_table` |
| `webhook_deliveries` | `webhook.SQLDeliveryLog` | the `table` argument of `webhook.NewSQLDeliveryLog` | `20261018000003_vukem_create_webhook_deliveries_table` |

Plus `migrations(id, batch, checksum, ran_at)`, the migrator's own tracking table.

- The defaults keep their names: `jobs` and `failed_jobs` are not prefixed, because renaming them would break every app that already has those tables (the queue would find an empty `vukem_jobs` and the jobs in flight would never run). `QUEUE_TABLE` and `QUEUE_FAILED_TABLE` exist for collision avoidance, not as a customisation knob: an app that wants its own `jobs` table sets them (`QUEUE_TABLE=vukem_jobs`, `QUEUE_FAILED_TABLE=vukem_failed_jobs`) **before the first boot**, since moving them later leaves the pending jobs behind in the old table. `stores.Options` then needs the same names, so the migration manages the tables the stores read.
- `stores.Tables(stores.Options{})` returns the list above at run time, and `go doc github.com/AlexGalhardo/vukem/schema/stores` prints it.

### Managing them with migrate

By default each store runs its own `CREATE TABLE IF NOT EXISTS` at boot, so `migrate` reports `Nothing to migrate.` while those tables exist and no migration describes them. Register the framework's migrations to put them under `migrate` instead:

```go
// bootstrap/app.go
import "github.com/AlexGalhardo/vukem/schema/stores"

vukem.New(
	vukem.WithMigrations(slices.Concat(
		stores.Migrations(stores.Options{}), // framework tables
		migrations.All(),                    // the app's own
	)),
)
```

- Opting in is the only change needed: `migrate:status` then lists the five tables, `migrate:fresh` round-trips them and `migrate:rollback` drops them. An app that does not register them keeps the boot-time creation, so nothing breaks on upgrade.
- Both paths run the same statements (one DDL builder per store), for SQLite, PostgreSQL and MySQL. `TestMigrationMatchesCreateTableDDL` in each package diffs the migration's recorded SQL against the boot-time DDL for all three dialects, and `TestMigrationSchemaMatchesCreateTable` builds one database by each path and compares SQLite's own catalog (columns, types, nullability, defaults, primary keys, indexes).
- On a database where the tables already exist (the app opts in after the stores created them), the migration records itself and changes nothing — no conflicting `CREATE TABLE`. Rolling it back afterwards still drops the table.
- `stores.Options` must repeat the names the stores read at run time; the ids are fixed, so a migration is recorded once whichever way the list is composed. Changing a table name after the migration ran makes `migrate:status` show `Ran (CHANGED since applied)`, because the recorded checksum covers the old name.
- An app that uses only some stores can pass the per-package lists instead: `queue.Migrations(queue.DatabaseOptions{…})`, `session.Migrations(table)`, `cache.Migrations(table)`, `webhook.Migrations(table)`.
- The stores still call their `CreateTable` at boot even when the migration ran: the statements are `IF NOT EXISTS`, so it is a no-op, but the database user needs `CREATE` rights at boot anyway. A least-privilege user without `CREATE` is not supported yet.

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

- `factory.Faker` (no dependency): `Name`, `FirstName`, `LastName`, `Username`, `Email` (reserved example domains), `Word(s)`, `Sentence`, `Paragraph`, `Slug`, `URL`, `UUID`, `Int`, `Float`, `Bool`, `Time`, `factory.Pick(f, ...)`, `factory.PickWeighted(f, factory.Weighted{Item, Weight}...)` (odds proportional to weight; panics like `Pick` on no items, and on a negative or all-zero weight), `Seq()`. It uses `math/rand/v2` and must never generate secrets.
- Locales: `factory.LocaleEN()` and `factory.LocalePTBR()` (embedded data, no dependency), set with `Factory.Locale(l)` or `factory.NewFaker(seed).WithLocale(l)`. They localize names, words and sentences and add `Company`, `ProductTitle`, `ProductDescription`, `Category`, `StreetAddress`, `City`, `State`, `PostalCode` (ZIP or CEP), `Address` (city and state match), `EmailFor(name)` and `FakeCPF` (CPF format whose first check digit is always wrong, so never a real person's and rejected by every standard validator). **The zero Locale is not English**: it keeps the original mixed data set (mostly Brazilian first names, lorem ipsum) so `NewFaker(seed)` and `Seed(n)` output from before locales is unchanged; only the methods that set never had (commerce, company, address) use English there. Pass `LocaleEN()` for consistent English.
- Factories are values: `State`, `Count`, `Seed`, `AfterCreate` return copies.
- `Create` inserts one row at a time (each model's `AfterCreate` runs right after its insert). `CreateMany` inserts through `orm.CreateMany`: multi-row `INSERT`s split under the dialect's bound-parameter limit, ids written back (`RETURNING` on PostgreSQL and SQLite, one `INSERT` per row on MySQL), all in one transaction (joined on a `*database.Tx`), then the `AfterCreate` hooks in order inside it. Given a `*database.DB` it opens the transaction and an error rolls back every row; given a `*database.Tx` it joins it and the caller owns the rollback. 1000 users on a SQLite file: `Create` 65 ms, `CreateMany` 20 ms (`go test ./factory -bench Create`); the gap grows with network round trips.
- Seeders implement `vukem.Seeder` (`Seed(ctx, db) error`); `vukem make:seeder Users` creates one and registers it in `database/seeders/registry.go`, which the app passes to `vukem.WithSeeders(seeders.All()...)`. `vukem db:seed` runs them in order; tests call `app.Seed(ctx)`. Seeders that need config, the logger or provider services implement `vukem.AppSeeder` (`SeedApp(ctx, app) error`, or `vukem.AppSeederFunc`) and are passed to `vukem.WithAppSeeders`; both kinds run in one list in option order, after `db:seed` booted the app. A seeder's `Name() string`, if any, names it in output and errors; `vukem.NamedAppSeeder(name, fn)` names a function.
- `vukem db:seed DemoCatalog Users` (or `--class DemoCatalog`, repeatable) runs only those seeders, in argument order; a name matches case-insensitively, also without the `Seeder` suffix, and an unknown or ambiguous name fails before anything runs. Flags after the names go to the seeders that run: a seeder implementing `vukem.FlagSeeder` (`SeedFlags(fs *flag.FlagSet)`) defines them, bound to fields of its pointer receiver (`vukem db:seed DemoCatalog --fresh --count 500`). Unknown flags, stray arguments and a `--class` without a name fail; seeders defining the same flag all receive it; `--force`, `--class`, `--help` and `-h` belong to `db:seed`: a seeder cannot redefine them, and `--force`/`--class` cannot be values of a seeder flag. A seeder that defines flags must be registered as a pointer (`&seeders.DemoCatalog{}`), otherwise `db:seed` fails. `--force` may come anywhere, and the production check runs after parsing, before any seeder. `app.Seed(ctx)` runs every seeder with its flag defaults.
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

`Field(name)` reports the error under `name` instead of the column, and the built-in message names it too: `Exists[models.Team]("id", x).Field("team_id")` fails with "The selected team_id is invalid.". The built-in messages follow the request locale (`validation.unique`, `validation.exists`, with `{field}` from `validation.attributes.<field>`, the `Field` name when set; see [i18n](subsystems.md#i18n)); a `Message` override is used as is.

Empty values are skipped (`required` belongs to the schema). Columns are validated against the model, so input never chooses a column.

## MongoDB

`go get github.com/AlexGalhardo/vukem/drivers/mongo` (official driver v2). A separate, typed API, not the SQL builder:

```go
m, err := mongo.Open(ctx, os.Getenv("MONGO_URL"), os.Getenv("MONGO_DATABASE")) // empty MONGO_DATABASE: database from the URI path
app.AddHealthCheck("mongo", m.Ping)                                              // reported by /up?checks=1

users := mongo.Collection[models.User](m, "users")
u, err := users.FindOne(ctx, bson.D{{Key: "email", Value: e}}) // mongo.ErrNotFound (404) when missing
for u, err := range users.Find(ctx, bson.D{{Key: "active", Value: true}}, options.Find().SetLimit(50)) { ... }
id, err := users.Insert(ctx, u)
matched, modified, err := users.UpdateOne(ctx, filter, bson.D{{Key: "$set", Value: bson.D{{Key: "name", Value: n}}}})
totals, err := mongo.Aggregate[Total](ctx, users, pipeline)
```

`mongo.ErrNotFound` answers 404 when a handler returns it, like `orm.ErrNotFound` (it implements `HTTPStatus()`, which `errs.Status` reads), and wraps the official driver's `mongo.ErrNoDocuments`. `mongo.ErrDuplicate` answers 409 when a write violates a unique index. `All` returns an empty slice, not nil, when nothing matches, so it encodes as JSON `[]`.

Also `All`, `Count`, `InsertMany`, `UpdateMany`, `ReplaceOne`, `DeleteOne`, `DeleteMany` and `Raw()` for indexes and change streams. **Never pass decoded request JSON as a filter**: operators such as `$where` or `$ne` would come from the client; build filters from validated fields.

- **Database name.** `Open(ctx, uri, db)` uses `db` when it is non-empty, else the URI path (`mongodb://host:27017/app`); with neither, `Open` fails. Managed providers often give a URL without a path (Railway's `MONGO_URL` is `mongodb://mongo:<password>@mongodb.railway.internal:27017`), so set `MONGO_DATABASE=app` instead of editing the URL.
- **authSource.** A database in the URI path is also the default `authSource`: appending `/app` to a provider URL makes the driver authenticate against `app` instead of `admin`, and login fails. Keep the path empty and pass the database name, or add `?authSource=admin` (`mongodb://user:pw@host:27017/app?authSource=admin`).
- **Boot without the server.** `Open` pings the primary, so the app does not boot while MongoDB is down; a malformed URI always fails. `mongo.OpenLazy(uri, db)` validates the URI and database name without contacting the server, which lets the process start and report the outage through `m.Ping` in a readiness check (`app.AddHealthCheck`). The client starts its topology-monitoring goroutines immediately (even before the server is up), so close it on shutdown: `app.OnShutdown(m.Close)`. Server selection waits 10 s unless the URI sets `serverSelectionTimeoutMS`.

## Redis

`drivers/redis` provides cache, session and queue stores, a shared rate limiter, locks and pub/sub; it works with Redis 7+ and Valkey 8+. See [subsystems.md](subsystems.md#redis).

## Not supported yet

Checked against the code at v0.40.0. Until one of these lands, the workaround is raw SQL (`WhereRaw`, `db.QueryContext`, `s.Raw`).

| Area | Missing today | Workaround |
| --- | --- | --- |
| Query shape | `RIGHT`/`FULL` joins, self joins and table aliases, CTEs, `UNION`, window functions | `orm.Raw` |
| Relations | Polymorphic and has-many-through relations, a limit per parent in `WithWhere` | Load the second level with its own query |
| Writes | Find-or-create (two hidden queries; `Upsert` covers most uses), automatic retry on a serialization failure | `db.ExecContext` inside `db.Tx` |
| Migrations | Table rebuild on SQLite, changing a column's type, reading an existing schema, generating a migration from a diff, detecting migrations that conflict across branches | `s.Raw`; write the migration by hand |
| Databases | Turso / libSQL | A SQLite file |

## Lessons from other ORMs

What was read while auditing this package, and what the ORM takes from each. The links are the pages the statement comes from; pre-release documentation is marked, because it can change before a stable release.

### Drizzle ORM (1.0 release candidate)

- "If you know SQL, you know Drizzle" and "Drizzle always outputs exactly 1 SQL query" ([overview](https://orm.drizzle.team/docs/overview)). Adopted as the first two [design principles](#design-principles).
- It keeps two query APIs: a SQL-shaped one (joins, subqueries, CTEs) and a relational one that loads nested relations and filters by them ([select](https://orm.drizzle.team/docs/select), [relational queries](https://orm.drizzle.team/docs/rqb-v2)). The gap table above is mostly the SQL-shaped half.
- Relations are declared once, in one place, with explicit `from` and `to` columns ([relations](https://orm.drizzle.team/docs/relations-v2)). The `orm:"fk:…"` tag is the same idea; nested loading is the part still missing.
- Migrations: `generate` writes SQL from a schema diff, `pull` introspects a database, `check` finds migrations that conflict across branches, and a data-loss statement needs `--force` ([kit overview](https://orm.drizzle.team/docs/kit-overview), [push](https://orm.drizzle.team/docs/drizzle-kit-push)). Each migration lives in its own folder with its snapshot ([upgrade to v1](https://orm.drizzle.team/docs/upgrade-v1)).
- Transactions nest through savepoints and take an isolation level ([transactions](https://orm.drizzle.team/docs/transactions)).
- Drizzle Studio is a local data browser with filtering, inline editing, a SQL console with explain and import/export ([studio](https://orm.drizzle.team/drizzle-studio/overview)); it listens on `127.0.0.1:4983` by default ([drizzle-kit studio](https://orm.drizzle.team/docs/drizzle-kit-studio)).

### Prisma ORM (8 release candidate)

- One description of the data is the source for types, queries and migrations ([ORM overview](https://www.prisma.io/docs/orm)). Here the Go struct plays that part for queries and the migration builder for the schema; a diff between the two is the missing link.
- Destructive migration steps need consent per item: the planner asks about each dropped model or field, and a non-interactive run fails instead of guessing ([changelog 2026-10-09](https://prisma.io/changelog/2026-10-09)).
- There is no reset command and no down-migration file; a mistake is undone with a new migration ([rollbacks and recovery](https://www.prisma.io/docs/orm/migrations/rollbacks-and-recovery), [coming from Prisma ORM 7](https://www.prisma.io/docs/orm/coming-from-prisma-orm-7)). Vukem keeps `Down` and `migrate:reset`, both behind `--force` in production.
- Prisma 8 removes soft delete, model lifecycle hooks and client extensions from the core ([coming from Prisma ORM 7](https://www.prisma.io/docs/orm/coming-from-prisma-orm-7)). The lesson taken is restraint: the five hooks here stay five, and nothing is added that runs code the call site does not show.
- Relation filters (`some`, `every`, `none`) are part of the query API ([relations and joins](https://www.prisma.io/docs/orm/fundamentals/relations-and-joins)).
- Prisma Studio browses and edits data with filters, relation navigation and tabs ([studio](https://www.prisma.io/studio)).

### GORM

GORM is the Go ORM most readers arrive from, so its documented pitfalls are the things this package avoids.

- Updating with a struct skips zero values, so `false`, `0` and `""` are silently not written ([update](https://gorm.io/docs/update.html)). `orm.Update` writes every column and `orm.Set` writes exactly the keys given.
- An update or delete without a condition is blocked with `ErrMissingWhereClause` ([update](https://gorm.io/docs/update.html), [delete](https://gorm.io/docs/delete.html)). Worth copying: an unfiltered `Query.Update` or `Query.Delete` should have to say so.
- `Order`, `Select`, `Group`, `Having`, `Table` and `Joins` take strings that are not escaped ([security](https://gorm.io/docs/security.html)). Here every column name is validated against the model, and raw fragments go through a lexer.
- Auto-migration never drops a column and the documentation points to versioned migrations for production ([migration](https://gorm.io/docs/migration.html)). Vukem has only versioned migrations.
- Worth copying: nested and conditional preloading ([preload](https://gorm.io/docs/preload.html)), savepoints ([transactions](https://gorm.io/docs/transactions.html)), upsert ([create](https://gorm.io/docs/create.html)) and the generics API with generated, typed column helpers ([the generics way](https://gorm.io/docs/the_generics_way.html), [GORM CLI](https://gorm.io/cli/index.html)).

### Turso

- Turso recommends its Rust rewrite, Turso Database, for new projects; libSQL is the maintained fork of SQLite it replaces ([libSQL](https://docs.turso.tech/libsql.md), [turso repository](https://github.com/tursodatabase/turso)). The rewrite has not reached 1.0.
- The Go driver without cgo is `turso.tech/database/tursogo` ([package](https://pkg.go.dev/turso.tech/database/tursogo)); `go-libsql` needs cgo ([repository](https://github.com/tursodatabase/go-libsql)) and `libsql-client-go` is deprecated ([repository](https://github.com/tursodatabase/libsql-client-go)). Vukem builds without cgo, so only the first one fits.
- It works local-first: a local file that pushes to and pulls from Turso Cloud ([sync usage](https://docs.turso.tech/sync/usage.md)).
