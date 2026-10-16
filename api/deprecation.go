package api

import (
	"net/http"
	"strconv"
	"time"
)

// Deprecate marks routes as deprecated (RFC 9745 Deprecation and RFC 8594
// Sunset headers) and links to the replacement or migration guide.
// Apply it to a version group: g.Use(api.Deprecate(since, sunset, "/docs/v2")).
func Deprecate(since, sunset time.Time, link string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := w.Header()
			h.Set("Deprecation", "@"+strconv.FormatInt(since.Unix(), 10))

			if !sunset.IsZero() {
				h.Set("Sunset", sunset.UTC().Format(http.TimeFormat))
			}

			if link != "" {
				h.Add("Link", "<"+link+`>; rel="deprecation"; type="text/html"`)
			}

			next.ServeHTTP(w, r)
		})
	}
}
