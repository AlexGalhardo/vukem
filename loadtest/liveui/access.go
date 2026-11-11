package liveui

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"net"
	"net/http"
	"net/netip"
	"time"

	"github.com/AlexGalhardo/vukem/loadtest"
)

const cookieName = "vukem_bench"

const (
	// maxSessions bounds the browsers signed in at once.
	maxSessions = 16
	// sessionLifetime is how long a session lasts without a request, and the Max-Age of its cookie. Every answer
	// renews it.
	sessionLifetime = time.Hour
)

type sessionEntry struct {
	id      string
	expires time.Time
}

// csp is the policy of every response: scripts and styles only from the embedded files, nothing inline, and requests
// only to the same origin (the event stream).
const csp = "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; " +
	"form-action 'self'; frame-ancestors 'none'; base-uri 'none'; object-src 'none'; connect-src 'self'"

// proxyHeaders are the headers a reverse proxy adds. A request that has one did not come straight from the local
// machine, whatever its peer address says.
var proxyHeaders = []string{
	"Forwarded", "X-Forwarded-For", "X-Forwarded-Host", "X-Forwarded-Proto", "X-Forwarded-Port", "X-Real-Ip", "Via",
}

func newToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}

	return base64.RawURLEncoding.EncodeToString(b), nil
}

// protect wraps next with the chain, outermost first: headers, loopback, origin, session.
func (s *Server) protect(next http.Handler) http.Handler {
	return secureHeaders(loopbackOnly(sameOriginOnly(http.NewCrossOriginProtection().Handler(s.session(next)))))
}

// secureHeaders sets the headers of every response, refusals included.
func secureHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", csp)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Cache-Control", "no-store")

		next.ServeHTTP(w, r)
	})
}

// loopbackOnly accepts a loopback peer with no proxy header and a loopback Host. The Host check stops a DNS
// rebinding page from reaching the dashboard under another name.
func loopbackOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !loopbackRequest(r) {
			http.Error(w, "the dashboard answers local requests only", http.StatusForbidden)

			return
		}

		next.ServeHTTP(w, r)
	})
}

func loopbackRequest(r *http.Request) bool {
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

	return loadtest.IsLoopbackHost(r.Host)
}

// sameOriginOnly refuses a request a browser marks as coming from another site or origin, even a GET: the
// dashboard has no use for one.
func sameOriginOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Header.Get("Sec-Fetch-Site") {
		case "", "same-origin", "none":
			next.ServeHTTP(w, r)
		default:
			http.Error(w, "cross-origin request refused", http.StatusForbidden)
		}
	})
}

// session accepts the token once, in the start URL, and trades it for a session cookie; every later request needs the
// cookie. The cookie holds a random session id, never the token: it is sent to every service on localhost, the app under
// test included, and it expires. The token works a single time, because its URL stays in the browser history and on
// the process list.
func (s *Server) session(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if q := r.URL.Query(); q.Has("token") {
			if !s.validToken(q.Get("token")) || !s.tokenUsed.CompareAndSwap(false, true) {
				http.Error(w, "unauthorized", http.StatusUnauthorized)

				return
			}

			setSessionCookie(w, r, s.newSession())

			q.Del("token")

			u := *r.URL
			u.RawQuery = q.Encode()

			// The target is the path of this request, never a host.
			target := u.RequestURI()

			http.Redirect(w, r, target, http.StatusSeeOther) //nolint:gosec // G710: see above

			return
		}

		c, err := r.Cookie(cookieName)
		if err != nil || !s.validSession(c.Value) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)

			return
		}

		setSessionCookie(w, r, c.Value)

		next.ServeHTTP(w, r)
	})
}

// setSessionCookie marks the cookie Secure when the request came over TLS. The dashboard itself serves plain HTTP on
// loopback, where a Secure cookie would never be sent back; proxy headers are refused before this runs.
func setSessionCookie(w http.ResponseWriter, r *http.Request, id string) {
	//nolint:gosec // G124: Secure follows the transport; on the plain HTTP loopback dashboard it must stay false
	http.SetCookie(w, &http.Cookie{
		Name: cookieName, Value: id, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: r.TLS != nil,
		MaxAge: int(sessionLifetime / time.Second),
	})
}

// newSession signs a browser in: a random id valid for sessionLifetime. Only maxSessions are kept; the oldest goes.
func (s *Server) newSession() string {
	id, err := newToken()
	if err != nil {
		// The system has no randomness: no session can be issued, and an empty id is never valid.
		return ""
	}

	s.sessMu.Lock()
	defer s.sessMu.Unlock()

	s.sessions = append(s.sessions, sessionEntry{id: id, expires: s.now().Add(sessionLifetime)})
	if len(s.sessions) > maxSessions {
		s.sessions = s.sessions[len(s.sessions)-maxSessions:]
	}

	return id
}

// validSession reports a live session and renews it.
func (s *Server) validSession(id string) bool {
	if id == "" {
		return false
	}

	s.sessMu.Lock()
	defer s.sessMu.Unlock()

	now := s.now()
	live := s.sessions[:0]
	found := false

	for _, e := range s.sessions {
		if !e.expires.After(now) {
			continue
		}

		if subtle.ConstantTimeCompare([]byte(id), []byte(e.id)) == 1 {
			e.expires, found = now.Add(sessionLifetime), true
		}

		live = append(live, e)
	}

	s.sessions = live

	return found
}

func (s *Server) validToken(got string) bool {
	return subtle.ConstantTimeCompare([]byte(got), []byte(s.token)) == 1
}
