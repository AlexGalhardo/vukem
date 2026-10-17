package stripe_test

import (
	"context"
	"encoding/json"
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
		"v2 account id with a path": func() error {
			_, err := c.GetAccountV2(t.Context(), "acct_1/../../v1/balance", nil)
			return err
		},
		"v2 update of a non-account": func() error {
			_, err := c.UpdateAccountV2(t.Context(), "cus_1", nil)
			return err
		},
		"empty refund id": func() error {
			_, err := c.GetRefund(t.Context(), "")
			return err
		},
		"empty transfer for a reversal": func() error {
			_, err := c.CreateTransferReversal(t.Context(), "", nil)
			return err
		},
		"refund id that is not a refund": func() error {
			_, err := c.GetRefund(t.Context(), "re_1/../../v1/balance")
			return err
		},
		"transfer id that is not a transfer": func() error {
			_, err := c.GetTransfer(t.Context(), "cus_1")
			return err
		},
		"reversal of a non-transfer": func() error {
			_, err := c.CreateTransferReversal(t.Context(), "py_1", nil)
			return err
		},
		"reversal id that is not a reversal": func() error {
			_, err := c.GetTransferReversal(t.Context(), "tr_1", "re_1")
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

func TestSecurity_A01_StripePlatformOnlyRefusesAccountScope(t *testing.T) {
	t.Parallel()

	f := newConnectAPI(t)
	scoped := connectClient(t, f).ForAccount("acct_1")
	platform := connectClient(t, f)
	ctx := t.Context()

	tests := map[string]func() error{
		"CreateAccount via ForAccount": func() error {
			_, err := scoped.CreateAccount(ctx, nil)
			return err
		},
		"GetAccount via ForAccount": func() error {
			_, err := scoped.GetAccount(ctx, "acct_1")
			return err
		},
		"CreateAccountLink via ForAccount": func() error {
			_, err := scoped.CreateAccountLink(ctx, nil)
			return err
		},
		"CreateLoginLink via ForAccount": func() error {
			_, err := scoped.CreateLoginLink(ctx, "acct_1")
			return err
		},
		"CreateTransfer via ForAccount": func() error {
			_, err := scoped.CreateTransfer(ctx, nil)
			return err
		},
		"GetTransfer via ForAccount": func() error {
			_, err := scoped.GetTransfer(ctx, "tr_1")
			return err
		},
		"CreateTransfer with OnAccount": func() error {
			_, err := platform.CreateTransfer(ctx, nil, stripe.OnAccount("acct_1"))
			return err
		},
		"CreateAccountV2 via ForAccount": func() error {
			_, err := scoped.CreateAccountV2(ctx, nil)
			return err
		},
		"GetAccountV2 via ForAccount": func() error {
			_, err := scoped.GetAccountV2(ctx, "acct_1", nil)
			return err
		},
		"UpdateAccountV2 with OnAccount": func() error {
			_, err := platform.UpdateAccountV2(ctx, "acct_1", nil, stripe.OnAccount("acct_1"))
			return err
		},
		"CreateAccountLinkV2 via ForAccount": func() error {
			_, err := scoped.CreateAccountLinkV2(ctx, nil)
			return err
		},
		"CreateTransferReversal via ForAccount": func() error {
			_, err := scoped.CreateTransferReversal(ctx, "tr_1", nil)
			return err
		},
		"GetTransferReversal with OnAccount": func() error {
			_, err := platform.GetTransferReversal(ctx, "tr_1", "trr_1", stripe.OnAccount("acct_1"))
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
			t.Errorf("platform-only calls with an account scope reached the API: %+v", f.seen)
		}
	})
}

func TestExpandedFieldsKeepID(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		body string
		got  func([]byte) (any, error)
		want any
	}{
		{
			name: "PaymentIntent",
			body: `{"id":"pi_1","customer":{"id":"cus_1","object":"customer"},` +
				`"latest_charge":{"id":"ch_1"},"on_behalf_of":{"id":"acct_1"},` +
				`"transfer_data":{"destination":{"id":"acct_2"},"amount":5}}`,
			got: func(b []byte) (any, error) {
				var v stripe.PaymentIntent
				err := json.Unmarshal(b, &v)

				return [5]any{
					v.Customer,
					v.LatestCharge,
					v.OnBehalfOf,
					v.TransferData.Destination,
					v.TransferData.Amount,
				}, err
			},
			want: [5]any{"cus_1", "ch_1", "acct_1", "acct_2", int64(5)},
		},
		{
			name: "PaymentIntent unexpanded",
			body: `{"id":"pi_1","customer":"cus_1","latest_charge":null,"transfer_data":null}`,
			got: func(b []byte) (any, error) {
				var v stripe.PaymentIntent
				err := json.Unmarshal(b, &v)

				return [3]any{v.ID, v.Customer, v.LatestCharge}, err
			},
			want: [3]any{"pi_1", "cus_1", ""},
		},
		{
			name: "Transfer",
			body: `{"id":"tr_1","destination":{"id":"acct_1"},"destination_payment":{"id":"py_1"},` +
				`"source_transaction":{"id":"ch_1"}}`,
			got: func(b []byte) (any, error) {
				var v stripe.Transfer
				err := json.Unmarshal(b, &v)

				return [3]any{v.Destination, v.DestinationPayment, v.SourceTransaction}, err
			},
			want: [3]any{"acct_1", "py_1", "ch_1"},
		},
		{
			name: "CheckoutSession",
			body: `{"id":"cs_1","customer":{"id":"cus_1"},"subscription":{"id":"sub_1"},"payment_intent":{"id":"pi_1"}}`,
			got: func(b []byte) (any, error) {
				var v stripe.CheckoutSession
				err := json.Unmarshal(b, &v)

				return [3]any{v.Customer, v.Subscription, v.PaymentIntent}, err
			},
			want: [3]any{"cus_1", "sub_1", "pi_1"},
		},
		{
			name: "Subscription",
			body: `{"id":"sub_1","customer":{"id":"cus_1"}}`,
			got: func(b []byte) (any, error) {
				var v stripe.Subscription
				err := json.Unmarshal(b, &v)

				return v.Customer, err
			},
			want: "cus_1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := tt.got([]byte(tt.body))
			if err != nil || got != tt.want {
				t.Errorf("Unmarshal(%s) = %v, %v, want %v", tt.body, got, err, tt.want)
			}
		})
	}
}

func TestRetryKeepsCallerIdempotencyKey(t *testing.T) {
	t.Parallel()

	var (
		mu   sync.Mutex
		keys []string
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()

		keys = append(keys, r.Header.Get("Idempotency-Key"))
		if len(keys) == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusInternalServerError)

			return
		}

		_, _ = w.Write([]byte(`{"id":"tr_1"}`))
	}))
	t.Cleanup(srv.Close)

	c, err := stripe.New(key, stripe.WithBaseURL(srv.URL), stripe.WithRetries(2))
	if err != nil {
		t.Fatal(err)
	}

	if _, err := c.CreateTransfer(t.Context(), nil, stripe.IdempotencyKey("order-9-transfer")); err != nil {
		t.Fatalf("CreateTransfer after a 500 = %v", err)
	}

	mu.Lock()
	defer mu.Unlock()

	if want := []string{"order-9-transfer", "order-9-transfer"}; !slices.Equal(keys, want) {
		t.Errorf("Idempotency-Key per attempt = %v, want %v", keys, want)
	}
}
