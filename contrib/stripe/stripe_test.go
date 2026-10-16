package stripe_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/AlexGalhardo/vukem/cache"
	"github.com/AlexGalhardo/vukem/contrib/stripe"
	"github.com/AlexGalhardo/vukem/events"
)

const key = "rk_test_51abcdefghijklmnop"

// fakeAPI is an in-process Stripe API.
type fakeAPI struct {
	*httptest.Server

	mu       sync.Mutex
	failures int // answer 503 this many times first
	idem     map[string]string
	requests []*http.Request
	forms    []string
}

func newFakeAPI(t *testing.T) *fakeAPI {
	t.Helper()

	f := &fakeAPI{idem: map[string]string{}}
	f.Server = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.Close)

	return f
}

func (f *fakeAPI) serve(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()

	f.mu.Lock()
	f.requests = append(f.requests, r)
	f.forms = append(f.forms, r.PostForm.Encode())

	if f.failures > 0 {
		f.failures--
		f.mu.Unlock()
		w.Header().Set("Retry-After", "0")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":{"type":"api_error","message":"try again"}}`))

		return
	}
	f.mu.Unlock()

	w.Header().Set("Request-Id", "req_123")

	switch {
	case r.Header.Get("Authorization") != "Bearer "+key:
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"type":"authentication_error","message":"Invalid API Key"}}`))
	case r.Header.Get("Stripe-Version") != stripe.APIVersion:
		w.WriteHeader(http.StatusBadRequest)
	case r.Method == http.MethodPost && r.URL.Path == "/v1/customers":
		_, _ = fmt.Fprintf(w, `{"id":"cus_1","email":%q,"name":%q,"metadata":{"user_id":%q}}`,
			r.PostForm.Get("email"), r.PostForm.Get("name"), r.PostForm.Get("metadata[user_id]"))
	case r.URL.Path == "/v1/checkout/sessions":
		_, _ = fmt.Fprintf(w, `{"id":"cs_1","url":"https://checkout.stripe.com/c/cs_1","mode":%q,"customer":%q}`,
			r.PostForm.Get("mode"), r.PostForm.Get("customer"))
	case r.URL.Path == "/v1/payment_intents" && r.PostForm.Get("amount") == "1":
		w.WriteHeader(http.StatusPaymentRequired)
		_, _ = w.Write(
			[]byte(
				`{"error":{"type":"card_error","code":"card_declined","decline_code":"insufficient_funds","message":"Your card was declined."}}`,
			),
		)
	case r.URL.Path == "/v1/billing_portal/sessions":
		_, _ = w.Write([]byte(`{"id":"bps_1","url":"https://billing.stripe.com/p/session/1"}`))
	default:
		http.NotFound(w, r)
	}
}

func client(t *testing.T, f *fakeAPI) *stripe.Client {
	t.Helper()

	c, err := stripe.New(key, stripe.WithBaseURL(f.URL))
	if err != nil {
		t.Fatal(err)
	}

	return c
}

func TestClientCalls(t *testing.T) {
	t.Parallel()

	f := newFakeAPI(t)
	c := client(t, f)
	ctx := t.Context()

	cus, err := c.CreateCustomer(
		ctx,
		stripe.Params{"email": "ana@example.com", "name": "Ana", "metadata": map[string]string{"user_id": "42"}},
	)
	if err != nil || cus.ID != "cus_1" || cus.Metadata["user_id"] != "42" {
		t.Fatalf("CreateCustomer = %+v, %v", cus, err)
	}

	cs, err := c.CreateCheckoutSession(ctx, stripe.Params{
		"mode": "subscription", "customer": cus.ID,
		"line_items":  []stripe.Params{{"price": "price_pro", "quantity": 1}},
		"success_url": "https://app.test/billing?ok=1",
	})
	if err != nil || cs.Mode != "subscription" || cs.Customer != "cus_1" {
		t.Fatalf("CreateCheckoutSession = %+v, %v", cs, err)
	}

	if !strings.Contains(f.forms[1], "line_items%5B0%5D%5Bprice%5D=price_pro") ||
		strings.Contains(f.forms[1], "payment_method_types") {
		t.Errorf("checkout form = %s", f.forms[1])
	}

	if ps, err := c.CreatePortalSession(ctx, "cus_1", "https://app.test/billing"); err != nil || ps.URL == "" {
		t.Errorf("CreatePortalSession = %+v, %v", ps, err)
	}

	for _, r := range f.requests {
		if r.Method == http.MethodPost && r.Header.Get("Idempotency-Key") == "" {
			t.Errorf("%s %s without Idempotency-Key", r.Method, r.URL.Path)
		}
	}
}

func TestTypedErrors(t *testing.T) {
	t.Parallel()

	f := newFakeAPI(t)
	c := client(t, f)

	_, err := c.CreatePaymentIntent(t.Context(), stripe.Params{"amount": 1, "currency": "usd"}, "order-1")

	var se *stripe.Error
	if !errors.As(err, &se) || !errors.Is(err, stripe.ErrCardDeclined) || se.DeclineCode != "insufficient_funds" ||
		se.Status != http.StatusPaymentRequired || se.RequestID != "req_123" {
		t.Fatalf("declined = %#v", err)
	}

	if len(f.requests) != 1 {
		t.Errorf("card error retried %d times", len(f.requests)-1)
	}

	bad, _ := stripe.New("rk_test_wrong", stripe.WithBaseURL(f.URL))
	if _, err := bad.GetCustomer(t.Context(), "cus_1"); !errors.Is(err, stripe.ErrAuth) {
		t.Errorf("bad key = %v", err)
	}

	if _, err := stripe.New("pk_live_abc"); err == nil {
		t.Error("publishable key accepted")
	}
}

func TestRetriesKeepIdempotencyKey(t *testing.T) {
	t.Parallel()

	f := newFakeAPI(t)
	f.failures = 2

	if _, err := client(t, f).CreateCustomer(t.Context(), stripe.Params{"email": "a@b.co"}); err != nil {
		t.Fatalf("CreateCustomer after 2 failures = %v", err)
	}

	if len(f.requests) != 3 {
		t.Fatalf("requests = %d, want 3", len(f.requests))
	}

	k := f.requests[0].Header.Get("Idempotency-Key")
	for _, r := range f.requests {
		if r.Header.Get("Idempotency-Key") != k {
			t.Error("retries changed the Idempotency-Key")
		}
	}

	f2 := newFakeAPI(t)
	f2.failures = 10

	if _, err := client(t, f2).CreateCustomer(t.Context(), nil); err == nil || len(f2.requests) != 3 {
		t.Errorf("persistent 503 = %v after %d requests, want error after 3", err, len(f2.requests))
	}
}

func TestSecurity_A09_StripeKeyRedacted(t *testing.T) {
	t.Parallel()

	c, err := stripe.New(key)
	if err != nil {
		t.Fatal(err)
	}

	var logs bytes.Buffer

	slog.New(slog.NewTextHandler(&logs, nil)).Info("client", "c", c)

	for _, s := range []string{c.String(), fmt.Sprintf("%v %+v %#v %s", c, c, c, c), logs.String()} {
		if strings.Contains(s, "51abcdefghij") {
			t.Errorf("key leaked: %s", s)
		}
	}
}

var (
	secret = "whsec_test_secret"
	now    = time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
)

func payload(id string) []byte {
	return []byte(`{"id":"` + id + `","type":"checkout.session.completed","created":1792152000,` +
		`"data":{"object":{"id":"cs_1","mode":"subscription","payment_status":"paid","customer":"cus_1"}}}`)
}

func TestSecurity_A08_StripeWebhookSignature(t *testing.T) {
	t.Parallel()

	good := payload("evt_1")

	tests := map[string]string{
		"missing header":    "",
		"forged signature":  "t=" + strconv.FormatInt(now.Unix(), 10) + ",v1=" + strings.Repeat("ab", 32),
		"other secret":      stripe.SignForTest(good, "whsec_other", now),
		"stale (replay)":    stripe.SignForTest(good, secret, now.Add(-10*time.Minute)),
		"from the future":   stripe.SignForTest(good, secret, now.Add(10*time.Minute)),
		"no v1":             "t=" + strconv.FormatInt(now.Unix(), 10) + ",v0=abc",
		"tampered payload":  stripe.SignForTest(payload("evt_2"), secret, now),
		"garbage timestamp": "t=abc,v1=00",
	}

	for name, header := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if _, err := stripe.ConstructEvent(
				good,
				header,
				secret,
				stripe.DefaultTolerance,
				now,
			); !errors.Is(
				err,
				stripe.ErrSignature,
			) {
				t.Errorf("ConstructEvent(%s) = %v, want ErrSignature", name, err)
			}
		})
	}

	// Several signatures (secret rotation): any match is enough.
	multi := "t=" + strconv.FormatInt(now.Unix(), 10) + ",v1=" + strings.Repeat("00", 32) + "," +
		strings.TrimPrefix(stripe.SignForTest(good, secret, now), "t="+strconv.FormatInt(now.Unix(), 10)+",")

	ev, err := stripe.ConstructEvent(good, multi, secret, stripe.DefaultTolerance, now)
	if err != nil || ev.ID != "evt_1" {
		t.Fatalf("multi-signature header = %v", err)
	}

	cs, err := stripe.Decode[stripe.CheckoutSession](ev)
	if err != nil || cs.PaymentStatus != "paid" || cs.Customer != "cus_1" {
		t.Errorf("Decode = %+v, %v", cs, err)
	}
}

func TestWebhookHandlerProcessesOnce(t *testing.T) {
	t.Parallel()

	bus := events.New(slog.New(slog.DiscardHandler), 1)

	var (
		calls atomic.Int32
		fail  atomic.Bool
	)

	events.Listen(bus, func(context.Context, stripe.Received) error {
		calls.Add(1)

		if fail.Load() {
			return errors.New("db down")
		}

		return nil
	})

	h, err := stripe.WebhookHandler(stripe.WebhookConfig{
		Secret: secret, Store: cache.NewMemory(100), Bus: bus,
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

	body := payload("evt_10")
	sig := stripe.SignForTest(body, secret, now)

	fail.Store(true)

	if code := post(body, sig); code != http.StatusInternalServerError {
		t.Fatalf("failing listener = %d, want 500 (Stripe redelivers)", code)
	}

	fail.Store(false)

	for range 3 {
		if code := post(body, sig); code != http.StatusOK {
			t.Fatalf("delivery = %d", code)
		}
	}

	if got := calls.Load(); got != 2 {
		t.Errorf("listener calls = %d, want 2 (failed attempt + one success, duplicates skipped)", got)
	}

	if code := post(body, "t=1,v1=00"); code != http.StatusBadRequest {
		t.Errorf("forged webhook = %d, want 400", code)
	}

	if _, err := stripe.WebhookHandler(stripe.WebhookConfig{Secret: secret, Bus: bus}); err == nil {
		t.Error("handler without an event store accepted")
	}
}
