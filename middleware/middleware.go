// Package middleware provides Vukem's built-in net/http middleware. Every
// constructor returns a func(http.Handler) http.Handler, so they compose with
// any other middleware. Errors are rendered through web.Fail, which uses the
// router's error handler (problem+json, HTML or htmx fragments).
//
// The set covers [Recover], [RequestID], [Logger], [Timeout], [BodyLimit],
// [MethodOverride], [JSONOnly], [SecureHeaders] (CSP with nonces, HSTS),
// [CORS], [Gzip], [Static] and [Maintenance]. vukem.DefaultMiddleware
// installs the secure web stack; vukem.APIMiddleware the API one.
//
// See https://github.com/AlexGalhardo/vukem/blob/main/docs/http.md.
package middleware

import (
	"context"
	"crypto/rand"
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"
	"slices"
	"strings"
	"time"

	"github.com/AlexGalhardo/vukem/errs"
	"github.com/AlexGalhardo/vukem/web"
)

// Middleware wraps an http.Handler.
type Middleware = func(http.Handler) http.Handler

// Recover converts panics into 500 responses rendered by the error handler
// and logs the stack trace server-side only (A10). http.ErrAbortHandler is
// re-raised so net/http can abort the connection.
func Recover() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				p := recover()
				if p == nil {
					return
				}

				if p == http.ErrAbortHandler { //nolint:errorlint // sentinel compared by identity, as net/http does
					panic(p)
				}

				// Skip this deferred func and runtime.gopanic: the stack starts at
				// the panicking call.
				err := errs.WithStackSkip(fmt.Errorf("panic: %v", p), 2)
				log := web.ServicesFrom(r.Context()).Log
				if log == nil {
					log = slog.Default()
				}

				log.ErrorContext(r.Context(), "panic recovered", "err", err, "stack", string(debug.Stack()),
					"request_id", web.RequestID(r.Context()))

				web.Fail(w, r, errs.Wrap(http.StatusInternalServerError, err))
			}()

			next.ServeHTTP(w, r)
		})
	}
}

// RequestIDHeader is the header carrying the request id.
const RequestIDHeader = "X-Request-ID"

// RequestID reuses a well-formed incoming X-Request-ID or generates one,
// stores it in the context (web.RequestID) and echoes it in the response.
func RequestID() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id := r.Header.Get(RequestIDHeader)
			if !validRequestID(id) {
				id = rand.Text()
			}

			w.Header().Set(RequestIDHeader, id)
			next.ServeHTTP(w, r.WithContext(web.WithRequestID(r.Context(), id)))
		})
	}
}

// validRequestID accepts short ids of safe characters, so attacker input
// cannot inject into logs (A09).
func validRequestID(id string) bool {
	if id == "" || len(id) > 128 {
		return false
	}

	for _, c := range id {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-', c == '_', c == '.':
		default:
			return false
		}
	}

	return true
}

// Logger logs one structured line per request (method, path, status,
// duration, size, request id). 5xx log at ERROR, 4xx at WARN.
func Logger(log *slog.Logger) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()

			rw, ok := w.(*web.ResponseWriter)
			if !ok {
				rw = &web.ResponseWriter{ResponseWriter: w}
			}

			next.ServeHTTP(rw, r)

			status := rw.Status()
			if status == 0 {
				status = http.StatusOK
			}

			level := slog.LevelInfo

			switch {
			case status >= 500:
				level = slog.LevelError
			case status >= 400:
				level = slog.LevelWarn
			}

			log.LogAttrs(r.Context(), level, "request",
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
				slog.Int("status", status),
				slog.Duration("duration", time.Since(start)),
				slog.Int64("bytes", rw.Size()),
				slog.String("request_id", web.RequestID(r.Context())),
			)
		})
	}
}

// Timeout sets a deadline on the request context. Handlers and queries that
// respect the context stop when it expires.
func Timeout(d time.Duration) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx, cancel := context.WithTimeout(r.Context(), d)
			defer cancel()

			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// BodyLimit caps request bodies at n bytes; larger bodies fail with 413.
func BodyLimit(n int64) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.ContentLength > n {
				web.Fail(w, r, errs.ErrRequestTooLarge)
				return
			}

			next.ServeHTTP(w, web.LimitBody(w, r, n))
		})
	}
}

// MethodOverride lets HTML forms send PUT, PATCH and DELETE through a
// hidden "_method" field on POST. Multipart forms put it in the action's
// query string instead ("?_method=PUT"), so their body is never buffered.
// Register it as server middleware so routing sees the overridden method.
func MethodOverride() Middleware {
	allowed := []string{http.MethodPut, http.MethodPatch, http.MethodDelete}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodPost && isFormContent(r) {
				var m string

				// Multipart bodies are left unread so uploads can stream
				// (package upload): their override comes from the query.
				if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
					m = r.URL.Query().Get("_method")
				} else {
					r = web.LimitBody(w, r, web.DefaultMaxBody)
					m = r.PostFormValue("_method")
				}

				if m = strings.ToUpper(m); slices.Contains(allowed, m) {
					r.Method = m
				}
			}

			next.ServeHTTP(w, r)
		})
	}
}

func isFormContent(r *http.Request) bool {
	ct := r.Header.Get("Content-Type")
	return strings.HasPrefix(ct, "application/x-www-form-urlencoded") || strings.HasPrefix(ct, "multipart/form-data")
}

// JSONOnly makes a route group JSON-only: write requests must send JSON
// (415 otherwise) and errors render as problem+json.
func JSONOnly() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Accept") == "" || r.Header.Get("Accept") == "*/*" {
				r.Header.Set("Accept", "application/json")
			}

			hasBody := r.ContentLength > 0 || len(r.TransferEncoding) > 0
			if hasBody && r.Method != http.MethodGet && r.Method != http.MethodHead {
				if !web.IsJSONMediaType(r.Header.Get("Content-Type")) {
					web.Fail(w, r, errs.ErrUnsupportedMediaType)
					return
				}
			}

			next.ServeHTTP(w, r)
		})
	}
}
