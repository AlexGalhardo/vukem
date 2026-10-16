package crypt

import (
	"errors"
	"fmt"
	"net/http"
)

// maxCookieValue is the browser limit for a single cookie value.
const maxCookieValue = 4000

// ErrCookieTooLarge is returned by SetCookie when the encrypted value
// exceeds 4 KB; keep large data server-side.
var ErrCookieTooLarge = errors.New("crypt: encrypted cookie value exceeds 4 KB")

// cookieAD binds a cookie value to its name, so a value cannot be moved to
// another cookie, nor confused with tokens sealed for other purposes.
func cookieAD(name string) []byte { return []byte("cookie:" + name) }

// SetCookie encrypts c.Value with the cookie name as additional data and
// adds the Set-Cookie header. Defaults are secure: HttpOnly is always set,
// SameSite defaults to Lax, and Secure is set when r arrived over TLS (set
// c.Secure yourself behind a TLS-terminating proxy). Nothing is written on
// error.
//
//nolint:gosec // G124: HttpOnly is forced, SameSite defaults to Lax and Secure follows TLS below
func (e *Encrypter) SetCookie(w http.ResponseWriter, r *http.Request, c http.Cookie) error {
	c.Value = e.Seal([]byte(c.Value), cookieAD(c.Name))
	c.HttpOnly = true
	c.Secure = c.Secure || r.TLS != nil

	if c.SameSite == 0 || c.SameSite == http.SameSiteDefaultMode {
		c.SameSite = http.SameSiteLaxMode
	}

	if c.SameSite == http.SameSiteNoneMode && !c.Secure {
		return errors.New("crypt: SameSite=None cookies must be Secure")
	}

	if len(c.Value) > maxCookieValue {
		return ErrCookieTooLarge
	}

	if err := c.Valid(); err != nil {
		return fmt.Errorf("crypt: cookie %q: %w", c.Name, err)
	}

	http.SetCookie(w, &c)

	return nil
}

// Cookie returns the decrypted value of the request cookie name, set by
// SetCookie. It returns http.ErrNoCookie when the cookie is absent and
// ErrInvalid when it was tampered with, renamed or sealed with an unknown
// key.
func (e *Encrypter) Cookie(r *http.Request, name string) (string, error) {
	c, err := r.Cookie(name)
	if err != nil {
		return "", err
	}

	plain, err := e.Open(c.Value, cookieAD(name))
	if err != nil {
		return "", err
	}

	return string(plain), nil
}
