package stripe_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/AlexGalhardo/vukem/contrib/stripe"
)

// seen is one request the Connect fake received.
type seen struct {
	Method, Path, Account, Idem, Form string
}

// connectAPI answers every request with a canned body per path and records
// what it saw.
type connectAPI struct {
	*httptest.Server

	mu   sync.Mutex
	seen []seen
}

var connectBodies = map[string]string{
	"/v1/accounts": `{"id":"acct_1","type":"express","charges_enabled":false,"details_submitted":false,` +
		`"requirements":{"currently_due":["external_account"],"disabled_reason":"requirements.past_due"}}`,
	"/v1/accounts/acct_1":             `{"id":"acct_1","type":"express","charges_enabled":true,"payouts_enabled":true,"details_submitted":true}`,
	"/v1/account_links":               `{"object":"account_link","created":1680577733,"expires_at":1680578033,"url":"https://connect.stripe.com/setup/c/acct_1/x"}`,
	"/v1/accounts/acct_1/login_links": `{"object":"login_link","created":1680577733,"url":"https://connect.stripe.com/express/acct_1/y"}`,
	"/v1/transfers": `{"id":"tr_1","amount":400,"currency":"usd","destination":"acct_1",` +
		`"destination_payment":"py_1","transfer_group":"ORDER_95","source_transaction":"ch_1"}`,
	"/v1/transfers/tr_1": `{"id":"tr_1","amount":400,"amount_reversed":400,"reversed":true,"currency":"usd","destination":"acct_1"}`,
	"/v1/payment_intents": `{"id":"pi_1","amount":1000,"currency":"usd","application_fee_amount":100,` +
		`"on_behalf_of":"acct_1","transfer_data":{"destination":"acct_1"},"transfer_group":"ORDER_95"}`,
	"/v1/payment_intents/pi_1": `{"id":"pi_1","amount":1000,"currency":"usd"}`,
	"/v1/checkout/sessions":    `{"id":"cs_1","url":"https://checkout.stripe.com/c/cs_1","mode":"payment"}`,
	"/v1/refunds":              `{"id":"re_1"}`,
}

