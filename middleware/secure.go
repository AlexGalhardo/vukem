package middleware

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// SecureHeadersConfig configures SecureHeaders. Zero values use the secure
// defaults documented in docs/security.md.
type SecureHeadersConfig struct {
	// CSP is the Content-Security-Policy; "{nonce}" is replaced with a
	// per-request nonce. Empty uses DefaultCSP; "-" disables the header.
	CSP string
	// HSTSMaxAge enables Strict-Transport-Security on HTTPS requests.
	// Zero uses two years; negative disables it.
	HSTSMaxAge time.Duration
	// FrameOptions defaults to DENY; "-" disables it.
	FrameOptions string
	// ReferrerPolicy defaults to strict-origin-when-cross-origin.
	ReferrerPolicy string
	// PermissionsPolicy defaults to camera=(), microphone=(), geolocation=().
	PermissionsPolicy string
	// TrustForwardedProto treats X-Forwarded-Proto: https as HTTPS (set it
	// only behind a trusted proxy).
	TrustForwardedProto bool
}

// DefaultCSP is a strict policy suitable for server-rendered pages with
// nonce'd scripts and styles.
const DefaultCSP = "default-src 'self'; script-src 'self' 'nonce-{nonce}'; style-src 'self' 'nonce-{nonce}'; " +
	"img-src 'self' data:; font-src 'self'; connect-src 'self'; object-src 'none'; base-uri 'self'; " +
	"frame-ancestors 'none'; form-action 'self'"

type nonceKey struct{}

// CSPNonce returns the per-request CSP nonce, or "" when SecureHeaders did
// not run. Templates expose it as {{ cspNonce }}.
func CSPNonce(ctx context.Context) string {
	n, _ := ctx.Value(nonceKey{}).(string)
	return n
}

// SecureHeaders sets security headers on every response (A02).
func SecureHeaders(cfg SecureHeadersConfig) Middleware {
	csp := cfg.CSP
	if csp == "" {
		csp = DefaultCSP
	}

	hsts := ""
	if cfg.HSTSMaxAge >= 0 {
		age := cfg.HSTSMaxAge
		if age == 0 {
			age = 2 * 365 * 24 * time.Hour
		}

		hsts = "max-age=" + strconv.Itoa(int(age.Seconds())) + "; includeSubDomains"
	}

	frame := orDefault(cfg.FrameOptions, "DENY")
	referrer := orDefault(cfg.ReferrerPolicy, "strict-origin-when-cross-origin")
	permissions := orDefault(cfg.PermissionsPolicy, "camera=(), microphone=(), geolocation=()")

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := w.Header()
			h.Set("X-Content-Type-Options", "nosniff")
			h.Set("Referrer-Policy", referrer)
			h.Set("Cross-Origin-Opener-Policy", "same-origin")
			h.Set("Permissions-Policy", permissions)

			if frame != "-" {
				h.Set("X-Frame-Options", frame)
			}

			https := r.TLS != nil || (cfg.TrustForwardedProto && r.Header.Get("X-Forwarded-Proto") == "https")
			if hsts != "" && https {
				h.Set("Strict-Transport-Security", hsts)
			}

			if csp != "-" {
				nonce := newNonce()
				h.Set("Content-Security-Policy", strings.ReplaceAll(csp, "{nonce}", nonce))
				r = r.WithContext(context.WithValue(r.Context(), nonceKey{}, nonce))
			}

			next.ServeHTTP(w, r)
		})
	}
}

func newNonce() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b) // never fails since Go 1.24

	return base64.RawStdEncoding.EncodeToString(b)
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}

	return v
}
