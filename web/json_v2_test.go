package web

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/AlexGalhardo/vukem/errs"
)

type jsonUser struct {
	Name string `json:"name"`
	N    int    `json:"n"`
}

func TestHTTPJSONV2RoundTripAndDecodeRules(t *testing.T) {
	t.Parallel()

	t.Run("response and problem round trip", func(t *testing.T) {
		t.Parallel()

		c, rec := newCtx(t, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/items", http.NoBody), nil)
		if err := c.JSON(http.StatusOK, jsonUser{Name: "Ada", N: 2}); err != nil {
			t.Fatal(err)
		}

		if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "application/json" {
			t.Fatalf("JSON() = %d %q", rec.Code, rec.Header().Get("Content-Type"))
		}

		var got jsonUser
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}

		if diff := cmp.Diff(jsonUser{Name: "Ada", N: 2}, got); diff != "" {
			t.Fatalf("JSON() mismatch (-want +got):\n%s", diff)
		}

		c, rec = newCtx(t, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/items", http.NoBody), nil)
		if err := c.Problem(http.StatusBadRequest, "Bad request", "see docs"); err != nil {
			t.Fatal(err)
		}

		if rec.Header().Get("Content-Type") != "application/problem+json" {
			t.Fatalf("Problem() content type = %q", rec.Header().Get("Content-Type"))
		}

		var problem Problem
		if err := json.Unmarshal(rec.Body.Bytes(), &problem); err != nil {
			t.Fatal(err)
		}

		if problem.Status != http.StatusBadRequest || problem.Title != "Bad request" || problem.Detail != "see docs" {
			t.Fatalf("Problem() = %+v", problem)
		}
	})

	t.Run("generic map keeps the number text", func(t *testing.T) {
		t.Parallel()

		body := `{"name":"Ada","n":12345678901234567890}`
		r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		c, _ := newCtx(t, r, nil)

		got, err := c.Input()
		if err != nil {
			t.Fatal(err)
		}

		want := map[string]any{"name": "Ada", "n": json.Number("12345678901234567890")}
		if diff := cmp.Diff(want, got); diff != "" {
			t.Fatalf("Input() mismatch (-want +got):\n%s", diff)
		}
	})

	tests := []struct {
		name       string
		body       []byte
		wantStatus int
		wantMsg    string
	}{
		{name: "object", body: []byte(`{"name":"Ada","n":2}`)},
		{name: "duplicate name", body: []byte(`{"name":"Ada","name":"Grace"}`), wantStatus: http.StatusBadRequest},
		{name: "invalid utf-8", body: []byte("{\"name\":\"caf\xff\"}"), wantStatus: http.StatusBadRequest},
		{
			name:       "unknown field",
			body:       []byte(`{"name":"Ada","role":"admin"}`),
			wantStatus: http.StatusUnprocessableEntity,
			wantMsg:    "not allowed",
		},
		{
			name:       "trailing value",
			body:       []byte(`{"name":"Ada"}{"name":"Grace"}`),
			wantStatus: http.StatusBadRequest,
			wantMsg:    "single JSON value",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/", bytes.NewReader(tt.body))
			r.Header.Set("Content-Type", "application/json")
			c, _ := newCtx(t, r, nil)

			var got jsonUser
			err := c.BindJSON(&got)

			if tt.wantStatus == 0 {
				if err != nil {
					t.Fatalf("BindJSON() = %v, want nil", err)
				}

				if diff := cmp.Diff(jsonUser{Name: "Ada", N: 2}, got); diff != "" {
					t.Fatalf("BindJSON() mismatch (-want +got):\n%s", diff)
				}

				return
			}

			if errs.Status(err) != tt.wantStatus {
				t.Fatalf("BindJSON() status = %d (%v), want %d", errs.Status(err), err, tt.wantStatus)
			}

			if tt.wantMsg != "" && !strings.Contains(err.Error(), tt.wantMsg) {
				t.Fatalf("BindJSON() = %v, want %q", err, tt.wantMsg)
			}
		})
	}
}

func TestSecurity_A05_JSONDecodeRejectsDuplicateNamesAndInvalidUTF8(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		body   []byte
		reject bool
	}{
		{name: "object", body: []byte(`{"name":"Ada"}`)},
		{name: "duplicate name", body: []byte(`{"name":"Ada","name":"Grace"}`), reject: true},
		{name: "invalid utf-8", body: []byte("{\"name\":\"\xff\"}"), reject: true},
	}

	for _, tt := range cases {
		t.Run("BindJSON/"+tt.name, func(t *testing.T) {
			t.Parallel()

			r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/", bytes.NewReader(tt.body))
			r.Header.Set("Content-Type", "application/json")
			c, _ := newCtx(t, r, nil)

			var got jsonUser
			err := c.BindJSON(&got)
			assertJSONDecodeGate(t, "BindJSON", err, tt.reject, got.Name)
		})

		t.Run("Input/"+tt.name, func(t *testing.T) {
			t.Parallel()

			r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/", bytes.NewReader(tt.body))
			r.Header.Set("Content-Type", "application/json")
			c, _ := newCtx(t, r, nil)

			got, err := c.Input()
			name, _ := got["name"].(string)
			assertJSONDecodeGate(t, "Input", err, tt.reject, name)
		})

		t.Run("PeekJSON/"+tt.name, func(t *testing.T) {
			t.Parallel()

			r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/", bytes.NewReader(tt.body))
			r.Header.Set("Content-Type", "application/json")

			got, err := PeekJSON(r)
			name, _ := got["name"].(string)
			assertJSONDecodeGate(t, "PeekJSON", err, tt.reject, name)
		})
	}
}

func assertJSONDecodeGate(t *testing.T, fn string, err error, reject bool, name string) {
	t.Helper()

	if reject {
		if err == nil {
			t.Fatalf("%s() accepted a body the decoder must reject", fn)
		}

		return
	}

	if err != nil || name != "Ada" {
		t.Fatalf("%s() = %q, %v, want name Ada", fn, name, err)
	}
}

func TestPeekJSON_SurvivesLaterLimitBody(t *testing.T) {
	t.Parallel()

	const payload = `{"email":"a@b.c","password":"x"}`

	tests := []struct {
		name    string
		second  int64
		wantErr bool
	}{
		{"larger limit keeps body", 2 << 20, false},
		{"smaller limit still enforced", 5, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(payload))
			r.Header.Set("Content-Type", "application/json")

			r = LimitBody(w, r, 1<<20)
			if m, err := PeekJSON(r); err != nil || m["email"] != "a@b.c" {
				t.Fatalf("PeekJSON() = %v, %v", m, err)
			}

			r = LimitBody(w, r, tt.second)
			got, err := io.ReadAll(r.Body)

			if tt.wantErr {
				if _, ok := errors.AsType[*http.MaxBytesError](err); !ok {
					t.Fatalf("ReadAll error = %v, want *http.MaxBytesError", err)
				}

				return
			}

			if err != nil || string(got) != payload {
				t.Fatalf("ReadAll = %q, %v, want %q", got, err, payload)
			}
		})
	}
}