func newConnectAPI(t *testing.T) *connectAPI {
	t.Helper()

	f := &connectAPI{}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()

		f.mu.Lock()
		f.seen = append(f.seen, seen{
			Method: r.Method, Path: r.URL.Path, Account: r.Header.Get("Stripe-Account"),
			Idem: r.Header.Get("Idempotency-Key"), Form: r.PostForm.Encode(),
		})
		f.mu.Unlock()

		if r.Header.Get("Authorization") != "Bearer "+key {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		body, ok := connectBodies[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}

		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(f.Close)

	return f
}

func (f *connectAPI) last(t *testing.T) seen {
	t.Helper()

	f.mu.Lock()
	defer f.mu.Unlock()

	if len(f.seen) == 0 {
		t.Fatal("no request reached the fake API")
	}

	return f.seen[len(f.seen)-1]
}

func connectClient(t *testing.T, f *connectAPI) *stripe.Client {
	t.Helper()

	c, err := stripe.New(key, stripe.WithBaseURL(f.URL), stripe.WithRetries(0))
	if err != nil {
		t.Fatal(err)
	}

	return c
}

func TestConnectResources(t *testing.T) {
	t.Parallel()

	f := newConnectAPI(t)
	c := connectClient(t, f)
	ctx := t.Context()

	acct, err := c.CreateAccount(ctx, stripe.Params{
		"controller": stripe.Params{
			"stripe_dashboard": stripe.Params{"type": "express"},
			"fees":             stripe.Params{"payer": "application"},
			"losses":           stripe.Params{"payments": "application"},
		},
		"email": "seller@example.com",
	}, stripe.IdempotencyKey("seller-7"))
	if err != nil {
		t.Fatalf("CreateAccount = %v", err)
	}

	if !slices.Equal([]string{"external_account"}, acct.Requirements.CurrentlyDue) ||
		acct.ID != "acct_1" || acct.Type != "express" || acct.Requirements.DisabledReason == "" {
		t.Errorf("CreateAccount = %+v", acct)
	}

	got := f.last(t)
	if got.Idem != "seller-7" || !strings.Contains(got.Form, "controller%5Bstripe_dashboard%5D%5Btype%5D=express") {
		t.Errorf("CreateAccount request = %+v", got)
	}

	acct, err = c.GetAccount(ctx, "acct_1")
	if err != nil || !acct.ChargesEnabled || !acct.PayoutsEnabled || !acct.DetailsSubmitted {
		t.Errorf("GetAccount = %+v, %v", acct, err)
	}

	link, err := c.CreateAccountLink(ctx, stripe.Params{
		"account": "acct_1", "type": "account_onboarding",
		"refresh_url": "https://app.test/connect/refresh", "return_url": "https://app.test/connect/return",
	})
	if err != nil || link.URL == "" || link.ExpiresAt != 1680578033 {
		t.Errorf("CreateAccountLink = %+v, %v", link, err)
	}

	login, err := c.CreateLoginLink(ctx, "acct_1")
	if err != nil || login.URL == "" || f.last(t).Path != "/v1/accounts/acct_1/login_links" {
		t.Errorf("CreateLoginLink = %+v, %v (path %s)", login, err, f.last(t).Path)
	}

	tr, err := c.CreateTransfer(ctx, stripe.Params{
		"amount": 400, "currency": "usd", "destination": "acct_1",
		"transfer_group": "ORDER_95", "source_transaction": "ch_1",
	}, stripe.IdempotencyKey("order-95-transfer"))
	if err != nil || tr.Amount != 400 || tr.Destination != "acct_1" || tr.DestinationPayment != "py_1" ||
		tr.SourceTransaction != "ch_1" || f.last(t).Idem != "order-95-transfer" {
		t.Errorf("CreateTransfer = %+v, %v", tr, err)
	}

	tr, err = c.GetTransfer(ctx, "tr_1")
	if err != nil || !tr.Reversed || tr.AmountReversed != 400 {
		t.Errorf("GetTransfer = %+v, %v", tr, err)
	}
}

func TestApplicationFee(t *testing.T) {
	t.Parallel()

	f := newConnectAPI(t)
	c := connectClient(t, f)

	pi, err := c.CreatePaymentIntent(t.Context(), stripe.Params{
		"amount": 1000, "currency": "usd", "application_fee_amount": 100,
		"transfer_data": stripe.Params{"destination": "acct_1"}, "on_behalf_of": "acct_1",
	}, "order-95")
	if err != nil || pi.ApplicationFeeAmount != 100 || pi.OnBehalfOf != "acct_1" ||
		pi.TransferData.Destination != "acct_1" || pi.TransferGroup != "ORDER_95" {
		t.Fatalf("CreatePaymentIntent = %+v, %v", pi, err)
	}

	if form := f.last(t).Form; !strings.Contains(form, "application_fee_amount=100") ||
		!strings.Contains(form, "transfer_data%5Bdestination%5D=acct_1") {
		t.Errorf("payment intent form = %s", form)
	}

	var cs stripe.CheckoutSession

	err = c.Call(t.Context(), http.MethodPost, "/v1/checkout/sessions", stripe.Params{
		"mode": "payment",
		"payment_intent_data": stripe.Params{
			"application_fee_amount": 100, "transfer_data": stripe.Params{"destination": "acct_1"},
		},
	}, &cs, stripe.IdempotencyKey("order-95-checkout"))

	got := f.last(t)
	if err != nil || cs.ID != "cs_1" || got.Idem != "order-95-checkout" ||
		!strings.Contains(got.Form, "payment_intent_data%5Bapplication_fee_amount%5D=100") {
		t.Errorf("Call(checkout) = %+v, %v; request %+v", cs, err, got)
	}
}

func TestStripeAccountHeader(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		call func(context.Context, *stripe.Client) error
		want seen
	}{
		{
			name: "ForAccount on a typed method",
			call: func(ctx context.Context, c *stripe.Client) error {
				_, err := c.ForAccount("acct_1").GetPaymentIntent(ctx, "pi_1")
				return err
			},
			want: seen{Method: http.MethodGet, Path: "/v1/payment_intents/pi_1", Account: "acct_1"},
		},
		{
			name: "OnAccount option on Call",
			call: func(ctx context.Context, c *stripe.Client) error {
				return c.Call(ctx, http.MethodGet, "/v1/accounts/acct_1", nil, nil, stripe.OnAccount("acct_2"))
			},
			want: seen{Method: http.MethodGet, Path: "/v1/accounts/acct_1", Account: "acct_2"},
		},
		{
			name: "option overrides ForAccount",
			call: func(ctx context.Context, c *stripe.Client) error {
				return c.ForAccount("acct_1").Call(ctx, http.MethodGet, "/v1/transfers/tr_1", nil, nil,
					stripe.OnAccount("acct_3"))
			},
			want: seen{Method: http.MethodGet, Path: "/v1/transfers/tr_1", Account: "acct_3"},
		},
		{
			name: "platform client sends no header",
			call: func(ctx context.Context, c *stripe.Client) error {
				_, err := c.GetAccount(ctx, "acct_1")
				return err
			},
			want: seen{Method: http.MethodGet, Path: "/v1/accounts/acct_1"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			f := newConnectAPI(t)
			if err := tt.call(t.Context(), connectClient(t, f)); err != nil {
				t.Fatal(err)
			}

			got := f.last(t)
			got.Form, got.Idem = "", ""

			if got != tt.want {
				t.Errorf("request = %+v, want %+v", got, tt.want)
			}
		})
	}

	c, _ := stripe.New(key)
	if c.ForAccount("acct_1").String() != c.String() {
		t.Error("ForAccount changed how the client prints")
	}
}

