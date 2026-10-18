// Package authz decides what an authenticated user may do: gates (named
// abilities) and policies (abilities on a model type). Decisions fail
// closed: an ability or policy that was never registered denies.
//
//	gate := authz.New(func(ctx context.Context) (*models.User, error) {
//		u, err := auth.UserFrom[models.User](ctx, "web") // the session guard stores a value
//		return &u, err
//	})
//	gate.Define("edit-settings", func(ctx context.Context, u *models.User) bool { return u.Role == "admin" })
//	gate.Register(authz.For[*models.Post](policies.PostPolicy{}))
//	r.Services().Authorizer = gate
//
//	if err := c.Authorize("update", post); err != nil { return err } // 403 / 404
//
// See https://github.com/AlexGalhardo/vukem/blob/main/docs/authentication.md.
package authz

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"sync"

	"github.com/AlexGalhardo/vukem/errs"
	"github.com/AlexGalhardo/vukem/internal/naming"
)

// Result is an authorization decision.
type Result struct {
	allowed  bool
	notFound bool
	message  string
}

// Allow grants the ability.
func Allow() Result { return Result{allowed: true} }

// Deny refuses with 403 and an optional message.
func Deny(message ...string) Result {
	r := Result{}
	if len(message) > 0 {
		r.message = message[0]
	}

	return r
}

// DenyAsNotFound refuses with 404, hiding that the resource exists.
func DenyAsNotFound() Result { return Result{notFound: true} }

// Allowed reports whether the result grants the ability.
func (r Result) Allowed() bool { return r.allowed }

func (r Result) err() error {
	switch {
	case r.allowed:
		return nil
	case r.notFound:
		return &errs.HTTPError{Status: http.StatusNotFound, Message: "Not Found."}
	case r.message != "":
		return errs.New(http.StatusForbidden, r.message)
	default:
		return ErrForbidden
	}
}

// ErrForbidden is the default denial (403).
var ErrForbidden = &errs.HTTPError{Status: http.StatusForbidden, Message: "This action is unauthorized."}

// ErrNoUser is returned (401) when the user resolver finds nobody.
var ErrNoUser = &errs.HTTPError{Status: http.StatusUnauthorized, Message: "Unauthenticated."}

// errUnregistered marks fail-closed denials, which are also logged.
var errUnregistered = errors.New("authz: no rule registered")

// Gate holds abilities and policies for users of type U.
type Gate[U any] struct {
	user func(ctx context.Context) (U, error)

	mu        sync.RWMutex
	abilities map[string]func(ctx context.Context, u U) Result
	policies  map[reflect.Type]map[string]policyMethod[U]
	before    []func(ctx context.Context, u U, ability string) *Result
}

type policyMethod[U any] func(ctx context.Context, u U, subject reflect.Value) Result

// New returns a gate; user resolves the current user from the request
// context (e.g. auth.UserFrom).
func New[U any](user func(ctx context.Context) (U, error)) *Gate[U] {
	return &Gate[U]{
		user:      user,
		abilities: map[string]func(context.Context, U) Result{},
		policies:  map[reflect.Type]map[string]policyMethod[U]{},
	}
}

// Define registers a named ability.
func (g *Gate[U]) Define(ability string, fn func(ctx context.Context, u U) bool) {
	g.DefineResult(ability, func(ctx context.Context, u U) Result {
		if fn(ctx, u) {
			return Allow()
		}

		return Deny()
	})
}

// DefineResult registers a named ability returning a Result (for 404s or
// custom messages).
func (g *Gate[U]) DefineResult(ability string, fn func(ctx context.Context, u U) Result) {
	g.mu.Lock()
	defer g.mu.Unlock()

	g.abilities[ability] = fn
}

// Before runs before every check; a non-nil result decides (super-admins).
func (g *Gate[U]) Before(fn func(ctx context.Context, u U, ability string) *Result) {
	g.mu.Lock()
	defer g.mu.Unlock()

	g.before = append(g.before, fn)
}

// Policy is a policy bound to a subject type, created by For.
type Policy struct {
	subject reflect.Type
	impl    any
}

// For binds policy to subject type T (usually a model pointer): ability
// "update" calls policy.Update(ctx, user, subject T) returning Result or
// bool. Methods without the subject parameter (Create(ctx, user)) serve
// type-level abilities: c.Authorize("create", (*models.Post)(nil)).
// A policy bound to *T also serves T values (a template ranging over
// []models.Post): the method receives a pointer to a copy.
func For[T any](policy any) Policy { return Policy{subject: reflect.TypeFor[T](), impl: policy} }

