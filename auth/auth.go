// Package auth authenticates requests: guards (session for browsers,
// access tokens for APIs), user providers, middleware and password hashing
// (auth/hash).
//
// A guard ([SessionGuard], [TokenGuard], or jwt.Guard in auth/jwt) loads
// users through a [UserProvider] such as [ORMProvider]. Routes opt in with
// the [Required], [Guest], [Optional], [Verified] and [Ability] middleware,
// and handlers read the user with [User]. Apps scaffold the whole flow
// with `vukem install:auth`.
//
// See https://github.com/AlexGalhardo/vukem/blob/main/docs/authentication.md.
package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/AlexGalhardo/vukem/database"
	"github.com/AlexGalhardo/vukem/errs"
	"github.com/AlexGalhardo/vukem/orm"
	"github.com/AlexGalhardo/vukem/web"
)

// Authenticatable is a user model.
type Authenticatable interface {
	// AuthID is the user's primary key as a string.
	AuthID() string
	// AuthPassword is the stored password hash.
	AuthPassword() string
}

// UserProvider loads users. ByCredentials never checks the password; the
// guard does, in constant time.
//
// ByID must report a missing user as ErrUserNotFound (wrap it, or the
// orm.ErrNotFound / sql.ErrNoRows the lookup returned): the session guard
// forgets the session only then, and answers any other error with 503
// while keeping the session, so returning a generic error for "not found"
// leaves a deleted user's session failing instead of logging out.
type UserProvider[U Authenticatable] interface {
	ByID(ctx context.Context, id string) (U, error)
	ByCredentials(ctx context.Context, creds map[string]string) (U, error)
}

// PasswordUpdater is implemented by providers that can store a rehashed
// password after login (hash.Hasher.NeedsRehash).
type PasswordUpdater[U Authenticatable] interface {
	UpdatePassword(ctx context.Context, u U, hash string) error
}

// Errors returned by guards.
var (
	// ErrUnauthenticated is 401 (WWW-Authenticate is added by middleware).
	ErrUnauthenticated = &errs.HTTPError{Status: http.StatusUnauthorized, Message: "Unauthenticated."}
	// ErrUserNotFound is returned by providers for unknown users.
	ErrUserNotFound = errors.New("auth: user not found")
)

// credentialsError is the 422 shown for a failed login, on the identifier
// field so forms display it next to the email input. The message never
// says which part was wrong (user enumeration).
func credentialsError(field string) error {
	return errs.NewValidation().Add(field, "These credentials do not match our records.")
}

// ORMProvider loads users of model U with the ORM: ByID by primary key,
// ByCredentials by every credential except "password" (column names are
// validated against U). Its PasswordColumn (default "password") receives
// rehashed passwords.
type ORMProvider[U Authenticatable] struct {
	ex             database.Executor
	PasswordColumn string
}

// NewORMProvider returns the default provider for model U.
func NewORMProvider[U Authenticatable](ex database.Executor) *ORMProvider[U] {
	return &ORMProvider[U]{ex: ex, PasswordColumn: "password"}
}

// ByID implements UserProvider.
func (p *ORMProvider[U]) ByID(ctx context.Context, id string) (U, error) {
	return p.first(ctx, orm.QueryOf[U](p.ex).Where(pkColumn[U](), id))
}

// ByCredentials implements UserProvider.
func (p *ORMProvider[U]) ByCredentials(ctx context.Context, creds map[string]string) (U, error) {
	q := orm.QueryOf[U](p.ex)

	n := 0

	for col, v := range creds {
		if col == "password" {
			continue
		}

		q = q.Where(col, v)
		n++
	}

	if n == 0 {
		var zero U
		return zero, errors.New("auth: no identifying credentials")
	}

	return p.first(ctx, q)
}

// UpdatePassword implements PasswordUpdater.
func (p *ORMProvider[U]) UpdatePassword(ctx context.Context, u U, hash string) error {
	_, err := orm.QueryOf[U](p.ex).Where(pkColumn[U](), u.AuthID()).Update(ctx, orm.Set{p.PasswordColumn: hash})
	return err
}

func (p *ORMProvider[U]) first(ctx context.Context, q orm.Query[U]) (U, error) {
	u, err := q.First(ctx)
	if errors.Is(err, orm.ErrNotFound) {
		return u, ErrUserNotFound
	}

	return u, err
}

func pkColumn[U any]() string {
	if k, ok := any(new(U)).(orm.Keyer); ok {
		return k.PrimaryKey()
	}

	return "id"
}

// Checker is the non-generic part of a guard, used by middleware.
type Checker interface {
	// Name identifies the guard ("web", "api").
	Name() string
	// Check reports whether c is authenticated, caching the user in c.
	Check(c *web.Ctx) bool
}

type userKey struct{ guard string }

type principalKey struct{}

// cache stores the user for this request in c's context, and the
// principal Principal reports (cleared when u is nil, on logout).
func cache(c *web.Ctx, guard string, u any) {
	ctx := context.WithValue(c.Context(), userKey{guard}, u)

	p := ""
	if a, ok := u.(Authenticatable); ok {
		p = guard + ":" + a.AuthID()
	}

	c.SetRequest(c.Request().WithContext(context.WithValue(ctx, principalKey{}, p)))
}

// Principal returns "<guard>:<user id>" for the user most recently
// authenticated on this request by any guard, for code that must key data
// by caller without knowing the guard (api.Idempotency).
func Principal(ctx context.Context) (string, bool) {
	p, _ := ctx.Value(principalKey{}).(string)
	return p, p != ""
}

func cached[U any](c *web.Ctx, guard string) (U, bool) {
	u, ok := c.Context().Value(userKey{guard}).(U)
	return u, ok
}

// SetUser records u as the user guard authenticated for this request, for
// guards implemented outside this package (auth/jwt).
func SetUser(c *web.Ctx, guard string, u any) { cache(c, guard, u) }

// UserFrom returns the user authenticated by guard from a request context
// (authz gates, view functions).
func UserFrom[U any](ctx context.Context, guard string) (U, error) {
	if u, ok := ctx.Value(userKey{guard}).(U); ok {
		return u, nil
	}

	var zero U

	return zero, ErrUnauthenticated
}

// User returns the user authenticated by guard for this request (after
// Required or a guard's Check ran).
func User[U Authenticatable](c *web.Ctx, guard string) (U, error) {
	if u, ok := cached[U](c, guard); ok {
		return u, nil
	}

	var zero U

	return zero, fmt.Errorf("%w (guard %q did not authenticate this request)", ErrUnauthenticated, guard)
}
