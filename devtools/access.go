package devtools

import (
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// proxyHeaders mark a request relayed by a proxy or tunnel: the direct
// peer is then loopback even though the client is not.
var proxyHeaders = []string{"Forwarded", "X-Forwarded-For", "X-Forwarded-Host", "X-Real-Ip"}

// Allowed reports whether r may see the dashboard: with
// InsecureAllowRemote always; otherwise only when the direct peer is a
// loopback address, no proxy header is present, and Host is a loopback
// name (which defeats DNS rebinding from a page the developer visits).
func (d *Devtools) Allowed(r *http.Request) bool {
	if d.cfg.InsecureAllowRemote {
		return true
	}

	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}

	ip, err := netip.ParseAddr(host)
	if err != nil || !ip.Unmap().IsLoopback() {
		return false
	}

	for _, h := range proxyHeaders {
		if r.Header.Get(h) != "" {
			return false
		}
	}

	return loopbackHost(r.Host)
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

// guard answers 404 to clients that may not see the dashboard, so its
// existence is not revealed.
func (d *Devtools) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !d.Allowed(r) {
			http.NotFound(w, r)
			return
		}

		next.ServeHTTP(w, r)
	})
}
