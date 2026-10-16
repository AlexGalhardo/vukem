package webhook

import (
	"encoding/base64"
	"encoding/hex"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/AlexGalhardo/vukem/web"
)

var secret = []byte("whsec_test")

func TestVerifyHMAC(t *testing.T) {
	t.Parallel()

	payload := []byte(`{"action":"opened"}`)
	sig := Sign(secret, payload)
	raw, _ := hex.DecodeString(sig)

	for _, s := range []string{sig, "sha256=" + sig, base64.StdEncoding.EncodeToString(raw), base64.RawURLEncoding.EncodeToString(raw)} {
		if !VerifyHMAC(secret, payload, s) {
			t.Errorf("valid signature %q rejected", s)
		}
	}

	for name, s := range map[string]string{
		"empty": "", "garbage": "zzz", "other secret": Sign([]byte("other"), payload), "other body": Sign(secret, []byte("{}")),
		"truncated": sig[:20],
	} {
		if VerifyHMAC(secret, payload, s) {
			t.Errorf("%s accepted", name)
		}
	}

	if VerifyHMAC(nil, payload, sig) {
		t.Error("empty secret accepted")
	}
}

func TestSecurity_A08_WebhookMiddleware(t *testing.T) {
	t.Parallel()

	var got string

	h := Middleware("X-Signature", secret, 64)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got = string(b)
		w.WriteHeader(http.StatusNoContent)
	}))

	send := func(body, sig string) int {
		rec := httptest.NewRecorder()
		r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/hook", strings.NewReader(body))
		r.Header.Set("X-Signature", sig)
		h.ServeHTTP(
			rec,
			r.WithContext(web.WithServices(r.Context(), &web.Services{Log: slog.New(slog.DiscardHandler)})),
		)

		return rec.Code
	}

	body := `{"id":1}`
	if code := send(body, "sha256="+Sign(secret, []byte(body))); code != 204 || got != body {
		t.Fatalf("valid = %d body %q", code, got)
	}

	if code := send(body, Sign(secret, []byte(`{"id":2}`))); code != 401 {
		t.Fatalf("tampered = %d", code)
	}

	long := strings.Repeat("x", 65)
	if code := send(long, Sign(secret, []byte(long))); code != 413 {
		t.Fatalf("too large = %d", code)
	}
}
