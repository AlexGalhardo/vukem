// Package grpc serves gRPC, gRPC-Web and Connect APIs built with
// connect-go (connectrpc.com/connect) from the Vukem router. connect-go
// handlers are plain net/http handlers, so the three protocols share one
// port, the router's middleware (request id, logging, rate limiting, auth
// guards) and the app container.
//
//	path, h := greetv1connect.NewGreetServiceHandler(svc, grpc.HandlerOptions(grpc.Options{})...)
//	grpc.Mount(r, path, h).Use(grpc.Required(apiGuard))
//
// Mount makes every Vukem error written by route middleware (401 from a
// guard, 429 from the rate limiter) a protocol error with the matching
// code, and HandlerOptions maps the errors returned by RPC methods the same
// way (Error). [Health] and [Reflection] add the standard health and
// reflection services, and package grpctest calls a service over every
// protocol in tests.
//
// See https://github.com/AlexGalhardo/vukem/blob/main/docs/api.md.
package grpc

import (
	"net/http"

	"connectrpc.com/connect"

	"github.com/AlexGalhardo/vukem/auth"
	"github.com/AlexGalhardo/vukem/router"
	"github.com/AlexGalhardo/vukem/web"
)

// Mounter is a router or route group.
type Mounter interface {
	Handle(pattern string, h http.Handler) *router.Route
}

// Mount registers a connect-go handler (gRPC, gRPC-Web and Connect) at
// path, as returned by the generated New<Service>Handler. Middleware added
// with Use on the returned route runs inside the protocol error bridge
// (Errors), so its rejections reach clients as RPC errors.
func Mount(r Mounter, path string, h http.Handler) *router.Route {
	return r.Handle(path, flushable(h)).Use(Errors())
}

// Errors makes the Vukem errors written by the middleware after it (through
// the error handler, as auth, rate limiting and body limits do) RPC errors
// in the caller's protocol. Mount installs it; add it yourself to a group
// whose middleware should answer RPC clients the same way. Requests that
// are not RPCs keep the app's error handler.
func Errors() router.Middleware {
	ew := connect.NewErrorWriter()

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !ew.IsSupported(r) {
				next.ServeHTTP(w, r)
				return
			}

			s := *web.ServicesFrom(r.Context())
			s.Errors = func(c *web.Ctx, err error) {
				cerr := ErrorContext(c.Context(), err)
				if logsCause(err, cerr) {
					c.Log().ErrorContext(c.Context(), "rpc middleware error", "code", cerr.Code().String(), "err", err)
				}

				_ = ew.Write(c.Writer(), c.Request(), cerr)
			}

			next.ServeHTTP(w, r.WithContext(web.WithServices(r.Context(), &s)))
		})
	}
}

// Required lets an RPC through when any guard authenticates it, like
// auth.Required, and answers Unauthenticated otherwise (never a login
// redirect, even in apps with sessions). Methods read the user with
// auth.UserFrom(ctx, guard).
func Required(guards ...auth.Checker) router.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			c := web.NewCtx(w, r, web.ServicesFrom(r.Context()))

			for _, g := range guards {
				if g.Check(c) {
					next.ServeHTTP(w, c.Request())
					return
				}
			}

			c.HandleError(auth.ErrUnauthenticated)
		})
	}
}

// flushable gives connect-go a Flush that reaches the connection. Middleware
// writers either lack Flush (web.ResponseWriter only supports
// http.ResponseController, and streaming handlers refuse writers without
// http.Flusher) or stop at the next writer that lacks it, which buffers
// streams and deadlocks bidirectional ones.
func flushable(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.ServeHTTP(flushWriter{w}, r)
	})
}

type flushWriter struct{ http.ResponseWriter }

// Flush flushes every writer of the chain, outermost first, so wrappers
// that act on flush (session commit, compression) run before the bytes
// reach the connection.
func (w flushWriter) Flush() {
	rw := w.ResponseWriter
	for {
		if f, ok := rw.(http.Flusher); ok {
			f.Flush()
		}

		u, ok := rw.(interface{ Unwrap() http.ResponseWriter })
		if !ok {
			return
		}

		rw = u.Unwrap()
	}
}

func (w flushWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
