package openapi_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/AlexGalhardo/vukem/openapi"
)

func TestValidateResponse(t *testing.T) {
	t.Parallel()

	doc := openapi.Generate(routes(), openapi.Info{})
	product := `{"id":1,"name":"Pen","price":2.5,"created_at":"2026-01-02T03:04:05Z","updated_at":"2026-01-02T03:04:05Z",` +
		`"published_at":null,"deleted_at":null,"Author":{"id":2,"name":"Ana","created_at":"2026-01-02T03:04:05Z",` +
		`"updated_at":"2026-01-02T03:04:05Z"},"related":null,"tags":["a"],"meta":{"k":"v"}}`

	tests := []struct {
		name, method, path string
		status             int
		body, wantErr      string
	}{
		{"valid list", "GET", "/api/v1/products", 200, `{"data":[` + product + `]}`, ""},
		{"head is get", "HEAD", "/api/v1/products", 200, `{"data":[]}`, ""},
		{"valid create", "POST", "/api/v1/shops/3/products", 201, product, ""},
		{"wrong type", "GET", "/api/v1/products", 200, `{"data":[{"id":"one"}]}`, "$.data[0].id: got string"},
		{"not an array", "GET", "/api/v1/products", 200, `{"data":{}}`, "$.data: got object"},
		{"bad date", "POST", "/api/v1/shops/3/products", 201, `{"created_at":"yesterday"}`, "not a date-time"},
		{"nested map", "POST", "/api/v1/shops/3/products", 201, `{"meta":{"k":1}}`, "$.meta.k: got integer"},
		{"problem default", "GET", "/api/v1/products", 500, `{"type":"about:blank","title":"x","status":500}`, ""},
		{"problem wrong", "GET", "/api/v1/products", 422, `{"status":"bad"}`, "$.status"},
		{"no content", "DELETE", "/api/v1/products/9", 204, "", ""},
		{"not json", "GET", "/api/v1/products", 200, `nope`, "not JSON"},
		{"unknown route", "GET", "/nowhere", 200, `{}`, "no operation"},
		{"generic route", "GET", "/api/v1/legacy/a", 200, ``, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := doc.ValidateResponse(tt.method, tt.path, tt.status, []byte(tt.body))

			switch {
			case tt.wantErr == "" && err != nil:
				t.Fatalf("ValidateResponse = %v, want nil", err)
			case tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)):
				t.Fatalf("ValidateResponse = %v, want %q", err, tt.wantErr)
			}
		})
	}

	if err := doc.ValidateResponse("GET", "/x", 200, nil); !errors.Is(err, openapi.ErrNoOperation) {
		t.Errorf("unknown = %v", err)
	}
}
