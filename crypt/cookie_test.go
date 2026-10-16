package crypt

import (
	"crypto/tls"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func cookieRequest(t *testing.T, https bool) *http.Request {
	t.Helper()

	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody)
	if https {
		r.TLS = &tls.ConnectionState{}
	}

	return r
}

func setCookie(t *testing.T, e *Encrypter, r *http.Request, c http.Cookie) *http.Cookie {
	t.Helper()

	w := httptest.NewRecorder()
	if err := e.SetCookie(w, r, c); err != nil {
		t.Fatalf("SetCookie() error = %v", err)
	}

	cookies := w.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("SetCookie() wrote %d cookies, want 1", len(cookies))
	}

	return cookies[0]
}

func TestSecurity_A04_EncryptedCookieDefaults(t *testing.T) {
	t.Parallel()

	e := mustNew(t, newKey(t))

	tests := []struct {
		name       string
		https      bool
		in         http.Cookie
		wantSecure bool
		wantSite   http.SameSite
	}{
		{name: "http", in: http.Cookie{Name: "cart", Value: "42"}, wantSite: http.SameSiteLaxMode},
		{
			name:       "https",
			https:      true,
			in:         http.Cookie{Name: "cart", Value: "42"},
			wantSecure: true,
			wantSite:   http.SameSiteLaxMode,
		},
		{
			name:     "explicit strict",
			in:       http.Cookie{Name: "cart", Value: "42", SameSite: http.SameSiteStrictMode},
			wantSite: http.SameSiteStrictMode,
		},
		{
			name:       "explicit secure over http",
			in:         http.Cookie{Name: "cart", Value: "42", Secure: true},
			wantSecure: true,
			wantSite:   http.SameSiteLaxMode,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := setCookie(t, e, cookieRequest(t, tt.https), tt.in)

			type flags struct {
				HTTPOnly, Secure bool
				SameSite         http.SameSite
			}

			want := flags{HTTPOnly: true, Secure: tt.wantSecure, SameSite: tt.wantSite}
			if diff := cmp.Diff(want, flags{got.HttpOnly, got.Secure, got.SameSite}); diff != "" {
				t.Fatalf("cookie flags mismatch (-want +got):\n%s", diff)
			}

			if got.Value == tt.in.Value {
				t.Fatalf("cookie value %q is not encrypted", got.Value)
			}
		})
	}
}

func TestEncryptedCookieRoundTrip(t *testing.T) {
	t.Parallel()

	e := mustNew(t, newKey(t))
	set := setCookie(t, e, cookieRequest(t, false), http.Cookie{Name: "cart", Value: "42", Path: "/"})

	r := cookieRequest(t, false)
	r.AddCookie(&http.Cookie{Name: "cart", Value: set.Value})

	got, err := e.Cookie(r, "cart")
	if err != nil || got != "42" {
		t.Fatalf("Cookie(cart) = %q, %v, want 42, nil", got, err)
	}

	if _, err := e.Cookie(r, "missing"); !errors.Is(err, http.ErrNoCookie) {
		t.Fatalf("Cookie(missing) error = %v, want http.ErrNoCookie", err)
	}
}

func TestSecurity_A04_EncryptedCookieCannotMoveBetweenNames(t *testing.T) {
	t.Parallel()

	e := mustNew(t, newKey(t))
	set := setCookie(t, e, cookieRequest(t, false), http.Cookie{Name: "role", Value: "guest"})

	r := cookieRequest(t, false)
	r.AddCookie(&http.Cookie{Name: "admin_role", Value: set.Value})
	r.AddCookie(&http.Cookie{Name: "tampered", Value: set.Value[:len(set.Value)-2] + "xx"})

	for _, name := range []string{"admin_role", "tampered"} {
		if _, err := e.Cookie(r, name); !errors.Is(err, ErrInvalid) {
			t.Fatalf("Cookie(%s) error = %v, want ErrInvalid", name, err)
		}
	}

	if got, err := e.Open(set.Value, nil); err == nil {
		t.Fatalf("cookie token opened without its name as AD: %q", got)
	}
}

func TestSetCookieRejects(t *testing.T) {
	t.Parallel()

	e := mustNew(t, newKey(t))

	tests := []struct {
		name string
		in   http.Cookie
		want error
	}{
		{name: "too large", in: http.Cookie{Name: "big", Value: strings.Repeat("x", 4000)}, want: ErrCookieTooLarge},
		{name: "invalid name", in: http.Cookie{Name: "bad name", Value: "v"}},
		{name: "samesite none without secure", in: http.Cookie{Name: "c", Value: "v", SameSite: http.SameSiteNoneMode}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			w := httptest.NewRecorder()

			err := e.SetCookie(w, cookieRequest(t, false), tt.in)
			if err == nil || tt.want != nil && !errors.Is(err, tt.want) {
				t.Fatalf("SetCookie() error = %v, want %v", err, tt.want)
			}

			if h := w.Header().Get("Set-Cookie"); h != "" {
				t.Fatalf("SetCookie() wrote %q on error", h)
			}
		})
	}
}
