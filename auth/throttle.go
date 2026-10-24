package auth

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/AlexGalhardo/vukem/ratelimit"
)

const maxThrottleBody = 64 << 10

// RequestEmail returns the normalized (trimmed, lower case) value of field
// from a form or JSON request body, so " a@b.co" and "A@b.co " share one
// throttle bucket. A JSON body is restored for the handler. It returns ""
// when the field is absent.
func RequestEmail(r *http.Request, field string) string {
	email := r.PostFormValue(field)

	if email == "" && strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		body, err := io.ReadAll(io.LimitReader(r.Body, maxThrottleBody))
		if err == nil {
			var in map[string]any

			if json.Unmarshal(body, &in) == nil {
				email, _ = in[field].(string)
			}
		}

		// Restore what was read so the handler still sees the whole body.
		r.Body = io.NopCloser(io.MultiReader(bytes.NewReader(body), r.Body))
	}

	return strings.ToLower(strings.TrimSpace(email))
}

// ThrottleKeys returns the two [ratelimit.KeyFunc]s a login route should be
// limited by: perIP counts attempts per email and client IP, so one attacker
// cannot lock an account out from another network; perAccount counts them per
// email from any IP, so many addresses cannot share the guessing budget of
// one account. Both skip (return "") anything but POST, so showing the form
// costs no attempt. emailField names the form or JSON field holding the email.
func ThrottleKeys(emailField string) (perIP, perAccount ratelimit.KeyFunc) {
	perIP = func(r *http.Request) string {
		if r.Method != http.MethodPost {
			return ""
		}

		return "login:" + RequestEmail(r, emailField) + ":" + ratelimit.ByIP(r)
	}

	perAccount = func(r *http.Request) string {
		if r.Method != http.MethodPost {
			return ""
		}

		return "login-account:" + RequestEmail(r, emailField)
	}

	return perIP, perAccount
}
