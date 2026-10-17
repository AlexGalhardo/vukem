package webhook

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/AlexGalhardo/vukem/cache"
	"github.com/AlexGalhardo/vukem/web"
)

var stdKey = []byte("0123456789abcdef0123456789abcdef")

func stdSign(id string, ts int64, body string) string {
	m := hmac.New(sha256.New, stdKey)
	m.Write([]byte(id + "." + strconv.FormatInt(ts, 10) + "." + body))

	return "v1," + base64.StdEncoding.EncodeToString(m.Sum(nil))
}

func serve(t *testing.T, h http.Handler, body string, header map[string]string) *httptest.ResponseRecorder {
	t.Helper()

	rec := httptest.NewRecorder()
	r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/hook", strings.NewReader(body))

	for k, v := range header {
		r.Header.Set(k, v)
	}

	h.ServeHTTP(rec, r.WithContext(web.WithServices(r.Context(), &web.Services{Log: slog.New(slog.DiscardHandler)})))

	return rec
}

func TestSecurity_A08_StandardWebhooks(t *testing.T) {
	t.Parallel()

	now := time.Unix(1_800_000_000, 0)
	secret := "whsec_" + base64.StdEncoding.EncodeToString(stdKey)

	mw, err := Standard(secret, StandardOptions{MaxBody: 64, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}

	var got string

	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got = string(b)
		w.WriteHeader(http.StatusNoContent)
	}))

	body := `{"type":"paid"}`
	ts := now.Unix()

	headers := func(id string, ts int64, sig string) map[string]string {
		return map[string]string{
			"webhook-id": id, "webhook-timestamp": strconv.FormatInt(ts, 10), "webhook-signature": sig,
		}
	}

	tests := []struct {
		name   string
		body   string
		header map[string]string
		want   int
	}{
		{"valid", body, headers("msg_1", ts, stdSign("msg_1", ts, body)), 204},
		{"one of several signatures", body, headers("msg_1", ts, "v1,AAAA "+stdSign("msg_1", ts, body)), 204},
		{"tampered body", `{"type":"refund"}`, headers("msg_1", ts, stdSign("msg_1", ts, body)), 401},
		{"id swapped", body, headers("msg_2", ts, stdSign("msg_1", ts, body)), 401},
		{"too old (replay)", body, headers("msg_1", ts-600, stdSign("msg_1", ts-600, body)), 401},
		{"too far in future", body, headers("msg_1", ts+600, stdSign("msg_1", ts+600, body)), 401},
		{"bad timestamp", body, headers("msg_1", 0, stdSign("msg_1", ts, body)), 401},
		{"missing id", body, headers("", ts, stdSign("", ts, body)), 401},
		{"too large", strings.Repeat("x", 65), headers("m", ts, stdSign("m", ts, strings.Repeat("x", 65))), 413},
	}

	for _, tt := range tests {
		got = ""

		rec := serve(t, h, tt.body, tt.header)
		if rec.Code != tt.want {
			t.Errorf("%s: status = %d, want %d", tt.name, rec.Code, tt.want)
		}

		if tt.want == 204 && got != tt.body {
			t.Errorf("%s: handler body = %q, want %q", tt.name, got, tt.body)
		}
	}
}

func TestStandardRejectsBadSecret(t *testing.T) {
	t.Parallel()

	for _, s := range []string{"", "whsec_", "whsec_not base64!"} {
		if _, err := Standard(s, StandardOptions{}); err == nil {
			t.Errorf("Standard(%q) = nil error, want error", s)
		}
	}

	if _, err := Standard(base64.StdEncoding.EncodeToString(stdKey), StandardOptions{}); err != nil {
		t.Errorf("Standard(no prefix) = %v, want nil", err)
	}
}

func TestMiddlewareDefaultsMaxBody(t *testing.T) {
	t.Parallel()

	h := Middleware("X-Signature", secret, 0)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	body := `{"id":1}`
	if rec := serve(t, h, body, map[string]string{"X-Signature": Sign(secret, []byte(body))}); rec.Code != 204 {
		t.Fatalf("maxBody 0 = %d, want 204 (1 MiB default)", rec.Code)
	}
}

func TestSecurity_A08_WebhookDedupe(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32

	status := http.StatusNoContent
	h := Dedupe(cache.NewMemory(100), StandardID, DedupeOptions{})(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			calls.Add(1)
			w.WriteHeader(status)
		}),
	)

	id := map[string]string{"webhook-id": "evt_1"}

	if rec := serve(t, h, "{}", id); rec.Code != 204 || calls.Load() != 1 {
		t.Fatalf("first = %d calls=%d", rec.Code, calls.Load())
	}

	rec := serve(t, h, "{}", id)
	if rec.Code != 200 || calls.Load() != 1 || rec.Header().Get("Webhook-Duplicate") != "true" {
		t.Fatalf("redelivery = %d calls=%d header=%q, want 200 without running", rec.Code, calls.Load(),
			rec.Header().Get("Webhook-Duplicate"))
	}

	// A failed delivery is not remembered, so the provider's retry runs.
	status = http.StatusInternalServerError
	serve(t, h, "{}", map[string]string{"webhook-id": "evt_2"})

	status = http.StatusNoContent
	if rec := serve(t, h, "{}", map[string]string{"webhook-id": "evt_2"}); rec.Code != 204 || calls.Load() != 3 {
		t.Fatalf("retry after failure = %d calls=%d", rec.Code, calls.Load())
	}

	if rec := serve(t, h, "{}", nil); rec.Code != 400 {
		t.Fatalf("missing id = %d, want 400", rec.Code)
	}
}

type downStore struct{ cache.Store }

func (downStore) Add(context.Context, string, []byte, time.Duration) (bool, error) {
	return false, errors.New("down")
}

func TestSecurity_A04_WebhookDedupeFailsClosed(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32

	h := Dedupe(downStore{}, StandardID, DedupeOptions{})(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		calls.Add(1)
	}))

	if rec := serve(t, h, "{}", map[string]string{"webhook-id": "e"}); rec.Code != 503 || calls.Load() != 0 {
		t.Fatalf("store down = %d calls=%d, want 503 and no call", rec.Code, calls.Load())
	}
}
