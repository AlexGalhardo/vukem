// Package csrf protects state-changing requests from cross-site request
// forgery using Go's http.CrossOriginProtection (Fetch Metadata and Origin
// checks, no tokens needed in modern browsers), with an optional
// session-token fallback for clients that send neither header.
package csrf

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"fmt"
	"net/http"

	"github.com/AlexGalhardo/vukem/errs"
	"github.com/AlexGalhardo/vukem/web"
)

// HeaderName is the request header carrying the fallback token.
const HeaderName = "X-CSRF-Token"

// FieldName is the form field carrying the fallback token.
const FieldName = "_token"

// Config configures Middleware.
type Config struct {
	// TrustedOrigins are other origins allowed to send unsafe requests,
	// e.g. "https://admin.example.com".
	TrustedOrigins []string
	// BypassPatterns are ServeMux patterns exempt from checks (webhooks).
	// Bypasses are explicit and logged at boot by the caller.
	BypassPatterns []string
	// TokenFallback requires a session token on unsafe requests that carry
	// neither Sec-Fetch-Site nor Origin but do carry a session (old
	// browsers). API clients without sessions are unaffected.
	TokenFallback bool
}

// Middleware returns CSRF middleware for the router stack (after session
// middleware when TokenFallback is enabled). Rejections answer 403 through
// the error handler and log a "csrf.rejected" security event (A01/A08).
func Middleware(cfg Config) (func(http.Handler) http.Handler, error) {
	cop := http.NewCrossOriginProtection()

	for _, o := range cfg.TrustedOrigins {
		if err := cop.AddTrustedOrigin(o); err != nil {
			return nil, fmt.Errorf("csrf: trusted origin %q: %w", o, err)
		}
	}

	for _, p := range cfg.BypassPatterns {
		if err := addBypass(cop, p); err != nil {
			return nil, err
		}
	}

	cop.SetDenyHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reject(w, r, "cross-origin")
	}))

	return func(next http.Handler) http.Handler {
		inner := next
		if cfg.TokenFallback {
			inner = tokenFallback(next)
		}

		return cop.Handler(inner)
	}, nil
}

func addBypass(cop *http.CrossOriginProtection, pattern string) (err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("csrf: bypass pattern %q: %v", pattern, p)
		}
	}()

	cop.AddInsecureBypassPattern(pattern)

	return nil
}

func reject(w http.ResponseWriter, r *http.Request, reason string) {
	web.SecurityEvent(r, "csrf.rejected", "reason", reason,
		"origin", r.Header.Get("Origin"), "sec_fetch_site", r.Header.Get("Sec-Fetch-Site"))
	web.Fail(w, r, errs.New(http.StatusForbidden, "Cross-site request blocked."))
}

func safeMethod(m string) bool {
	return m == http.MethodGet || m == http.MethodHead || m == http.MethodOptions
}

func tokenFallback(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if safeMethod(r.Method) || r.Header.Get("Sec-Fetch-Site") != "" || r.Header.Get("Origin") != "" {
			next.ServeHTTP(w, r)
			return
		}

		sess := web.SessionFrom(r.Context())
		if sess == nil || !hasSessionCookie(r) {
			next.ServeHTTP(w, r)
			return
		}

		expected, _ := sess.Get(web.TokenKey).(string)

		got := r.Header.Get(HeaderName)
		if got == "" {
			got = r.PostFormValue(FieldName)
		}

		if expected == "" || subtle.ConstantTimeCompare([]byte(got), []byte(expected)) != 1 {
			reject(w, r, "token mismatch")
			return
		}

		next.ServeHTTP(w, r)
	})
}

func hasSessionCookie(r *http.Request) bool {
	for _, c := range r.Cookies() {
		if c.Value != "" {
			return true
		}
	}

	return false
}

// Token returns the session's CSRF token, creating it on first use.
// Templates expose it as {{ csrfToken }} / {{ csrfField }}; htmx sends it
// with hx-headers:inherited. It returns "" without a session.
func Token(ctx context.Context) string {
	sess := web.SessionFrom(ctx)
	if sess == nil {
		return ""
	}

	if t, ok := sess.Get(web.TokenKey).(string); ok && t != "" {
		return t
	}

	t := rand.Text()
	sess.Put(web.TokenKey, t)

	return t
}
