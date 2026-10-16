package web

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/AlexGalhardo/golang-framework/errs"
)

func TestETagOf(t *testing.T) {
	t.Parallel()

	a, _ := ETagOf(Map{"id": 1})
	b, _ := ETagOf(Map{"id": 1})
	c, _ := ETagOf("x")
	d, _ := ETagOf([]byte("x"))

	if a != b || a == c || c != d || a[0] != '"' {
		t.Fatalf("ETagOf() = %s %s %s %s", a, b, c, d)
	}

	if _, err := ETagOf(func() {}); err == nil {
		t.Fatal("ETagOf(func) error = nil")
	}
}

func TestNotModified(t *testing.T) {
	t.Parallel()

	modified := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

	tests := []struct {
		name, method string
		headers      map[string]string
		etag         string
		lastMod      time.Time
		want         bool
	}{
		{name: "no condition", method: "GET", etag: `"a"`},
		{
			name:    "etag match",
			method:  "GET",
			etag:    `"a"`,
			headers: map[string]string{"If-None-Match": `"b", "a"`},
			want:    true,
		},
		{
			name:    "weak etag match",
			method:  "GET",
			etag:    `"a"`,
			headers: map[string]string{"If-None-Match": `W/"a"`},
			want:    true,
		},
		{name: "star", method: "GET", etag: `"a"`, headers: map[string]string{"If-None-Match": "*"}, want: true},
		{name: "etag mismatch", method: "GET", etag: `"a"`, headers: map[string]string{"If-None-Match": `"b"`}},
		{
			name:    "etag wins over date",
			method:  "GET",
			etag:    `"a"`,
			lastMod: modified,
			headers: map[string]string{"If-None-Match": `"b"`, "If-Modified-Since": modified.Format(http.TimeFormat)},
		},
		{
			name:    "not modified since",
			method:  "GET",
			lastMod: modified,
			headers: map[string]string{"If-Modified-Since": modified.Format(http.TimeFormat)},
			want:    true,
		},
		{
			name:    "modified since",
			method:  "GET",
			lastMod: modified.Add(time.Hour),
			headers: map[string]string{"If-Modified-Since": modified.Format(http.TimeFormat)},
		},
		{name: "post ignored", method: "POST", etag: `"a"`, headers: map[string]string{"If-None-Match": `"a"`}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := httptest.NewRequestWithContext(t.Context(), tt.method, "/", http.NoBody)
			for k, v := range tt.headers {
				r.Header.Set(k, v)
			}

			c, rec := newCtx(t, r, nil)
			if got := c.NotModified(tt.etag, tt.lastMod); got != tt.want {
				t.Fatalf("NotModified() = %v, want %v", got, tt.want)
			}
			if tt.want && rec.Code != http.StatusNotModified {
				t.Fatalf("status = %d", rec.Code)
			}
			if tt.etag != "" && rec.Header().Get("ETag") != tt.etag {
				t.Fatalf("ETag header = %q", rec.Header().Get("ETag"))
			}
		})
	}
}

func TestCheckIfMatch(t *testing.T) {
	t.Parallel()

	for header, wantErr := range map[string]bool{"": false, "*": false, `"v1"`: false, `"v0", "v1"`: false, `"v0"`: true, `W/"v1"`: true} {
		r := httptest.NewRequestWithContext(t.Context(), http.MethodPut, "/", http.NoBody)
		if header != "" {
			r.Header.Set("If-Match", header)
		}

		c, _ := newCtx(t, r, nil)
		err := c.CheckIfMatch(`"v1"`)
		if wantErr != errors.Is(err, errs.ErrPreconditionFailed) {
			t.Errorf("If-Match %q: err = %v, wantErr %v", header, err, wantErr)
		}
	}
}
