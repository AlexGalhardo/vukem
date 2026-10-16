package vite

import (
	"context"
	"net/http"
)

type ctxKey struct{}

// NewContext returns ctx carrying v, for components that render tags
// without the view engine (templ).
func NewContext(ctx context.Context, v *Vite) context.Context {
	return context.WithValue(ctx, ctxKey{}, v)
}

// FromContext returns the Vite stored in ctx, or nil.
func FromContext(ctx context.Context) *Vite {
	v, _ := ctx.Value(ctxKey{}).(*Vite)
	return v
}

// Middleware stores v in every request context (see FromContext), so templ
// components can call FromContext(ctx).Tags.
func (v *Vite) Middleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(NewContext(r.Context(), v)))
		})
	}
}
