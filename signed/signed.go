// Package signed creates and verifies signed URLs: links that carry an
// expiry and an HMAC-SHA256 signature over their path and query, for email
// verification, unsubscribe links or temporary downloads.
//
//	key, err := app.DeriveKey("signed.urls", 32) // fails without APP_KEY
//	if err != nil {
//		return err
//	}
//
//	s := signed.New(key).WithPreviousKeys(app.DerivePreviousKeys("signed.urls", 32)...)
//	link, _ := s.Sign(appURL+"/email/verify/42", time.Hour)
//	r.Get("/email/verify/{id}", verify).Use(s.Middleware())
//
// [Signer.Verify] checks a request in constant time and returns [ErrInvalid]
// or [ErrExpired]; [Signer.Middleware] answers 403 for both.
//
// See https://github.com/AlexGalhardo/vukem/blob/main/docs/security.md.
package signed

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
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

// Signer signs URLs with one key and verifies them with that key or any
// previous key (APP_KEY rotation, see WithPreviousKeys).
type Signer struct {
	key      []byte
	previous [][]byte
	now      func() time.Time
}

// New returns a Signer; derive key from APP_KEY (app.DeriveKey). It
// panics when key is shorter than MinKeyLen.
func New(key []byte) *Signer {
	checkKey(key)

	return &Signer{key: key, now: time.Now}
}

// MinKeyLen is the minimum signing key length; New panics below it.
const MinKeyLen = 32

// WithPreviousKeys makes s also accept links signed with keys (derive them
// with app.DerivePreviousKeys, from APP_PREVIOUS_KEYS), so links sent
// before an APP_KEY rotation keep working until the old key is removed.
// Sign always uses the current key. Call it before s is used; it panics
// when a key is shorter than MinKeyLen, like New.
func (s *Signer) WithPreviousKeys(keys ...[]byte) *Signer {
	for _, k := range keys {
		checkKey(k)
	}

	s.previous = append(s.previous, keys...)

	return s
}

func checkKey(key []byte) {
	if len(key) < MinKeyLen {
		// An empty or short HMAC key makes every link forgeable.
		panic(
			fmt.Sprintf(
				"signed: key must be at least %d bytes, got %d (derive it with app.DeriveKey)",
				MinKeyLen,
				len(key),
			),
		)
	}
}

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

	if !s.valid(sig, r.URL.EscapedPath(), q.Encode()) {
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

// valid reports whether sig matches path and query under the current or a
// previous key, each compared in constant time.
func (s *Signer) valid(sig, path, query string) bool {
	if hmac.Equal([]byte(sig), []byte(mac(s.key, path, query))) {
		return true
	}

	for _, k := range s.previous {
		if hmac.Equal([]byte(sig), []byte(mac(k, path, query))) {
			return true
		}
	}

	return false
}

func (s *Signer) sign(path, query string) string { return mac(s.key, path, query) }

func mac(key []byte, path, query string) string {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(path + "?" + query))

	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}
