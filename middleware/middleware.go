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
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
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
					"panic", true,
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
// duration, size, request id, peer address and client IP). peer is the
// direct connection (RemoteAddr host); ip is the client resolved through
// web.Services.TrustedProxies, as c.IP() returns it. 5xx log at ERROR, 4xx
// at WARN.
func Logger(log *slog.Logger) Middleware { return LoggerWith(log, LoggerOptions{}) }

// LoggerOptions configures LoggerWith.
type LoggerOptions struct {
	// TruncateIP logs the /24 network of IPv4 addresses and the /48 of
	// IPv6 ones in peer and ip (203.0.113.0, 2001:db8:1234::) instead of
	// the address, for data-minimising logs (LOG_TRUNCATE_IP). Rate limits
	// and c.IP() keep the full address.
	TruncateIP bool
}

// LoggerWith is Logger with options.
func LoggerWith(log *slog.Logger, o LoggerOptions) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()

			rw, ok := w.(*web.ResponseWriter)
			if !ok {
				rw = &web.ResponseWriter{ResponseWriter: w}
			}

			ctx, extra := web.WithLogAttrs(r.Context())
			next.ServeHTTP(rw, r.WithContext(ctx))

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

			peer, _, err := net.SplitHostPort(r.RemoteAddr)
			if err != nil {
				peer = r.RemoteAddr
			}

			ip := web.NewCtx(nil, r, web.ServicesFrom(r.Context())).IP()
			if o.TruncateIP {
				peer, ip = truncateIP(peer), truncateIP(ip)
			}

			attrs := append([]slog.Attr{
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
				slog.Int("status", status),
				slog.Duration("duration", time.Since(start)),
				slog.Int64("bytes", rw.Size()),
				slog.String("request_id", web.RequestID(r.Context())),
				slog.String("peer", peer),
				slog.String("ip", ip),
			}, extra.Attrs()...)

			log.LogAttrs(r.Context(), level, "request", attrs...)
		})
	}
}

// truncateIP returns the /24 (IPv4) or /48 (IPv6) network address of s;
// a value that is not an IP is returned as is.
func truncateIP(s string) string {
	a, err := netip.ParseAddr(s)
	if err != nil {
		return s
	}

	a = a.Unmap().WithZone("")

	bits := 48
	if a.Is4() {
		bits = 24
	}

	p, _ := a.Prefix(bits)

	return p.Addr().String()
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

// BodyLimit caps request bodies at n bytes; reading a larger body fails
// with *http.MaxBytesError, which the web helpers (Input, BindJSON, Parse,
// uploads) answer with 413. A declared Content-Length over the limit fails
// on the first read, before any byte is consumed.
//
// A later BodyLimit, web.LimitBody or upload.Rules.MaxSize on the same
// request replaces the limit instead of nesting under it, so the
// router-wide default set by vukem.DefaultMiddleware can be raised or
// lowered per route. The check runs when the body is read, against the
// innermost limit: a handler that never reads the body gets no 413.
func BodyLimit(n int64) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
					// Keep a limit set earlier (BodyLimit as server middleware):
					// replacing it with the default would let the form parse
					// read far more than the app allows.
					if _, limited := web.BodyLimitOf(r); !limited {
						r = web.LimitBody(w, r, web.DefaultMaxBody)
					}

					// PostFormValue drops the ParseForm error, and net/http
					// then treats the form as parsed. Remember a body-limit
					// error so web.Parse answers 413 instead of 422.
					if err := r.ParseForm(); err != nil {
						if _, tooBig := errors.AsType[*http.MaxBytesError](err); tooBig {
							r = web.NoteBodyError(r, err)
						}
					}

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
