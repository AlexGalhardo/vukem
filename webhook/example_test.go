package webhook_test

import (
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
