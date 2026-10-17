package stripe_test

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/AlexGalhardo/vukem/contrib/stripe"
)

// v2Seen is one request the fake received, with its raw body.
type v2Seen struct {
	Method, Path, Query, ContentType, Version, Idem, Account string
	Body                                                     string
}

// v2API is a fake Stripe API answering canned bodies per method+path,
// recording each request.
type v2API struct {
	*httptest.Server

	mu   sync.Mutex
	seen []v2Seen
}

var v2Bodies = map[string]string{
	"POST /v2/core/accounts": `{"id":"acct_1","object":"v2.core.account","applied_configurations":["merchant","recipient"],` +
		`"dashboard":"express","contact_email":"seller@example.com","created":"2026-10-01T12:00:00.000Z",` +
		`"livemode":false,"metadata":{"shop_id":"7"},"configuration":{` +
		`"merchant":{"applied":"2026-10-01T12:00:00.000Z","capabilities":{"card_payments":{"status":"restricted",` +
		`"status_details":[{"code":"requirements_past_due","resolution":"provide_info"}]}}},` +
		`"recipient":{"capabilities":{"stripe_balance":{"stripe_transfers":{"status":"pending","status_details":[]}}}}},` +
		`"requirements":{"entries":[{"description":"identity.business_details.name","awaiting_action_from":"user",` +
		`"minimum_deadline":{"status":"currently_due"}}],"summary":{"minimum_deadline":{"status":"currently_due",` +
		`"time":"2026-11-01T00:00:00.000Z"}}}}`,
	"GET /v2/core/accounts/acct_1": `{"id":"acct_1","object":"v2.core.account","configuration":{"recipient":` +
		`{"capabilities":{"stripe_balance":{"stripe_transfers":{"status":"active","status_details":[]}}}}}}`,
	"POST /v2/core/accounts/acct_1": `{"id":"acct_1","object":"v2.core.account","display_name":"Shop 7"}`,
	"POST /v2/core/account_links": `{"object":"v2.core.account_link","account":"acct_1",` +
		`"created":"2026-10-01T12:00:00.000Z","expires_at":"2026-10-01T12:10:00.000Z","livemode":false,` +
		`"url":"https://accounts.stripe.com/r/acct_1#alu_test_1","use_case":{"type":"account_onboarding"}}`,
	"POST /v1/refunds": `{"id":"re_1","object":"refund","amount":500,"currency":"brl","charge":"ch_1",` +
		`"payment_intent":{"id":"pi_1","object":"payment_intent"},"reason":"requested_by_customer",` +
		`"status":"succeeded","created":1760000000,"metadata":{"seller_order_id":"9"},` +
		`"transfer_reversal":{"id":"trr_9"}}`,
	"GET /v1/refunds/re_1": `{"id":"re_1","amount":500,"status":"failed","failure_reason":"expired_or_canceled_card"}`,
	"POST /v1/transfers/tr_1/reversals": `{"id":"trr_1","object":"transfer_reversal","amount":400,"currency":"brl",` +
		`"transfer":{"id":"tr_1"},"destination_payment_refund":"pyr_1","source_refund":null,"created":1760000000,` +
		`"metadata":{"seller_order_id":"9"}}`,
	"GET /v1/transfers/tr_1/reversals/trr_1": `{"id":"trr_1","amount":400,"transfer":"tr_1"}`,
	"POST /v2/core/accounts/acct_bad":        `{"error":{"type":"invalid_request_error","code":"accounts_v2_access_blocked",` + `"message":"Accounts v2 is not enabled for your merchant."}}`,
}

func newV2API(t *testing.T) *v2API {
	t.Helper()

	f := &v2API{}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)

		f.mu.Lock()
		f.seen = append(f.seen, v2Seen{
			Method: r.Method, Path: r.URL.Path, Query: r.URL.RawQuery,
			ContentType: r.Header.Get("Content-Type"), Version: r.Header.Get("Stripe-Version"),
			Idem: r.Header.Get("Idempotency-Key"), Account: r.Header.Get("Stripe-Account"), Body: string(b),
		})
		f.mu.Unlock()

		body, ok := v2Bodies[r.Method+" "+r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}

		if r.URL.Path == "/v2/core/accounts/acct_bad" {
			w.WriteHeader(http.StatusBadRequest)
		}

		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(f.Close)

	return f
}

