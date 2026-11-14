package middleware

import (
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/AlexGalhardo/vukem/web"
)

// loopbackProxyHeaders mark a request relayed by a proxy or tunnel, whose
// direct peer is loopback even though the client is not.
var loopbackProxyHeaders = []string{ //nolint:gochecknoglobals // read-only
	"Forwarded", "X-Forwarded-For", "X-Forwarded-Host", "X-Forwarded-Proto", "X-Forwarded-Port", "X-Real-Ip", "Via",
}

// LoopbackOnly admits only requests meant for a local server listening on
// port: the direct peer is a loopback address, no proxy header is present,
// Host is exactly localhost, 127.0.0.1 or [::1] with that port, and an
// Origin header, when sent, is the http origin of one of those hosts. The
// Host and Origin checks defeat DNS rebinding from a page the user visits.
// Anything else gets 403 with no useful body.
//
// Names such as app.localhost are refused: a browser resolves them to
// loopback, but the match stays exact on purpose. A port of 0 or less
// skips the port comparison; pass the bound port when it is known.
// There is no opt-out; use it for a development server or a local-only mode.
func LoopbackOnly(port int) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !loopbackRequest(r, port) {
				http.Error(w, "Forbidden", http.StatusForbidden)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

func loopbackRequest(r *http.Request, port int) bool {
	if !web.PeerIP(r).IsLoopback() {
		return false
	}

	for _, h := range loopbackProxyHeaders {
		if r.Header.Get(h) != "" {
			return false
		}
	}

	if !exactLoopbackHost(r.Host, port) {
		return false
	}

	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}

	u, err := url.Parse(origin)
	if err != nil || u.Scheme != "http" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return false
	}

	return exactLoopbackHost(u.Host, port)
}

// exactLoopbackHost reports whether hostport names localhost, 127.0.0.1 or
// [::1] on port. Any port passes when port <= 0; a Host without a port
// means the scheme default, so it matches only port 80.
func exactLoopbackHost(hostport string, port int) bool {
	host, p, err := net.SplitHostPort(hostport)
	if err != nil {
		host, p = strings.TrimSuffix(strings.TrimPrefix(hostport, "["), "]"), ""
	}

	switch strings.ToLower(host) {
	case "localhost", "127.0.0.1", "::1":
	default:
		return false
	}

	if port <= 0 {
		return true
	}

	return p == strconv.Itoa(port) || (p == "" && port == 80)
}
