package bench

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
	"github.com/labstack/echo/v4"
	echomw "github.com/labstack/echo/v4/middleware"

	"github.com/AlexGalhardo/vukem"
	"github.com/AlexGalhardo/vukem/config"
	"github.com/AlexGalhardo/vukem/middleware"
	"github.com/AlexGalhardo/vukem/router"
	"github.com/AlexGalhardo/vukem/web"
)

type user struct {
	ID    int64  `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
}

// discardLog formats every line as JSON (the realistic cost) and drops it;
// slog.DiscardHandler would skip the formatting being measured.
func discardLog() *slog.Logger {
	return slog.New(slog.NewJSONHandler(io.Discard, nil)) //nolint:sloglint // formatting cost is the point
}

// staticCSP mirrors middleware.DefaultCSP without the per-request nonce,
// which chi and echo do not offer.
const staticCSP = "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; " +
	"font-src 'self'; connect-src 'self'; object-src 'none'; base-uri 'self'; frame-ancestors 'none'; " +
	"form-action 'self'"

// BenchmarkFullRequest serves GET /users/{id} returning a small JSON body
// through each framework's production-style stack: request id, panic
// recovery, security headers and one structured log line per request.
func BenchmarkFullRequest(b *testing.B) {
	run(b, []impl{
		{"nethttp_bare", nethttpBare()},
		{"vukem_stack", vukemStack(b)},
		{"vukem_default", vukemDefault(b)},
		{"chi", chiStack()},
		{"echo", echoStack()},
	}, "/users/42")
}

func lookupUser(id string) (user, bool) {
	n, err := strconv.ParseInt(id, 10, 64)
	if err != nil {
		return user{}, false
	}

	return user{ID: n, Name: "Ada Lovelace", Email: "ada@example.com"}, true
}

func nethttpBare() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /users/{id}", func(w http.ResponseWriter, r *http.Request) {
		u, ok := lookupUser(r.PathValue("id"))
		if !ok {
			http.NotFound(w, r)
			return
		}

		writeJSON(w, u)
	})

	return mux
}

func writeJSON(w http.ResponseWriter, v any) {
	buf, err := json.Marshal(v)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(buf)
}

func showUser(c *web.Ctx) error {
	u, ok := lookupUser(c.Param("id"))
	if !ok {
		return c.NoContent()
	}

	return c.JSON(http.StatusOK, u)
}

// vukemStack is the server half of DefaultMiddleware that chi and echo can
// match: Recover, RequestID, Logger and SecureHeaders.
func vukemStack(b *testing.B) http.Handler {
	b.Helper()

	r := router.New(&web.Services{Log: discardLog()})
	r.UseServer(
		middleware.Recover(),
		middleware.RequestID(),
		middleware.Logger(discardLog()),
		middleware.SecureHeaders(middleware.SecureHeadersConfig{}),
	)
	r.Get("/users/{id}", showUser)

	return mustHandler(b, r)
}

// vukemDefault is a booted app with the full DefaultMiddleware: the stack
// above plus maintenance mode, method override, body limit, sessions and
// cross-origin (CSRF) protection.
func vukemDefault(b *testing.B) http.Handler {
	b.Helper()

	app, err := vukem.New(
		vukem.WithSource(config.MapSource(map[string]string{
			"APP_KEY": config.GenerateKey(), "APP_ENV": "production", "APP_NAME": "Bench",
		})),
		vukem.WithLogger(discardLog()),
		vukem.WithStdout(io.Discard),
		vukem.WithRoutes(func(r *router.Router, app *vukem.App) error {
			if err := vukem.DefaultMiddleware(app, r); err != nil {
				return err
			}

			r.Get("/users/{id}", showUser)

			return nil
		}),
	)
	if err != nil {
		b.Fatal(err)
	}

	h, err := app.Handler()
	if err != nil {
		b.Fatal(err)
	}

	return h
}

func chiStack() http.Handler {
	log := discardLog()

	r := chi.NewRouter()
	r.Use(chimw.Recoverer, chimw.RequestID, chiLogger(log), staticSecureHeaders)
	r.Get("/users/{id}", func(w http.ResponseWriter, r *http.Request) {
		u, ok := lookupUser(chi.URLParam(r, "id"))
		if !ok {
			http.NotFound(w, r)
			return
		}

		writeJSON(w, u)
	})

	return r
}

func chiLogger(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			ww := chimw.NewWrapResponseWriter(w, r.ProtoMajor)

			next.ServeHTTP(ww, r)

			log.LogAttrs(r.Context(), slog.LevelInfo, "request",
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
				slog.Int("status", ww.Status()),
				slog.Duration("duration", time.Since(start)),
				slog.Int("bytes", ww.BytesWritten()),
				slog.String("request_id", chimw.GetReqID(r.Context())),
			)
		})
	}
}

func staticSecureHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Content-Security-Policy", staticCSP)
		next.ServeHTTP(w, r)
	})
}

func echoStack() http.Handler {
	log := discardLog()

	e := echo.New()
	e.Use(
		echomw.Recover(),
		echomw.RequestID(),
		echomw.RequestLoggerWithConfig(echomw.RequestLoggerConfig{
			LogMethod:       true,
			LogURIPath:      true,
			LogStatus:       true,
			LogLatency:      true,
			LogResponseSize: true,
			LogRequestID:    true,
			LogValuesFunc: func(c echo.Context, v echomw.RequestLoggerValues) error {
				log.LogAttrs(c.Request().Context(), slog.LevelInfo, "request",
					slog.String("method", v.Method),
					slog.String("path", v.URIPath),
					slog.Int("status", v.Status),
					slog.Duration("duration", v.Latency),
					slog.Int64("bytes", v.ResponseSize),
					slog.String("request_id", v.RequestID),
				)

				return nil
			},
		}),
		echomw.SecureWithConfig(echomw.SecureConfig{
			ContentTypeNosniff:    "nosniff",
			XFrameOptions:         "DENY",
			ReferrerPolicy:        "strict-origin-when-cross-origin",
			ContentSecurityPolicy: staticCSP,
		}),
	)
	e.GET("/users/:id", func(c echo.Context) error {
		u, ok := lookupUser(c.Param("id"))
		if !ok {
			return echo.ErrNotFound
		}

		return c.JSON(http.StatusOK, u)
	})

	return e
}