func (f *v2API) last(t *testing.T) v2Seen {
	t.Helper()

	f.mu.Lock()
	defer f.mu.Unlock()

	if len(f.seen) == 0 {
		t.Fatal("no request reached the fake API")
	}

	return f.seen[len(f.seen)-1]
}

func v2Client(t *testing.T, f *v2API) *stripe.Client {
	t.Helper()

	c, err := stripe.New(key, stripe.WithBaseURL(f.URL), stripe.WithRetries(0))
	if err != nil {
		t.Fatal(err)
	}

	return c
}

func jsonBody(t *testing.T, s string) map[string]any {
	t.Helper()

	var m map[string]any
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		t.Fatalf("body %q is not JSON: %v", s, err)
	}

	return m
}

func TestAccountsV2(t *testing.T) {
	t.Parallel()

	f := newV2API(t)
	c := v2Client(t, f)
	ctx := t.Context()

	acct, err := c.CreateAccountV2(ctx, stripe.Params{
		"contact_email": "seller@example.com",
		"dashboard":     "express",
		"identity":      stripe.Params{"country": "br"},
		"defaults": stripe.Params{"responsibilities": stripe.Params{
			"fees_collector": "application", "losses_collector": "application",
		}},
		"configuration": stripe.Params{"recipient": stripe.Params{"capabilities": stripe.Params{
			"stripe_balance": stripe.Params{"stripe_transfers": stripe.Params{"requested": true}},
		}}},
		"include":  []string{"configuration.merchant", "configuration.recipient", "requirements"},
		"metadata": map[string]string{"shop_id": "7"},
	}, stripe.IdempotencyKey("shop-7-account"))
	if err != nil {
		t.Fatalf("CreateAccountV2 = %v", err)
	}

	got := f.last(t)
	if got.Method != http.MethodPost || got.Path != "/v2/core/accounts" || got.ContentType != "application/json" ||
		got.Version != stripe.APIVersion || got.Idem != "shop-7-account" || got.Query != "" {
		t.Errorf("CreateAccountV2 request = %+v", got)
	}

	wantBody := map[string]any{
		"contact_email": "seller@example.com",
		"dashboard":     "express",
		"identity":      map[string]any{"country": "br"},
		"defaults": map[string]any{"responsibilities": map[string]any{
			"fees_collector": "application", "losses_collector": "application",
		}},
		"configuration": map[string]any{"recipient": map[string]any{"capabilities": map[string]any{
			"stripe_balance": map[string]any{"stripe_transfers": map[string]any{"requested": true}},
		}}},
		"include":  []any{"configuration.merchant", "configuration.recipient", "requirements"},
		"metadata": map[string]any{"shop_id": "7"},
	}
	if diff := cmp.Diff(wantBody, jsonBody(t, got.Body)); diff != "" {
		t.Errorf("CreateAccountV2 body (-want +got):\n%s", diff)
	}

	deadline := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	want := stripe.AccountV2{
		ID: "acct_1", Object: "v2.core.account", AppliedConfigurations: []string{"merchant", "recipient"},
		Dashboard: "express", ContactEmail: "seller@example.com",
		Created:  time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC),
		Metadata: map[string]string{"shop_id": "7"},
		Configuration: stripe.AccountV2Configuration{
			Merchant: &stripe.AccountV2Merchant{Capabilities: stripe.AccountV2MerchantCapabilities{
				CardPayments: stripe.CapabilityV2{
					Status: "restricted",
					StatusDetails: []stripe.CapabilityV2StatusDetail{
						{Code: "requirements_past_due", Resolution: "provide_info"},
					},
				},
			}},
			Recipient: &stripe.AccountV2Recipient{Capabilities: stripe.AccountV2RecipientCapabilities{
				StripeBalance: stripe.AccountV2RecipientBalance{
					StripeTransfers: stripe.CapabilityV2{
						Status:        "pending",
						StatusDetails: []stripe.CapabilityV2StatusDetail{},
					},
				},
			}},
		},
		Requirements: &stripe.AccountV2Requirements{
			Entries: []stripe.AccountV2Requirement{{
				Description: "identity.business_details.name", AwaitingActionFrom: "user",
				MinimumDeadline: stripe.AccountV2Deadline{Status: "currently_due"},
			}},
			Summary: stripe.AccountV2RequirementsSummary{
				MinimumDeadline: &stripe.AccountV2Deadline{Status: "currently_due", Time: &deadline},
			},
		},
	}
	if diff := cmp.Diff(want, acct); diff != "" {
		t.Errorf("CreateAccountV2 (-want +got):\n%s", diff)
	}

	acct, err = c.GetAccountV2(ctx, "acct_1", []string{"configuration.recipient", "requirements"})
	if err != nil || acct.Configuration.Recipient == nil ||
		acct.Configuration.Recipient.Capabilities.StripeBalance.StripeTransfers.Status != "active" {
		t.Errorf("GetAccountV2 = %+v, %v", acct, err)
	}

	got = f.last(t)
	if got.Method != http.MethodGet || got.Path != "/v2/core/accounts/acct_1" || got.Body != "" ||
		got.Idem != "" || got.Version != stripe.APIVersion ||
		got.Query != "include%5B0%5D=configuration.recipient&include%5B1%5D=requirements" {
		t.Errorf("GetAccountV2 request = %+v", got)
	}

	acct, err = c.UpdateAccountV2(ctx, "acct_1", stripe.Params{
		"display_name": "Shop 7", "metadata": map[string]any{"old": nil},
	})
	if err != nil || acct.DisplayName != "Shop 7" {
		t.Errorf("UpdateAccountV2 = %+v, %v", acct, err)
	}

	got = f.last(t)
	if got.Path != "/v2/core/accounts/acct_1" || got.Idem == "" ||
		!cmp.Equal(
			jsonBody(t, got.Body),
			map[string]any{"display_name": "Shop 7", "metadata": map[string]any{"old": nil}},
		) {
		t.Errorf("UpdateAccountV2 request = %+v", got)
	}

	link, err := c.CreateAccountLinkV2(ctx, stripe.Params{
		"account": "acct_1",
		"use_case": stripe.Params{
			"type": "account_onboarding",
			"account_onboarding": stripe.Params{
				"configurations": []string{"merchant", "recipient"},
				"refresh_url":    "https://app.test/connect/refresh",
				"return_url":     "https://app.test/connect/return",
			},
		},
	})

	wantLink := stripe.AccountLinkV2{
		Object: "v2.core.account_link", Account: "acct_1", URL: "https://accounts.stripe.com/r/acct_1#alu_test_1",
		Created:   time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC),
		ExpiresAt: time.Date(2026, 10, 1, 12, 10, 0, 0, time.UTC),
	}
	if diff := cmp.Diff(wantLink, link); err != nil || diff != "" {
		t.Errorf("CreateAccountLinkV2 = %v (-want +got):\n%s", err, diff)
	}

	got = f.last(t)
	if got.Path != "/v2/core/account_links" || got.ContentType != "application/json" || got.Idem == "" {
		t.Errorf("CreateAccountLinkV2 request = %+v", got)
	}
}

