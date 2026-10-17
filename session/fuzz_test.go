package session

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// FuzzSessionCookie sends arbitrary cookie values (which must never yield
// session data, since they cannot be sealed without the key) and arbitrary
// sealed payloads (which must decode or reset without a server error).
func FuzzSessionCookie(f *testing.F) {
	m, err := New(Config{CookieKey: testKey})
	if err != nil {
		f.Fatal(err)
	}

	sealer, err := newSealer(testKey)
	if err != nil {
		f.Fatal(err)
	}

	h := appHandler(m, &bytes.Buffer{})

	f.Add("", []byte(`{"user":"alice","_created":1,"_last":1}`))
	f.Add("AAAA", []byte(`{}`))
	f.Add("not base64!", []byte(`null`))
	f.Add(sealer.seal("other", []byte(`{"user":"mallory"}`)), []byte(`{"_created":"x"}`))
	f.Add("x", []byte(`{"user":{"nested":[1,2,3]},"_created":9e999}`))

	get := func(t *testing.T, value string) *httptest.ResponseRecorder {
		r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/get", http.NoBody)
		r.AddCookie(&http.Cookie{Name: m.CookieName(), Value: value})

		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)

		if w.Code >= 500 {
			t.Fatalf("GET /get with cookie %q = %d, want no server error", value, w.Code)
		}

		return w
	}

	f.Fuzz(func(t *testing.T, value string, payload []byte) {
		if strings.ContainsFunc(
			value,
			func(r rune) bool { return r <= ' ' || r >= 0x7f || strings.ContainsRune(`";,\`, r) },
		) {
			return
		}

		if body := get(t, value).Body.String(); body != "" {
			t.Fatalf("forged cookie %q produced session data %q", value, body)
		}

		get(t, sealer.seal(m.CookieName(), payload))
	})
}
