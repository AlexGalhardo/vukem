package stripe_test

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/AlexGalhardo/vukem/contrib/stripe"
)

func TestSubscriptionAndCheckoutLists(t *testing.T) {
	t.Parallel()

	var (
		mu  sync.Mutex
		got []string
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		got = append(got, r.Method+" "+r.URL.Path+"?"+r.URL.RawQuery)
		mu.Unlock()

		switch r.Method + " " + r.URL.Path {
		case "GET /v1/subscriptions":
			_, _ = w.Write([]byte(`{"object":"list","has_more":true,"data":[
				{"id":"sub_2","customer":{"id":"cus_1","object":"customer"},"status":"active","created":1700000200},
				{"id":"sub_1","customer":"cus_1","status":"canceled","created":1700000100}]}`))
		case "GET /v1/checkout/sessions":
			_, _ = w.Write([]byte(`{"object":"list","has_more":false,"data":[
				{"id":"cs_1","status":"open","customer":"cus_1","created":1700000300,"expires_at":1700086700}]}`))
		case "POST /v1/checkout/sessions/cs_test_a1B2/expire":
			_, _ = w.Write([]byte(`{"id":"cs_1","status":"expired","created":1700000300}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	c, err := stripe.New(key, stripe.WithBaseURL(srv.URL))
	if err != nil {
		t.Fatal(err)
	}

	ctx := t.Context()

	subs, err := c.ListSubscriptions(ctx, stripe.Params{"customer": "cus_1", "status": "all"})
	if err != nil || len(subs.Data) != 2 || !subs.HasMore {
		t.Fatalf("ListSubscriptions = %+v, %v", subs, err)
	}

	if s := subs.Data[0]; s.ID != "sub_2" || s.Customer != "cus_1" || s.Created != 1700000200 {
		t.Errorf("subscription = %+v", s)
	}

	sessions, err := c.ListCheckoutSessions(ctx, stripe.Params{"customer": "cus_1", "status": "open"})
	if err != nil || len(sessions.Data) != 1 {
		t.Fatalf("ListCheckoutSessions = %+v, %v", sessions, err)
	}

	if s := sessions.Data[0]; s.Created != 1700000300 || s.ExpiresAt != 1700086700 {
		t.Errorf("session = %+v", s)
	}

	cs, err := c.ExpireCheckoutSession(ctx, "cs_test_a1B2")
	if err != nil || cs.Status != "expired" {
		t.Fatalf("ExpireCheckoutSession = %+v, %v", cs, err)
	}

	want := []string{
		"GET /v1/subscriptions?customer=cus_1&status=all",
		"GET /v1/checkout/sessions?customer=cus_1&status=open",
		"POST /v1/checkout/sessions/cs_test_a1B2/expire?",
	}
	mu.Lock()
	defer mu.Unlock()

	if len(got) != len(want) {
		t.Fatalf("requests = %q", got)
	}

	for i := range want {
		if got[i] != want[i] {
			t.Errorf("request %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestExpireCheckoutSessionRejectsForeignID(t *testing.T) {
	t.Parallel()

	c, err := stripe.New(key, stripe.WithBaseURL("http://127.0.0.1:1"))
	if err != nil {
		t.Fatal(err)
	}

	if _, err := c.ExpireCheckoutSession(t.Context(), "../customers/cus_1"); err == nil {
		t.Fatal("ExpireCheckoutSession accepted a non-cs_ id")
	}
}