func TestAccountsV2Errors(t *testing.T) {
	t.Parallel()

	f := newV2API(t)

	_, err := v2Client(t, f).UpdateAccountV2(t.Context(), "acct_bad", nil)

	var apiErr *stripe.Error
	if !errors.As(err, &apiErr) || !errors.Is(err, stripe.ErrInvalid) || apiErr.Code != "accounts_v2_access_blocked" {
		t.Fatalf("UpdateAccountV2 = %v, want accounts_v2_access_blocked", err)
	}

	if got := f.last(t).Body; got != "{}" {
		t.Errorf("v2 POST without params body = %q, want {}", got)
	}
}

func TestCallV2EncodesJSON(t *testing.T) {
	t.Parallel()

	f := newV2API(t)
	c := v2Client(t, f)

	var out json.RawMessage
	if err := c.Call(t.Context(), http.MethodPost, "/v2/core/accounts", stripe.Params{"dashboard": "none"}, &out,
		stripe.IdempotencyKey("k1")); err != nil {
		t.Fatal(err)
	}

	got := f.last(t)
	if got.ContentType != "application/json" || got.Body != `{"dashboard":"none"}` || got.Idem != "k1" {
		t.Errorf("Call(v2 POST) request = %+v", got)
	}

	_ = c.Call(t.Context(), http.MethodDelete, "/v2/core/event_destinations/ed_1", nil, nil)
	if got := f.last(t); got.Idem == "" {
		t.Errorf("v2 DELETE without Idempotency-Key: %+v", got)
	}

	_ = c.Call(t.Context(), http.MethodPost, "/v1/refunds", stripe.Params{"charge": "ch_1"}, nil)
	if got := f.last(t); got.ContentType != "application/x-www-form-urlencoded" || got.Body != "charge=ch_1" {
		t.Errorf("Call(v1 POST) request = %+v", got)
	}
}

