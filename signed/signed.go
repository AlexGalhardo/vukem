// Package signed creates and verifies signed URLs: links that carry an
// expiry and an HMAC-SHA256 signature over their path and query, for email
// verification, unsubscribe links or temporary downloads.
//
//	s := signed.New(app.Key.Derive("signed.urls", 32))
//	link, _ := s.Sign(appURL+"/email/verify/42", time.Hour)
//	r.Get("/email/verify/{id}", verify).Use(s.Middleware())
package signed

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/AlexGalhardo/vukem/errs"
	"github.com/AlexGalhardo/vukem/web"
)

// Errors returned by Verify (403 for clients).
var (
	ErrInvalid = &errs.HTTPError{Status: http.StatusForbidden, Message: "Invalid signature."}
	ErrExpired = &errs.HTTPError{Status: http.StatusForbidden, Message: "This link has expired."}
)

// Signer signs and verifies URLs with one key.
type Signer struct {
	key []byte
	now func() time.Time
}

// New returns a Signer; derive key from APP_KEY.
func New(key []byte) *Signer { return &Signer{key: key, now: time.Now} }

// Sign adds "expires" (Unix seconds; ttl 0 means no expiry) and
// "signature" query parameters to rawURL. Only the path and query are
// signed, so links keep working behind proxies and on other hosts.
func (s *Signer) Sign(rawURL string, ttl time.Duration) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", err
	}

	q := u.Query()
	q.Del("signature")

	if ttl > 0 {
		q.Set("expires", strconv.FormatInt(s.now().Add(ttl).Unix(), 10))
	} else {
		q.Del("expires")
	}

	u.RawQuery = q.Encode()
	q.Set("signature", s.sign(u.EscapedPath(), u.RawQuery))
	u.RawQuery = q.Encode()

	return u.String(), nil
}

// Verify checks the signature (constant time) and the expiry of r's URL.
func (s *Signer) Verify(r *http.Request) error {
	q := r.URL.Query()

	sig := q.Get("signature")
	if sig == "" {
		return ErrInvalid
	}

	q.Del("signature")

	want := s.sign(r.URL.EscapedPath(), q.Encode())
	if !hmac.Equal([]byte(sig), []byte(want)) {
		return ErrInvalid
	}

	if exp := q.Get("expires"); exp != "" {
		n, err := strconv.ParseInt(exp, 10, 64)
		if err != nil {
			return ErrInvalid
		}

		if s.now().Unix() > n {
			return ErrExpired
		}
	}

	return nil
}

// Middleware rejects requests whose URL signature is missing, wrong or
// expired with 403 (and an "url.signature_invalid" security event).
func (s *Signer) Middleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if err := s.Verify(r); err != nil {
				if errors.Is(err, ErrInvalid) {
					web.SecurityEvent(r, "url.signature_invalid")
				}

				web.Fail(w, r, err)

				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

func (s *Signer) sign(path, query string) string {
	m := hmac.New(sha256.New, s.key)
	m.Write([]byte(path + "?" + query))

	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}
