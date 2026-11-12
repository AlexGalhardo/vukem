# Multi-tenancy

Package `tenancy` (core) isolates rows per tenant (a workspace, a team, an
account) in one shared database. Every row carries a tenant column; the
package resolves the tenant per request, checks that the signed-in user
belongs to it and scopes queries. It fails closed: no tenant means no rows.

Decision record: [brainstorming.md](brainstorming.md).

## API

```go
package tenancy

// ID is a tenant id. A string like auth.Authenticatable.AuthID, so it fits
// integer, UUID and ULID keys alike.
type ID string

// Resolver finds the tenant a request addresses. An error or an empty ID
// means "no tenant".
type Resolver func(r *http.Request) (ID, error)

type Config struct {
	Resolve Resolver
	// Member reports whether the user (auth.Principal id) belongs to the tenant.
	Member func(ctx context.Context, userID string, tenant ID) (bool, error)
}

func Middleware(cfg Config) func(http.Handler) http.Handler // panics when Resolve or Member is nil

func From(ctx context.Context) (ID, bool)
func With(ctx context.Context, id ID) context.Context // jobs, commands, tests

func Session(key string) Resolver     // session value under key
func Header(name string) Resolver     // request header
func PathValue(name string) Resolver  // r.PathValue(name)
func Switch(r *http.Request, key string, id ID) error // store the tenant in the session

func Scope[T any](ctx context.Context, column string) func(orm.Query[T]) orm.Query[T]

type Scoped interface{ SetTenant(ID) }
func Assign(ctx context.Context, m Scoped) error // ErrNoTenant without a tenant
```

## Behaviour

- `Middleware` must run after the auth middleware (`auth.Required`) and, for
  the `Session` resolver, after the session middleware.
- Without an authenticated user, a tenant, or a membership, the answer is
  404, never 403, so a tenant id cannot be probed. A resolver or `Member`
  error is also 404 (and is logged).
- `Scope[T](ctx, "workspace_id")` adds `WHERE workspace_id = ?`. With no tenant
  in `ctx` it adds `WHERE workspace_id IN ()`, which matches nothing. Use it
  on every read, update and delete: `orm.QueryOf[Task](db).Scope(tenancy.Scope[Task](ctx, "workspace_id"))`.
- Inserts: implement `SetTenant` on the model and call `tenancy.Assign` from
  `BeforeCreate(ctx)`. The hook runs after input binding, so the column always
  holds the context tenant, whatever the request sent. Without a tenant the
  insert fails with `ErrNoTenant`. `orm.Upsert` runs no hooks: call `Assign`
  yourself before it.
- `Switch` writes the id into the session; the next request still goes
  through the membership check, so a forged or stale value is a 404.

## Example

```go
r.Group("/w/{workspace}", func(r router.Router) {
	r.Use(auth.Required(web), tenancy.Middleware(tenancy.Config{
		Resolve: tenancy.PathValue("workspace"),
		Member:  isMember,
	}))
})

func (t *Task) SetTenant(id tenancy.ID) { t.WorkspaceID = string(id) }

func (t *Task) BeforeCreate(ctx context.Context) error { return tenancy.Assign(ctx, t) }
```

## Not in scope

Database-per-tenant, schema-per-tenant, Postgres row-level security,
cross-tenant admin queries (use an unscoped query on purpose) and roles
inside a tenant (use `authz`).
