package stripe_test

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/AlexGalhardo/vukem/cache"
	"github.com/AlexGalhardo/vukem/contrib/stripe"
	"github.com/AlexGalhardo/vukem/events"
)

func TestSecurity_A08_StripeConnectWebhookSecrets(t *testing.T) {
	t.Parallel()

	const connectSecret = "whsec_connect"

	bus := events.New(slog.New(slog.DiscardHandler), 1)

	var (
		mu  sync.Mutex
		got []string
	)

	events.Listen(bus, func(_ context.Context, r stripe.Received) error {
		mu.Lock()
		defer mu.Unlock()

		got = append(got, r.Event.ID+"@"+r.Event.Account)

		return nil
	})

	h, err := stripe.WebhookHandler(stripe.WebhookConfig{
		Secret: secret, Secrets: []string{connectSecret}, Store: cache.NewMemory(100), Bus: bus,
		Now: func() time.Time { return now }, Log: slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatal(err)
	}

	post := func(body []byte, header string) int {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/stripe/webhook", bytes.NewReader(body))
		req.Header.Set("Stripe-Signature", header)

		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		return rec.Code
	}

	platform := payload("evt_p")
	connect := []byte(`{"id":"evt_c","type":"account.updated","account":"acct_1","created":1792152000,` +
		`"data":{"object":{"id":"acct_1","charges_enabled":true}}}`)

	for name, tc := range map[string]struct {
		body []byte
		sig  string
		want int
	}{
		"platform secret":      {platform, stripe.SignForTest(platform, secret, now), http.StatusOK},
		"connect secret":       {connect, stripe.SignForTest(connect, connectSecret, now), http.StatusOK},
		"unknown secret":       {connect, stripe.SignForTest(connect, "whsec_other", now), http.StatusBadRequest},
		"stale connect replay": {connect, stripe.SignForTest(connect, connectSecret, now.Add(-time.Hour)), http.StatusBadRequest},
	} {
		if code := post(tc.body, tc.sig); code != tc.want {
			t.Errorf("%s = %d, want %d", name, code, tc.want)
		}
	}

	mu.Lock()
	defer mu.Unlock()

	if want := []string{"evt_c@acct_1", "evt_p@"}; !slices.Equal(want, slices.Sorted(slices.Values(got))) {
		t.Errorf("dispatched = %v, want %v", got, want)
	}

	ev, err := stripe.ConstructEvent(connect, stripe.SignForTest(connect, connectSecret, now), connectSecret,
		stripe.DefaultTolerance, now)
	if err != nil || ev.Account != "acct_1" {
		t.Errorf("ConstructEvent(connect) = %+v, %v", ev, err)
	}

	a, err := stripe.Decode[stripe.Account](ev)
	if err != nil || !a.ChargesEnabled {
		t.Errorf("Decode[Account] = %+v, %v", a, err)
	}

	if _, err := stripe.WebhookHandler(stripe.WebhookConfig{
		Secrets: []string{connectSecret, ""}, Store: cache.NewMemory(1), Bus: bus,
	}); err == nil {
		t.Error("handler with an empty secret accepted")
	}

	if _, err := stripe.WebhookHandler(stripe.WebhookConfig{
		Secrets: []string{connectSecret}, Store: cache.NewMemory(1), Bus: bus,
	}); err != nil {
		t.Errorf("handler with only Secrets = %v", err)
	}
}

func TestWebhookSecretsOrder(t *testing.T) {
	t.Parallel()

	var logs bytes.Buffer

	bus := events.New(slog.New(slog.DiscardHandler), 1)
	events.Listen(bus, func(context.Context, stripe.Received) error { return nil })

	h, err := stripe.WebhookHandler(stripe.WebhookConfig{
		Secrets: []string{"whsec_first", "whsec_second"}, Store: cache.NewMemory(10), Bus: bus,
		Now: func() time.Time { return now }, Log: slog.New(slog.NewTextHandler(&logs, nil)),
	})
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name    string
		body    []byte
		secret  string
		want    int
		wantLog string
	}{
		{"first secret fails, second validates", payload("evt_2nd"), "whsec_second", http.StatusOK, ""},
		{
			"signed non-event stops the search",
			[]byte(`{"hello":"world"}`),
			"whsec_first",
			http.StatusBadRequest,
			"not an event",
		},
	}

	for _, tt := range tests {
		logs.Reset()

		req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/stripe/webhook", bytes.NewReader(tt.body))
		req.Header.Set("Stripe-Signature", stripe.SignForTest(tt.body, tt.secret, now))

		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		if rec.Code != tt.want || !strings.Contains(logs.String(), tt.wantLog) ||
			strings.Contains(logs.String(), "no matching signature") {
			t.Errorf("%s = %d (log %q), want %d with %q", tt.name, rec.Code, logs.String(), tt.want, tt.wantLog)
		}
	}
}
