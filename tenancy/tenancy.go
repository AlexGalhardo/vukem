// Package tenancy isolates rows per tenant (a workspace, a team, an
// account) in one shared database. [Middleware] resolves the tenant of a
// request and checks that the signed-in user belongs to it, [From] and
// [With] carry it in the context, [Scope] filters queries by it and
// [Assign] stamps it on new rows. Every step fails closed: no tenant means
// no rows and no insert.
package tenancy

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/AlexGalhardo/vukem/auth"
	"github.com/AlexGalhardo/vukem/errs"
	"github.com/AlexGalhardo/vukem/orm"
	"github.com/AlexGalhardo/vukem/web"
)

// ID is a tenant id. It is a string, like auth.Authenticatable.AuthID, so
// integer, UUID and ULID keys all fit.
type ID string

// ErrNoTenant is returned by [Assign] and [Switch] when there is no tenant.
var ErrNoTenant = errors.New("tenancy: no tenant")

// ErrNoSession is returned by [Switch] without session middleware.
var ErrNoSession = errors.New("tenancy: no session (add session middleware to the router stack)")

type ctxKey struct{}

// With returns a context carrying the tenant id, for jobs, commands and
// tests. An empty id carries no tenant.
func With(ctx context.Context, id ID) context.Context {
	return context.WithValue(ctx, ctxKey{}, id)
}

// From returns the tenant of ctx. ok is false when there is none.
func From(ctx context.Context) (ID, bool) {
	id, _ := ctx.Value(ctxKey{}).(ID)

	return id, id != ""
}

// Resolver finds the tenant a request addresses. An error or an empty ID
// means there is no tenant.
type Resolver func(r *http.Request) (ID, error)

// Config configures [Middleware].
type Config struct {
	// Resolve finds the tenant of the request ([Session], [Header],
	// [PathValue] or your own).
	Resolve Resolver
	// Member reports whether the user (the auth.Principal id) belongs to
	// the tenant. An error denies.
	Member func(ctx context.Context, userID string, tenant ID) (bool, error)
}

// Middleware resolves the tenant, checks the membership of the
// authenticated user and stores the tenant in the request context. A
// missing user, a missing tenant, a non-member and any resolver or Member
// error all answer 404 (never 403), so a tenant id cannot be probed. Mount
// it after the auth middleware, and after the session middleware for
// [Session]. It panics when Resolve or Member is nil.
func Middleware(cfg Config) func(http.Handler) http.Handler {
	if cfg.Resolve == nil || cfg.Member == nil {
		panic("tenancy: Config.Resolve and Config.Member are required")
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := r.Context()

			id, ok := admit(ctx, cfg, r)
			if !ok {
				web.NewCtx(w, r, web.ServicesFrom(ctx)).HandleError(errs.ErrNotFound)
				return
			}

			next.ServeHTTP(w, r.WithContext(With(ctx, id)))
		})
	}
}

func admit(ctx context.Context, cfg Config, r *http.Request) (ID, bool) {
	_, user, ok := auth.Principal(ctx)
	if !ok {
		return "", false
	}

	id, err := cfg.Resolve(r)
	if err != nil {
		slog.WarnContext(ctx, "tenancy: resolve failed", "error", err)
		return "", false
	}

	if id == "" {
		return "", false
	}

	member, err := cfg.Member(ctx, user, id)
	if err != nil {
		slog.WarnContext(ctx, "tenancy: membership check failed", "error", err)
		return "", false
	}

	return id, member
}

// Session resolves the tenant from the session value under key.
func Session(key string) Resolver {
	return func(r *http.Request) (ID, error) {
		s := web.SessionFrom(r.Context())
		if s == nil {
			return "", ErrNoSession
		}

		v, _ := s.Get(key).(string)

		return ID(v), nil
	}
}

// Header resolves the tenant from a request header.
func Header(name string) Resolver {
	return func(r *http.Request) (ID, error) { return ID(r.Header.Get(name)), nil }
}

// PathValue resolves the tenant from a route wildcard (r.PathValue).
func PathValue(name string) Resolver {
	return func(r *http.Request) (ID, error) { return ID(r.PathValue(name)), nil }
}

// Switch stores id in the session under key, the value [Session] reads.
// It does not check membership: the next request still goes through
// [Middleware], so a forged or stale value is a 404.
func Switch(r *http.Request, key string, id ID) error {
	if id == "" {
		return ErrNoTenant
	}

	s := web.SessionFrom(r.Context())
	if s == nil {
		return ErrNoSession
	}

	s.Put(key, string(id))

	return nil
}

// Scope returns a query scope adding "column = tenant", for
// q.Scope(tenancy.Scope[Task](ctx, "workspace_id")). With no tenant in ctx
// the query matches nothing instead of every row.
func Scope[T any](ctx context.Context, column string) func(orm.Query[T]) orm.Query[T] {
	id, ok := From(ctx)

	return func(q orm.Query[T]) orm.Query[T] {
		if !ok {
			return q.WhereIn(column)
		}

		return q.Where(column, string(id))
	}
}

// Scoped is a model with a tenant column.
type Scoped interface {
	// SetTenant stores the tenant id in the model's tenant column.
	SetTenant(ID)
}

// Assign sets the tenant of ctx on m. Call it from the model's
// BeforeCreate hook so the column always holds the context tenant, whatever
// the request sent; without a tenant it returns [ErrNoTenant] and the insert
// is refused. orm.Upsert runs no hooks: call Assign before it.
func Assign(ctx context.Context, m Scoped) error {
	id, ok := From(ctx)
	if !ok {
		return ErrNoTenant
	}

	m.SetTenant(id)

	return nil
}
