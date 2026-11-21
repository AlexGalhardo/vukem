package web_test

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/AlexGalhardo/vukem/web"
)

func TestBodyInput(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name, contentType, body string
		wantBody                map[string]any
		wantJSON                bool
	}{
		{"json without the query", "application/json", `{"a":"x"}`, map[string]any{"a": "x"}, true},
		{"form without the query", "application/x-www-form-urlencoded", "a=x", map[string]any{"a": "x"}, false},
		{"no body", "", "", map[string]any{}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := httptest.NewRequestWithContext(t.Context(), "POST", "/?q=1", strings.NewReader(tt.body))
			if tt.contentType != "" {
				r.Header.Set("Content-Type", tt.contentType)
			}

			c := web.NewCtx(httptest.NewRecorder(), r, nil)

			got, isJSON, err := c.BodyInput()
			if err != nil {
				t.Fatalf("BodyInput() error = %v", err)
			}

			if diff := cmp.Diff(tt.wantBody, got); diff != "" || isJSON != tt.wantJSON {
				t.Fatalf("BodyInput() = %v, %v, want %v, %v\n%s", got, isJSON, tt.wantBody, tt.wantJSON, diff)
			}
		})
	}
}
