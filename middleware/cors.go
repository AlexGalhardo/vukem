package middleware

import (
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
)

// CORSConfig configures cross-origin resource sharing. The zero value
// allows no cross-origin requests (deny by default).
type CORSConfig struct {
	// AllowedOrigins lists exact origins ("https://app.example.com") or "*".
	AllowedOrigins []string
	// AllowedMethods defaults to GET, HEAD, POST, PUT, PATCH, DELETE.
	AllowedMethods []string
	// AllowedHeaders defaults to Accept, Authorization, Content-Type,
	// X-Requested-With, X-CSRF-Token.
	AllowedHeaders []string
	// ExposedHeaders are readable by the browser.
	ExposedHeaders []string
	// AllowCredentials allows cookies/Authorization; incompatible with "*".
	AllowCredentials bool
	// MaxAge caches preflight responses.
	MaxAge time.Duration
}

// CORS returns CORS middleware. Register it as server middleware so
// preflight requests are answered before routing. It returns an error for
// insecure configurations ("*" with credentials), so misconfiguration fails
// at boot (A02).
func CORS(cfg CORSConfig) (Middleware, error) {
	wildcard := slices.Contains(cfg.AllowedOrigins, "*")
	if wildcard && cfg.AllowCredentials {
		return nil, errors.New(`middleware: CORS cannot allow credentials with origin "*"`)
	}

	methods := cfg.AllowedMethods
	if len(methods) == 0 {
		methods = []string{"GET", "HEAD", "POST", "PUT", "PATCH", "DELETE"}
	}

	headers := cfg.AllowedHeaders
	if len(headers) == 0 {
		headers = []string{"Accept", "Authorization", "Content-Type", "X-Requested-With", "X-CSRF-Token"}
	}

	allowMethods := strings.Join(methods, ", ")
	allowHeaders := strings.Join(headers, ", ")
	expose := strings.Join(cfg.ExposedHeaders, ", ")

	allowed := func(origin string) bool {
		return wildcard || slices.Contains(cfg.AllowedOrigins, origin)
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			w.Header().Add("Vary", "Origin")

			if origin == "" || !allowed(origin) {
				// Not a CORS request, or a disallowed origin: no CORS headers,
				// so the browser blocks the cross-origin read.
				next.ServeHTTP(w, r)
				return
			}

			h := w.Header()
			if wildcard {
				h.Set("Access-Control-Allow-Origin", "*")
			} else {
				h.Set("Access-Control-Allow-Origin", origin)
			}

			if cfg.AllowCredentials {
				h.Set("Access-Control-Allow-Credentials", "true")
			}

			preflight := r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != ""
			if !preflight {
				if expose != "" {
					h.Set("Access-Control-Expose-Headers", expose)
				}

				next.ServeHTTP(w, r)

				return
			}

			h.Add("Vary", "Access-Control-Request-Method")
			h.Add("Vary", "Access-Control-Request-Headers")
			h.Set("Access-Control-Allow-Methods", allowMethods)
			h.Set("Access-Control-Allow-Headers", allowHeaders)

			if cfg.MaxAge > 0 {
				h.Set("Access-Control-Max-Age", strconv.Itoa(int(cfg.MaxAge.Seconds())))
			}

			w.WriteHeader(http.StatusNoContent)
		})
	}, nil
}
