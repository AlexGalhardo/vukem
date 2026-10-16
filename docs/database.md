# Databases

Connections, the generic query builder, models, relations, migrations, factories and seeders. Decisions `D1`–`D5` in [brainstorming.md](brainstorming.md).

## Support matrix

| Database | Driver | Module | Phase |
| --- | --- | --- | --- |
| SQLite | `modernc.org/sqlite` (pure Go) | core | Prototype |
| PostgreSQL | `jackc/pgx/v5/stdlib` | `drivers/postgres` | ALPHA |
| Redis | `redis/go-redis/v9` | `drivers/redis` | BETA |
| MongoDB | `go.mongodb.org/mongo-driver/v2` | `drivers/mongo` | BETA |

MySQL/MariaDB arrive in BETA as `drivers/mysql` (B.8.5).

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
- Postgres registers its dialect when the user imports `drivers/postgres` and passes `postgres.Driver()` to the database provider — explicit, no `init()`.

### PostgreSQL

```sh
go get github.com/AlexGalhardo/golang-framework/drivers/postgres
```

```go
// bootstrap/app.go
lumek.New(append([]lumek.Option{
	lumek.WithDatabaseDrivers(postgres.Driver()), // postgres:// and postgresql:// URLs
	// ...
}, opts...)...)
```

```sh
DATABASE_URL=postgres://app:secret@localhost:5432/app?sslmode=disable
```

Everything above the driver is dialect-neutral: the same models, queries, migrations and `SESSION_DRIVER=database` work on SQLite and Postgres. `database/dbtest.Run(t, db)` is the conformance suite every dialect must pass (schema, migrations, ORM, transactions); `drivers/postgres` runs it against `postgres:17` with `task services:up` and `go test -tags=integration`.

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

## Query builder

Generic, built on `database/sql`, dialect-aware placeholders (`?` vs `$1`), always parameterised.

```go
// Select
users, err := orm.Query[models.User](db).
	Where("active", true).
	WhereIn("role", []string{"admin", "editor"}).
	OrderBy("created_at", orm.Desc).
	Limit(20).
	Get(ctx)                                  // []User

user, err := orm.Query[models.User](db).Find(ctx, 42)   // ErrNoRows → 404 in handlers
n, err := orm.Query[models.User](db).Where("active", true).Count(ctx)
ok, err := orm.Query[models.User](db).Where("email", e).Exists(ctx)
emails, err := orm.Query[models.User](db).Pluck[string](ctx, "email") // Go 1.27 generic method

// Streaming with range-over-func
for u, err := range orm.Query[models.User](db).Each(ctx) {
	if err != nil { return err }
	process(u)
}

// Writes
err = orm.Create(ctx, db, &user)            // fills ID, timestamps
err = orm.Update(ctx, db, &user)            // all columns (explicit)
err = orm.Query[models.User](db).Where("id", id).Update(ctx, orm.Set{"name": "x"})
err = orm.Delete(ctx, db, &user)            // soft delete if model opts in

// Transactions
err = db.Tx(ctx, func(tx *database.Tx) error {
	if err := orm.Create(ctx, tx, &order); err != nil { return err }
	return orm.Create(ctx, tx, &item)
}) // commit on nil, rollback on error or panic
```

| Group | Methods |
| --- | --- |
| Where | `Where(col, val)`, `Where(col, op, val)`, `OrWhere`, `WhereIn`, `WhereNotIn`, `WhereNull`, `WhereNotNull`, `WhereBetween`, `WhereRaw(sql, args...)`, grouped `WhereGroup(func(q))` |
| Shape | `Select(cols...)`, `Distinct()`, `Join/LeftJoin`, `GroupBy`, `Having`, `OrderBy`, `Limit`, `Offset` |
| Read | `Get`, `First`, `Find`, `Count`, `Exists`, `Pluck[V]`, `Each`, `Chunk(n, fn)` |
| Paginate | `Paginate(ctx, page, perPage)` → `Page[T]{Items, Total, Page, PerPage, LastPage}`; `CursorPaginate` (BETA) |
| Scopes | `Scope(fns ...func(*Query[T]) *Query[T])` |
| Raw | `orm.Raw[T](db, sql, args...)` scans into `T` |

Column names passed as strings are validated against the model's cached column set at query build time (unknown column → error before hitting the database). Raw SQL is the explicit escape hatch.

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

## Relations (ALPHA)

```go
type Post struct {
	orm.Model
	UserID int64
	Author orm.BelongsTo[User]   `rel:"user_id"`
	Tags   orm.ManyToMany[Tag]   `rel:"post_tag"`
	Comments orm.HasMany[Comment]
}

posts, err := orm.Query[Post](db).With("Author", "Comments").Get(ctx) // 1 + N queries batched by IN
author, err := post.Author.Get()                                    // ErrNotLoaded if not eager-loaded
```

Supported: `HasOne`, `HasMany`, `BelongsTo`, `ManyToMany` (pivot table, `Attach/Detach/Sync`). Eager loading is explicit; there is no lazy loading.

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
- `s.Raw(sql)` for anything else.
- Tracking table `migrations(id, batch, ran_at)`; each migration runs in a transaction where the dialect allows DDL in transactions.
- Commands: `migrate`, `migrate:rollback [--step N]`, `migrate:reset`, `migrate:fresh [--seed]`, `migrate:status`, `make:migration create_posts_table` (name prefix `create_`/`add_`/`alter_` picks the stub).

## Factories and seeders (ALPHA)

```go
var UserFactory = factory.New(func(f *factory.Faker) models.User {
	return models.User{Name: f.Name(), Email: f.Email(), Password: f.Password()}
}).State("admin", func(u *models.User) { u.Role = "admin" })

users, err := UserFactory.Count(3).State("admin").Create(ctx, db)
u := UserFactory.Make()                       // not persisted
```

- `factory.Faker` is a small built-in generator (names, emails, text, numbers, dates) backed by `math/rand/v2` with a seed — no faker dependency.
- Seeders: `type DatabaseSeeder struct{}` with `Run(ctx, db) error`; `db:seed [--class Name]`; seeders can be limited to environments.

## MongoDB (BETA)

Separate API, not the SQL builder:

```go
users := mongo.Collection[models.User](app.Mongo, "users")
u, err := users.FindOne(ctx, bson.D{{"email", e}})
for u, err := range users.Find(ctx, bson.D{{"active", true}}) { ... }
```

## Redis (BETA)

`drivers/redis` provides a connection manager and stores for cache, session, queue, rate limiting and locks. Works with Valkey.