func TestRefunds(t *testing.T) {
	t.Parallel()

	f := newV2API(t)
	c := v2Client(t, f)
	ctx := t.Context()

	rf, err := c.CreateRefund(ctx, stripe.Params{
		"payment_intent": "pi_1", "amount": 500, "reason": "requested_by_customer",
		"metadata": map[string]string{"seller_order_id": "9"},
	}, stripe.IdempotencyKey("refund-9"))

	want := stripe.Refund{
		ID: "re_1", Amount: 500, Currency: "brl", Charge: "ch_1", PaymentIntent: "pi_1",
		Reason: "requested_by_customer", Status: "succeeded", Created: 1760000000,
		Metadata: map[string]string{"seller_order_id": "9"}, TransferReversal: "trr_9",
	}
	if diff := cmp.Diff(want, rf); err != nil || diff != "" {
		t.Errorf("CreateRefund = %v (-want +got):\n%s", err, diff)
	}

	got := f.last(t)
	if got.Path != "/v1/refunds" || got.Idem != "refund-9" || got.ContentType != "application/x-www-form-urlencoded" ||
		got.Body != "amount=500&metadata%5Bseller_order_id%5D=9&payment_intent=pi_1&reason=requested_by_customer" {
		t.Errorf("CreateRefund request = %+v", got)
	}

	rf, err = c.ForAccount("acct_1").GetRefund(ctx, "re_1")
	if err != nil || rf.Status != "failed" || rf.FailureReason != "expired_or_canceled_card" {
		t.Errorf("GetRefund = %+v, %v", rf, err)
	}

	if got := f.last(t); got.Method != http.MethodGet || got.Path != "/v1/refunds/re_1" || got.Account != "acct_1" {
		t.Errorf("GetRefund request = %+v (direct-charge refunds live on the connected account)", got)
	}
}

func TestTransferReversals(t *testing.T) {
	t.Parallel()

	f := newV2API(t)
	c := v2Client(t, f)
	ctx := t.Context()

	rv, err := c.CreateTransferReversal(ctx, "tr_1", stripe.Params{
		"amount": 400, "metadata": map[string]string{"seller_order_id": "9"},
	}, stripe.IdempotencyKey("reversal-9"))

	want := stripe.TransferReversal{
		ID: "trr_1", Amount: 400, Currency: "brl", Transfer: "tr_1", DestinationPaymentRefund: "pyr_1",
		Created: 1760000000, Metadata: map[string]string{"seller_order_id": "9"},
	}
	if diff := cmp.Diff(want, rv); err != nil || diff != "" {
		t.Errorf("CreateTransferReversal = %v (-want +got):\n%s", err, diff)
	}

	if got := f.last(t); got.Path != "/v1/transfers/tr_1/reversals" || got.Idem != "reversal-9" ||
		got.Body != "amount=400&metadata%5Bseller_order_id%5D=9" {
		t.Errorf("CreateTransferReversal request = %+v", got)
	}

	rv, err = c.GetTransferReversal(ctx, "tr_1", "trr_1")
	if err != nil || rv.ID != "trr_1" || rv.Transfer != "tr_1" {
		t.Errorf("GetTransferReversal = %+v, %v", rv, err)
	}

	if got := f.last(t); got.Method != http.MethodGet || got.Path != "/v1/transfers/tr_1/reversals/trr_1" {
		t.Errorf("GetTransferReversal request = %+v", got)
	}
}
