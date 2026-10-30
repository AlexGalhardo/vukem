package cloudflare

import (
	"context"
	"net/http"
	"strconv"
)

// Visitor is what Cloudflare reports about the visitor in request
// headers (https://developers.cloudflare.com/fundamentals/reference/http-headers/
// and the "Add bot protection headers" Managed Transform,
// https://developers.cloudflare.com/rules/transform/managed-transforms/reference/,
// read 2026-10-29). Zero values mean Cloudflare did not send the header.
type Visitor struct {
	// Country is CF-IPCountry: an ISO 3166-1 alpha-2 code, "XX" when
	// unknown, "T1" for Tor.
	Country string
	// Ray is CF-Ray, the Ray ID to match a request with Cloudflare's logs.
	Ray string
	// BotScore is cf-bot-score, 1 (bot) to 99 (human); 0 when absent
	// (Bot Management only).
	BotScore int
	// VerifiedBot is cf-verified-bot (Bot Management only).
	VerifiedBot bool
}

type visitorKey struct{}

// Headers returns middleware that puts the Visitor in the request
// context. The headers are read only when the TCP peer is Cloudflare:
// from any other peer they are client input, and VisitorFrom reports
// false. A malformed value is dropped, never passed on.
func Headers() func(http.Handler) http.Handler {
	ranges := Ranges()

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if peerIn(r, ranges) {
				r = r.WithContext(context.WithValue(r.Context(), visitorKey{}, parseVisitor(r.Header)))
			}

			next.ServeHTTP(w, r)
		})
	}
}

// VisitorFrom returns the Visitor that Headers stored; ok is false when
// Headers did not run or the request did not come from Cloudflare.
func VisitorFrom(ctx context.Context) (v Visitor, ok bool) {
	v, ok = ctx.Value(visitorKey{}).(Visitor)
	return v, ok
}

func parseVisitor(h http.Header) Visitor {
	var v Visitor

	if c := h.Get("CF-IPCountry"); len(c) == 2 && isAlnumUpper(c) {
		v.Country = c
	}

	if ray := h.Get("CF-Ray"); len(ray) <= 64 && isRay(ray) {
		v.Ray = ray
	}

	if n, err := strconv.Atoi(h.Get("Cf-Bot-Score")); err == nil && n >= 1 && n <= 99 {
		v.BotScore = n
	}

	v.VerifiedBot = h.Get("Cf-Verified-Bot") == "true"

	return v
}

func isAlnumUpper(s string) bool {
	for _, c := range []byte(s) {
		if (c < 'A' || c > 'Z') && (c < '0' || c > '9') {
			return false
		}
	}

	return true
}

func isRay(s string) bool {
	for _, c := range []byte(s) {
		if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (c < '0' || c > '9') && c != '-' {
			return false
		}
	}

	return true
}
