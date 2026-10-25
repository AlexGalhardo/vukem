package auth

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"

	"github.com/AlexGalhardo/vukem/ratelimit"
	"github.com/AlexGalhardo/vukem/web"
)

// RequestEmail returns the normalized (trimmed, lower case) value of field
// the way web.Ctx.Input reads it, so the throttle and the login handler see
// the same address: a JSON body (any application/json or +json media type,
// parameters and casing ignored) wins over the query string, a form body
// over the query string. A JSON body is restored for the handler. It
// returns "" when the field is absent, not a string, or the body is one the
// handler would reject (web.PeekJSON: over the route's body limit, trailing
// data, not an object); callers must treat "" as unknown, never as an
// address.
func RequestEmail(r *http.Request, field string) string {
	var email string

	if web.IsJSONMediaType(r.Header.Get("Content-Type")) {
		in, err := web.PeekJSON(r)
		if err != nil {
			return ""
		}

		if v, ok := in[field]; ok {
			email, _ = v.(string)
		} else {
			email = r.URL.Query().Get(field)
		}
	} else {
		email = r.PostFormValue(field)
		if email == "" {
			email = r.URL.Query().Get(field)
		}
	}

	return strings.ToLower(strings.TrimSpace(email))
}

// ThrottleKeys returns the two [ratelimit.KeyFunc]s a login route should be
// limited by: perIP counts attempts per email and client IP, so one attacker
// cannot lock an account out from another network; perAccount counts them per
// email from any IP, so many addresses cannot share the guessing budget of
// one account. Both skip (return "") anything but POST, so showing the form
// costs no attempt. emailField names the form or JSON field holding the email.
// An email longer than 254 bytes (RFC 5321) is keyed by its SHA-256, so a
// large body cannot park megabytes in the limiter and still counts against
// its account. Pair it with a handler that reads the email the same way
// (web.Parse or web.Bind): BindJSON into a struct matches JSON keys without
// regard to case, so {"EMAIL": ...} would reach the handler while the
// throttle falls back to the per-IP bucket.
func ThrottleKeys(emailField string) (perIP, perAccount ratelimit.KeyFunc) {
	perIP = func(r *http.Request) string {
		if r.Method != http.MethodPost {
			return ""
		}

		email := RequestEmail(r, emailField)
		if email == "" {
			return unparsedKey(r)
		}

		return "login" + emailKey(email) + ":" + ratelimit.ByIP(r)
	}

	perAccount = func(r *http.Request) string {
		if r.Method != http.MethodPost {
			return ""
		}

		email := RequestEmail(r, emailField)
		if email == "" {
			return unparsedKey(r)
		}

		return "login-account" + emailKey(email)
	}

	return perIP, perAccount
}

// maxEmailLen is the longest address RFC 5321 allows (254 octets).
const maxEmailLen = 254

// emailKey is the email part of a login key: ":" and the email, or, past
// maxEmailLen, "-sha256:" and its digest. The distinct separator keeps a
// short email that spells "sha256:<hex>" out of a digested bucket.
func emailKey(email string) string {
	if len(email) <= maxEmailLen {
		return ":" + email
	}

	sum := sha256.Sum256([]byte(email))

	return "-sha256:" + hex.EncodeToString(sum[:])
}

// unparsedKey is the bucket of a login POST whose email could not be read.
// It is per IP, never shared: one shared bucket would let anyone lock every
// login out, and no bucket would let a body the throttle cannot parse but
// the handler can skip the limit.
func unparsedKey(r *http.Request) string { return "login-unparsed:" + ratelimit.ByIP(r) }