func TestIdempotencyKeyOnEveryPost(t *testing.T) {
	t.Parallel()

	f := newConnectAPI(t)
	c := connectClient(t, f)
	ctx := t.Context()

	_, _ = c.CreateAccount(ctx, nil)
	_, _ = c.CreateAccountLink(ctx, nil)
	_, _ = c.CreateLoginLink(ctx, "acct_1")
	_, _ = c.CreateTransfer(ctx, nil)
	_ = c.Call(ctx, http.MethodPost, "/v1/refunds", stripe.Params{"payment_intent": "pi_1"}, nil)
	_ = c.Do(ctx, http.MethodPost, "/v1/refunds", nil, nil)
	_, _ = c.GetAccount(ctx, "acct_1")

	keys := map[string]bool{}

	for _, r := range f.seen {
		switch {
		case r.Method == http.MethodPost && r.Idem == "":
			t.Errorf("POST %s without Idempotency-Key", r.Path)
		case r.Method == http.MethodGet && r.Idem != "":
			t.Errorf("GET %s with Idempotency-Key %q", r.Path, r.Idem)
		case r.Method == http.MethodPost && keys[r.Idem]:
			t.Errorf("POST %s reused generated key %q", r.Path, r.Idem)
		}

		keys[r.Idem] = true
	}
}

func TestSecurity_A03_StripeRequestHeadersValidated(t *testing.T) {
	t.Parallel()

	f := newConnectAPI(t)
	c := connectClient(t, f)

	tests := map[string]func() error{
		"empty account": func() error {
			_, err := c.ForAccount("").GetAccount(t.Context(), "acct_1")
			return err
		},
		"not an account id": func() error {
			return c.Call(t.Context(), http.MethodGet, "/v1/balance", nil, nil, stripe.OnAccount("cus_1"))
		},
		"header injection in account": func() error {
			return c.Call(t.Context(), http.MethodGet, "/v1/balance", nil, nil,
				stripe.OnAccount("acct_1\r\nX-Evil: 1"))
		},
		"idempotency key too long": func() error {
			_, err := c.CreateTransfer(t.Context(), nil, stripe.IdempotencyKey(strings.Repeat("k", 256)))
			return err
		},
		"control character in idempotency key": func() error {
			_, err := c.CreateTransfer(t.Context(), nil, stripe.IdempotencyKey("order\n1"))
			return err
		},
		"login link for a non-account": func() error {
			_, err := c.CreateLoginLink(t.Context(), "../customers/cus_1")
			return err
		},
	}

	for name, call := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if err := call(); !errors.Is(err, stripe.ErrInvalidRequestOption) {
				t.Errorf("%s = %v, want ErrInvalidRequestOption", name, err)
			}
		})
	}

	t.Cleanup(func() {
		f.mu.Lock()
		defer f.mu.Unlock()

		if len(f.seen) != 0 {
			t.Errorf("invalid requests reached the API: %+v", f.seen)
		}
	})
}
