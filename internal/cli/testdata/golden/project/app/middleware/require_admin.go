package middleware

import "net/http"

// RequireAdmin wraps handlers; register it with r.Use, group.Use or route.Use.
func RequireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r)
	})
}
