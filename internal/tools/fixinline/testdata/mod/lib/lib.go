// Package lib holds //go:fix inline wrappers whose parameter names match the
// argument names callers typically use.
package lib

import (
	"context"
	nethttp "net/http"
)

// Bundle is a test type.
type Bundle struct{}

// Config is a test type.
type Config struct{ N int }

// Middleware is the replacement.
func Middleware(b *Bundle, cfg Config) (func(nethttp.Handler) nethttp.Handler, error) {
	_ = b

	return func(h nethttp.Handler) nethttp.Handler { return h }, nil
}

// NewMiddleware keeps the parameter names of Middleware: b and cfg.
//
// Deprecated: use Middleware.
//
//go:fix inline
func NewMiddleware(b *Bundle, cfg Config) (func(nethttp.Handler) nethttp.Handler, error) {
	return Middleware(b, cfg)
}

// Swap forwards its arguments in the other order.
//
// Deprecated: use Pair.
//
//go:fix inline
func Swap(cfg Config, b *Bundle) string { return Pair(b, cfg) }

// Pair is the replacement of Swap.
func Pair(b *Bundle, cfg Config) string { _ = b; return "" }

// Do is a method wrapper.
//
// Deprecated: use Call.
//
//go:fix inline
func (b *Bundle) Do(ctx context.Context, path string, args ...string) error {
	return b.Call(ctx, path, args...)
}

// Call is the replacement of Do.
func (b *Bundle) Call(ctx context.Context, path string, args ...string) error {
	_, _, _ = ctx, path, args

	return nil
}

// Log has no results.
//
// Deprecated: use Logf.
//
//go:fix inline
func Log(msg string) { Logf("%s", msg) }

// Logf is the replacement of Log.
func Logf(format string, args ...any) { _, _ = format, args }
