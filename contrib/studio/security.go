package studio

import (
	"crypto/subtle"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
)

const (
	// sessionCookie holds the session id the start-URL token is traded for.
	sessionCookie = "vukem_studio"

	// strictCSP is the policy of every response: no inline script or style,
	// no framing, nothing from other origins.
	strictCSP = "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; " +
		"form-action 'self'; frame-ancestors 'none'; base-uri 'none'; object-src 'none'"
)

// proxyHeaders mark a request relayed by a proxy or tunnel: the direct peer
// is then loopback even though the client is not.
var proxyHeaders = []string{ //nolint:gochecknoglobals // read-only
	"Forwarded", "X-Forwarded-For", "X-Forwarded-Host", "X-Forwarded-Proto", "X-Forwarded-Port", "X-Real-Ip", "Via",
}

// allowed reports whether r may reach Studio: the direct peer is a loopback
// address, no proxy header is present, and Host is a loopback name (which
// defeats DNS rebinding from a page the developer visits). There is no
// escape hatch.
func allowed(r *http.Request) bool {
	if !peerIsLoopback(r.RemoteAddr) {
		return false
	}

	for _, h := range proxyHeaders {
		if r.Header.Get(h) != "" {
			return false
		}
	}

	return loopbackHost(r.Host)
}

func peerIsLoopback(remoteAddr string) bool {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}

	ip, err := netip.ParseAddr(host)

	return err == nil && ip.Zone() == "" && ip.Unmap().IsLoopback()
}

func loopbackHost(hostport string) bool {
	host, _, err := net.SplitHostPort(hostport)
	if err != nil {
		host = hostport
	}

	host = strings.ToLower(strings.Trim(host, "[]"))
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}

	ip, err := netip.ParseAddr(host)

	return err == nil && ip.Unmap().IsLoopback()
}

// secure wraps next with the four guards, outermost first: security headers,
// the access check, cross-origin protection, the session.
func (s *Studio) secure(next http.Handler) http.Handler {
	return headers(guard(http.NewCrossOriginProtection().Handler(s.session(next))))
}

// headers sets the security headers on every response, refusals included.
func headers(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", strictCSP)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Cache-Control", "no-store")

		next.ServeHTTP(w, r)
	})
}

// guard answers 403, with no useful body, to a request that is not from this
// machine's loopback.
func guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !allowed(r) {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// session trades the start-URL token for a cookie, then requires the cookie.
func (s *Studio) session(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if q := r.URL.Query(); q.Has("token") {
			s.exchange(w, r, q)
			return
		}

		c, err := r.Cookie(sessionCookie)
		if err != nil || subtle.ConstantTimeCompare([]byte(c.Value), []byte(s.sessionID)) != 1 {
			unauthorized(w)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// exchange checks the token and, if it is right, sets the session cookie and
// redirects to the same URL without the token (so it stays out of history
// and logs).
func (s *Studio) exchange(w http.ResponseWriter, r *http.Request, q url.Values) {
	if subtle.ConstantTimeCompare([]byte(q.Get("token")), []byte(s.token)) != 1 {
		unauthorized(w)
		return
	}

	// Secure follows the transport: Studio serves plain HTTP on loopback, where
	// Safari drops a Secure cookie and the session would never come back.
	//nolint:gosec // G124: Secure is set whenever the request came over TLS
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    s.sessionID,
		Path:     "/",
		HttpOnly: true,
		Secure:   r.TLS != nil,
		SameSite: http.SameSiteStrictMode,
	})

	q.Del("token")

	u := url.URL{Path: r.URL.Path, RawQuery: q.Encode()}
	http.Redirect(w, r, u.String(), http.StatusSeeOther)
}

func unauthorized(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusUnauthorized)

	_, _ = w.Write([]byte(`<!doctype html><meta charset="utf-8"><title>vukem-studio</title>` +
		`<p>Open the address with the token that vukem-studio printed in the terminal.</p>`))
}
