package analytics_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/AlexGalhardo/vukem/analytics"
)

const websiteID = "94db1cb1-74f4-4a40-ad6c-962362670409"

func TestUmamiTag(t *testing.T) {
	t.Parallel()

	cfg := analytics.Config{
		UmamiWebsiteID: websiteID,
		UmamiDomains:   []string{"example.com", "www.example.com"},
		UmamiHostURL:   "https://stats.example.com",
		UmamiTag:       "beta",
	}

	got := string(cfg.Tags("n0nce"))
	want := `<script defer src="https://cloud.umami.is/script.js" data-website-id="` + websiteID +
		`" data-do-not-track="true" data-domains="example.com,www.example.com"` +
		` data-host-url="https://stats.example.com" data-tag="beta" nonce="n0nce"></script>`

	if got != want {
		t.Errorf("Tags()\n got %s\nwant %s", got, want)
	}

	cfg.UmamiIgnoreDoNotTrack = true
	if strings.Contains(string(cfg.Tags("")), "data-do-not-track") {
		t.Error("UmamiIgnoreDoNotTrack still renders data-do-not-track")
	}

	if strings.Contains(string(cfg.Tags("")), "nonce=") {
		t.Error("empty nonce renders a nonce attribute")
	}
}

func TestUmamiCSPSources(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		cfg  analytics.Config
		want map[string][]string
	}{
		{
			name: "cloud",
			cfg:  analytics.Config{UmamiWebsiteID: websiteID},
			want: map[string][]string{
				"script-src":  {"https://cloud.umami.is"},
				"connect-src": {"https://gateway.umami.is"},
			},
		},
		{
			name: "self-hosted with host url",
			cfg: analytics.Config{
				UmamiWebsiteID: websiteID,
				UmamiScriptURL: "https://umami.example.com/u.js",
				UmamiHostURL:   "https://collect.example.com/",
			},
			want: map[string][]string{
				"script-src":  {"https://umami.example.com"},
				"connect-src": {"https://collect.example.com"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if diff := cmp.Diff(tt.want, tt.cfg.CSPSources()); diff != "" {
				t.Errorf("CSPSources() (-want +got):\n%s", diff)
			}
		})
	}
}

func TestUmamiValidate(t *testing.T) {
	t.Parallel()

	checkRefused(t, map[string]analytics.Config{
		"id":                 {UmamiWebsiteID: `x" onload="alert(1)`},
		"script http":        {UmamiWebsiteID: websiteID, UmamiScriptURL: "http://evil.example/s.js"},
		"script javascript":  {UmamiWebsiteID: websiteID, UmamiScriptURL: "javascript:alert(1)"},
		"host userinfo":      {UmamiWebsiteID: websiteID, UmamiHostURL: "https://u:p@x.example"},
		"domain":             {UmamiWebsiteID: websiteID, UmamiDomains: []string{"a.com\"><b"}},
		"tag":                {UmamiWebsiteID: websiteID, UmamiTag: "a b"},
		"domains without id": {UmamiDomains: []string{"example.com"}},
	})

	loopback := analytics.Config{UmamiWebsiteID: websiteID, UmamiScriptURL: "http://localhost:3001/script.js"}
	if err := loopback.Validate(); err != nil {
		t.Errorf("Validate(loopback http) = %v, want nil", err)
	}
}

func TestSendUmamiEvent(t *testing.T) {
	t.Parallel()

	var (
		body map[string]any
		ua   string
	)

	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/umami/api/send" {
			t.Errorf("got %s %s, want POST /umami/api/send", r.Method, r.URL.Path)
		}

		ua = r.Header.Get("User-Agent")
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &body)
	}))
	defer srv.Close()

	// Without UmamiHostURL, events go next to the script, like the tracker.
	cfg := analytics.Config{UmamiWebsiteID: websiteID, UmamiScriptURL: srv.URL + "/umami/script.js"}

	err := cfg.SendUmamiEvent(t.Context(), srv.Client(), "Mozilla/5.0 test", analytics.UmamiEvent{
		Name: "signup", URL: "/register", Hostname: "example.com", Data: map[string]any{"plan": "pro"},
	})
	if err != nil {
		t.Fatal(err)
	}

	want := map[string]any{
		"type": "event",
		"payload": map[string]any{
			"website": websiteID, "name": "signup", "url": "/register", "hostname": "example.com",
			"data": map[string]any{"plan": "pro"},
		},
	}
	if diff := cmp.Diff(want, body); diff != "" {
		t.Errorf("body (-want +got):\n%s", diff)
	}

	if ua != "Mozilla/5.0 test" {
		t.Errorf("User-Agent = %q, want the caller's", ua)
	}
}

func TestSendUmamiEventOffAndErrors(t *testing.T) {
	t.Parallel()

	if err := (analytics.Config{}).SendUmamiEvent(t.Context(), nil, "ua", analytics.UmamiEvent{Name: "x"}); err != nil {
		t.Errorf("SendUmamiEvent(disabled) = %v, want nil (no-op)", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer srv.Close()

	cfg := analytics.Config{UmamiWebsiteID: websiteID, UmamiHostURL: srv.URL}
	if err := cfg.SendUmamiEvent(t.Context(), srv.Client(), "ua", analytics.UmamiEvent{Name: "x"}); err == nil {
		t.Error("SendUmamiEvent(400) = nil, want an error")
	}

	if err := cfg.SendUmamiEvent(t.Context(), srv.Client(), "ua", analytics.UmamiEvent{}); err == nil {
		t.Error("SendUmamiEvent(empty name) = nil, want an error")
	}
}
