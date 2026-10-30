package cloudflare_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/AlexGalhardo/vukem/contrib/cloudflare"
)

func cached(t *testing.T, p cloudflare.CachePolicy, h http.HandlerFunc, header map[string]string) http.Header {
	t.Helper()

	mw, err := p.Middleware()
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody)
	for k, v := range header {
		req.Header.Set(k, v)
	}

	rec := httptest.NewRecorder()
	mw(h).ServeHTTP(rec, req)

	return rec.Header()
}

func TestCachePolicy(t *testing.T) {
	t.Parallel()

	ok := func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("hi")) }
	p := cloudflare.CachePolicy{
		Edge: time.Hour, Browser: time.Minute, StaleWhileRevalidate: 30 * time.Second, Tags: []string{"games", "home"},
	}

	got := cached(t, p, ok, nil)
	want := map[string]string{
		"Cache-Control":                "public, max-age=60",
		"Cloudflare-Cdn-Cache-Control": "max-age=3600, stale-while-revalidate=30",
		"Cache-Tag":                    "games,home",
	}

	for k, v := range want {
		if got.Get(k) != v {
			t.Errorf("%s = %q, want %q", k, got.Get(k), v)
		}
	}

	got = cached(t, cloudflare.CachePolicy{}, ok, nil)
	if got.Get("Cloudflare-CDN-Cache-Control") != "no-store" ||
		got.Get("Cache-Control") != "public, max-age=0, must-revalidate" {
		t.Errorf("zero policy headers = %v", got)
	}

	for _, bad := range []cloudflare.CachePolicy{
		{Edge: -time.Second},
		{Tags: []string{"a,b"}},
		{Tags: []string{"a b"}},
		{Tags: []string{""}},
		{Tags: []string{strings.Repeat("x", 1025)}},
	} {
		if _, err := bad.Middleware(); err == nil {
			t.Errorf("Middleware(%+v) = nil error", bad)
		}
	}
}

// A response that may belong to one user is never marked cacheable at the
// edge, which would serve it to every visitor.
func TestSecurity_A01_CachePolicySkipsPersonalResponses(t *testing.T) {
	t.Parallel()

	p := cloudflare.CachePolicy{Edge: time.Hour, Tags: []string{"t"}}
	plain := func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("hi")) }

	tests := []struct {
		name   string
		h      http.HandlerFunc
		header map[string]string
	}{
		{"cookie request", plain, map[string]string{"Cookie": "session=abc"}},
		{"authorization request", plain, map[string]string{"Authorization": "Bearer x"}},
		{"sets a cookie", func(w http.ResponseWriter, _ *http.Request) {
			http.SetCookie(w, &http.Cookie{Name: "session", Value: "abc"})
			_, _ = w.Write([]byte("hi"))
		}, nil},
		{"own cache-control", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Cache-Control", "private, no-store")
			_, _ = w.Write([]byte("hi"))
		}, nil},
		{
			"error status",
			func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusInternalServerError) },
			nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := cached(t, p, tt.h, tt.header)
			if v := got.Get("Cloudflare-CDN-Cache-Control"); v != "" || got.Get("Cache-Tag") != "" {
				t.Errorf("edge headers set: %v", got)
			}
		})
	}
}

func TestPurger(t *testing.T) {
	t.Parallel()

	const zone = "023e105f4ecef8ad9ca31a8372d0c353"

	var (
		mu     sync.Mutex
		bodies []map[string]any
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/zones/"+zone+"/purge_cache" || r.Header.Get("Authorization") != "Bearer tok" {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"success":false,"errors":[{"code":10000,"message":"Authentication error"}]}`))

			return
		}

		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)

		mu.Lock()
		bodies = append(bodies, body)
		mu.Unlock()

		if _, ok := body["hosts"]; ok {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"success":false,"errors":[{"code":1134,"message":"rate limit reached"}]}`))

			return
		}

		_, _ = w.Write([]byte(`{"success":true,"errors":[],"result":{"id":"x"}}`))
	}))
	t.Cleanup(srv.Close)

	p, err := cloudflare.NewPurger(zone, "tok")
	if err != nil {
		t.Fatal(err)
	}

	p.BaseURL, p.Client = srv.URL, srv.Client()

	urls := make([]string, 31)
	for i := range urls {
		urls[i] = fmt.Sprintf("https://example.com/%d", i)
	}

	if err := p.URLs(t.Context(), urls...); err != nil {
		t.Fatal(err)
	}

	if err := p.Tags(t.Context(), "games"); err != nil {
		t.Fatal(err)
	}

	if err := p.Everything(t.Context()); err != nil {
		t.Fatal(err)
	}

	if err := p.Prefixes(t.Context()); err != nil { // nothing to purge: no call
		t.Fatal(err)
	}

	err = p.Hosts(t.Context(), "example.com")
	if err == nil || !strings.Contains(err.Error(), "1134 rate limit reached") || strings.Contains(err.Error(), "tok") {
		t.Errorf("Hosts() = %v, want the API error without the token", err)
	}

	got := make([]string, len(bodies))
	for i, b := range bodies {
		for k, v := range b {
			if list, ok := v.([]any); ok {
				got[i] = fmt.Sprintf("%s:%d", k, len(list))
			} else {
				got[i] = k
			}
		}
	}

	if diff := cmp.Diff([]string{"files:30", "files:1", "tags:1", "purge_everything", "hosts:1"}, got); diff != "" {
		t.Errorf("purge calls (-want +got):\n%s", diff)
	}

	bad, _ := cloudflare.NewPurger(zone, "wrong")
	bad.BaseURL, bad.Client = srv.URL, srv.Client()

	if err := bad.Everything(t.Context()); err == nil || strings.Contains(err.Error(), "wrong") {
		t.Errorf("Everything(wrong token) = %v, want an error without the token", err)
	}

	for _, z := range []string{"", "../../accounts", strings.ToUpper(zone)} {
		if _, err := cloudflare.NewPurger(z, "tok"); err == nil {
			t.Errorf("NewPurger(%q) = nil error", z)
		}
	}

	if _, err := cloudflare.NewPurger(zone, ""); err == nil {
		t.Error("NewPurger(empty token) = nil error")
	}
}
