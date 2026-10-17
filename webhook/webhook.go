// Package webhook verifies signed incoming webhooks: [Middleware] checks an
// HMAC-SHA256 of the raw body (GitHub, Shopify, your own integrations),
// [Standard] checks Standard Webhooks signatures (Svix, Resend, Clerk) with a
// timestamp replay window, and [Dedupe] drops at-least-once redeliveries.
//
//	verify, err := webhook.HMAC("X-Hub-Signature-256", secret, 1<<20) // error on an empty secret
//	r.Post("/webhooks/github", handle).Use(verify)
//
// [HMAC] and [Middleware] bound the body, verifies the signature header and answers
// 401 ([ErrSignature]) on mismatch; [VerifyHMAC] and [Sign] are the
// building blocks for custom schemes and tests.
//
// Outgoing webhooks: [Sender] delivers [Delivery] payloads from queue jobs,
// signed with the Standard Webhooks scheme ([NewSecret], [SignStandard]),
// through [httpclient.Safe] with retries and backoff; [SQLDeliveryLog]
// records each attempt.
//
// See https://github.com/AlexGalhardo/vukem/blob/main/docs/security.md.
package webhook

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/AlexGalhardo/vukem/errs"
	"github.com/AlexGalhardo/vukem/web"
)

// Sign returns the hex HMAC-SHA256 of payload (for tests and outgoing
// webhooks).
func Sign(secret, payload []byte) string {
	m := hmac.New(sha256.New, secret)
	m.Write(payload)

	return hex.EncodeToString(m.Sum(nil))
}

// VerifyHMAC reports whether signature is the HMAC-SHA256 of payload with
// secret. The signature may be hex or base64 and may carry a "sha256="
// prefix. The comparison is constant time.
func VerifyHMAC(secret, payload []byte, signature string) bool {
	if len(secret) == 0 || signature == "" {
		return false
	}

	signature = strings.TrimPrefix(strings.TrimSpace(signature), "sha256=")

	m := hmac.New(sha256.New, secret)
	m.Write(payload)
	want := m.Sum(nil)

	for _, decode := range []func(string) ([]byte, error){
		hex.DecodeString, base64.StdEncoding.DecodeString, base64.RawURLEncoding.DecodeString,
	} {
		if got, err := decode(signature); err == nil && hmac.Equal(got, want) {
			return true
		}
	}

	return false
}

// ErrSignature is the 401 answered for missing or wrong signatures.
var ErrSignature = &errs.HTTPError{Status: http.StatusUnauthorized, Message: "Invalid webhook signature."}

// HMAC is [Middleware] that checks its configuration first: an empty
// header or secret (an unset environment variable) is an error at boot
// instead of a webhook that answers 401 forever. Prefer it to Middleware.
func HMAC(header string, secret []byte, maxBody int64) (func(http.Handler) http.Handler, error) {
	switch {
	case header == "":
		return nil, errors.New("webhook: HMAC needs a signature header name")
	case len(secret) == 0:
		return nil, errors.New("webhook: HMAC needs a non-empty secret")
	}

	return Middleware(header, secret, maxBody), nil
}

// Middleware verifies the signature in header against the raw body (at
// most maxBody bytes, default 1 MiB when maxBody <= 0) and restores the body
// for the handler. Failures get 401 and a "webhook.signature_invalid"
// security event. It does not bound replays; use [Standard] when the
// provider signs a timestamp, and [Dedupe] to drop redeliveries.
//
// Middleware is the lenient form: an empty secret is not reported and
// every request is refused. [HMAC] reports it at boot.
func Middleware(header string, secret []byte, maxBody int64) func(http.Handler) http.Handler {
	if maxBody <= 0 {
		maxBody = defaultMaxBody
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, err := io.ReadAll(io.LimitReader(r.Body, maxBody+1))
			if err != nil || int64(len(body)) > maxBody {
				web.Fail(w, r, errs.ErrRequestTooLarge)
				return
			}

			if !VerifyHMAC(secret, body, r.Header.Get(header)) {
				web.SecurityEvent(r, "webhook.signature_invalid", "header", header)
				web.Fail(w, r, ErrSignature)

				return
			}

			r.Body = io.NopCloser(bytes.NewReader(body))
			next.ServeHTTP(w, r)
		})
	}
}