// Register validates and adds policies. Signatures are checked here, at
// boot, so a typo fails fast instead of denying at runtime.
func (g *Gate[U]) Register(policies ...Policy) error {
	userType := reflect.TypeFor[U]()
	ctxType := reflect.TypeFor[context.Context]()
	resultType := reflect.TypeFor[Result]()
	boolType := reflect.TypeFor[bool]()

	g.mu.Lock()
	defer g.mu.Unlock()

	for _, p := range policies {
		pv := reflect.ValueOf(p.impl)
		methods := map[string]policyMethod[U]{}

		for i := range pv.NumMethod() {
			m := pv.Type().Method(i)
			fn := pv.Method(i)
			ft := fn.Type()

			if ft.NumIn() < 2 || ft.NumIn() > 3 || ft.In(0) != ctxType || ft.In(1) != userType ||
				ft.NumOut() != 1 || (ft.Out(0) != resultType && ft.Out(0) != boolType) {
				continue // helper methods are allowed on policies
			}

			if ft.NumIn() == 3 && ft.In(2) != p.subject {
				return fmt.Errorf("authz: %s.%s takes %s, policy is for %s", pv.Type(), m.Name, ft.In(2), p.subject)
			}

			methods[naming.Camel(m.Name)] = makeMethod[U](fn)
		}

		if len(methods) == 0 {
			return fmt.Errorf("authz: %s has no policy methods for %s", pv.Type(), userType)
		}

		g.policies[p.subject] = methods
	}

	return nil
}

func makeMethod[U any](fn reflect.Value) policyMethod[U] {
	withSubject := fn.Type().NumIn() == 3

	return func(ctx context.Context, u U, subject reflect.Value) Result {
		args := []reflect.Value{reflect.ValueOf(ctx), reflect.ValueOf(&u).Elem()}
		if withSubject {
			args = append(args, subject)
		}

		switch out := fn.Call(args)[0].Interface().(type) {
		case Result:
			return out
		case bool:
			if out {
				return Allow()
			}
		}

		return Deny()
	}
}

// Check decides ability for u on subject (nil for gate abilities).
func (g *Gate[U]) Check(ctx context.Context, u U, ability string, subject any) (Result, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()

	for _, b := range g.before {
		if r := b(ctx, u, ability); r != nil {
			return *r, nil
		}
	}

	if subject == nil {
		fn, ok := g.abilities[ability]
		if !ok {
			return Deny(), fmt.Errorf("%w: ability %q", errUnregistered, ability)
		}

		return fn(ctx, u), nil
	}

	sv := reflect.ValueOf(subject)

	methods, ok := g.policies[sv.Type()]
	if !ok && sv.Kind() != reflect.Pointer {
		// A value (a template ranging over []Post) uses the *Post policy on a
		// shallow copy: its fields are protected, data behind slices, maps and
		// pointers is shared.
		if methods, ok = g.policies[reflect.PointerTo(sv.Type())]; ok {
			p := reflect.New(sv.Type())
			p.Elem().Set(sv)
			sv = p
		}
	}

	if !ok {
		return Deny(), fmt.Errorf("%w: policy for %s", errUnregistered, sv.Type())
	}

	m, ok := methods[ability]
	if !ok {
		return Deny(), fmt.Errorf("%w: ability %q on %s", errUnregistered, ability, sv.Type())
	}

	return m(ctx, u, sv), nil
}

// Allows reports whether u may perform ability on subject.
func (g *Gate[U]) Allows(ctx context.Context, u U, ability string, subject any) bool {
	r, err := g.Check(ctx, u, ability, subject)
	return err == nil && r.allowed
}

// Authorize implements web.Authorizer: it resolves the current user from
// ctx and returns nil, ErrNoUser (401), a 403 or a 404. Unregistered
// abilities and policies deny (fail closed) and are reported in the
// returned error's cause for logs.
func (g *Gate[U]) Authorize(ctx context.Context, ability string, subject any) error {
	u, err := g.user(ctx)
	if err != nil {
		return ErrNoUser
	}

	r, err := g.Check(ctx, u, ability, subject)
	if err != nil {
		return errs.Wrap(http.StatusForbidden, err)
	}

	return r.err()
}
