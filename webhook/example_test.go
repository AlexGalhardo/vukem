package webhook_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/AlexGalhardo/vukem/webhook"
)

func Example() {
	secret := []byte("whsec_example")
	handler := webhook.Middleware("X-Hub-Signature-256", secret, 1<<20)(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, _ := io.ReadAll(r.Body)
			fmt.Fprintf(w, "accepted %s", body)
		}),
	)

	payload := `{"action":"opened"}`
	req := httptest.NewRequest(http.MethodPost, "/webhooks/github", strings.NewReader(payload))
	req.Header.Set("X-Hub-Signature-256", webhook.Sign(secret, []byte(payload)))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	fmt.Println(rec.Code, rec.Body.String())

	forged := httptest.NewRequest(http.MethodPost, "/webhooks/github", strings.NewReader(payload))
	forged.Header.Set("X-Hub-Signature-256", "sha256=00")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, forged)
	fmt.Println(rec.Code)
	// Output:
	// 200 accepted {"action":"opened"}
	// 401
}

func ExampleVerifyHMAC() {
	secret := []byte("whsec_example")
	sig := webhook.Sign(secret, []byte("hello"))
	fmt.Println(webhook.VerifyHMAC(secret, []byte("hello"), sig))
	fmt.Println(webhook.VerifyHMAC(secret, []byte("hello!"), sig))
	// Output:
	// true
	// false
}

// An app with its own deliveries table maps Attempt onto its columns
// instead of using SQLDeliveryLog, for example:
//
//	_, err := db.ExecContext(ctx,
//		`INSERT INTO seller_webhook_deliveries (seller_id, message_id, http_status, ok, failure, created_at)
//		 VALUES (?, ?, ?, ?, ?, ?)`,
//		a.Endpoint, a.EventID, a.Status, a.Error == "", a.Error, a.At)
func ExampleDeliveryLogFunc() {
	log := webhook.DeliveryLogFunc(func(_ context.Context, a webhook.Attempt) error {
		fmt.Printf("seller=%s message=%s status=%d ok=%t\n", a.Endpoint, a.EventID, a.Status, a.Error == "")
		return nil
	})

	sender, err := webhook.NewSender(webhook.SenderOptions{
		Endpoint: func(context.Context, string) (webhook.Endpoint, error) {
			return webhook.Endpoint{}, webhook.ErrEndpointDisabled
		},
		Log: log,
	})
	if err != nil {
		fmt.Println(err)
		return
	}

	_ = sender
	_ = log.Record(context.Background(), webhook.Attempt{Endpoint: "seller-1", EventID: "msg_1", Status: 200})
	// Output: seller=seller-1 message=msg_1 status=200 ok=true
}
