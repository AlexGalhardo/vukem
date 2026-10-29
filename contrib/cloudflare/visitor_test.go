package cloudflare_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/AlexGalhardo/vukem/contrib/cloudflare"
)

func visitorOf(t *testing.T, peer string, header map[string]string) (cloudflare.Visitor, bool) {
	t.Helper()

	var (
		got cloudflare.Visitor
		ok  bool
	)

	h := cloudflare.Headers()(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got, ok = cloudflare.VisitorFrom(r.Context())
	}))

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody)
	req.RemoteAddr = peer

	for k, v := range header {
		req.Header.Set(k, v)
	}

	h.ServeHTTP(httptest.NewRecorder(), req)

	return got, ok
}

func TestVisitorFromCloudflare(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		header map[string]string
		want   cloudflare.Visitor
	}{
		{
			name: "all headers",
			header: map[string]string{
				"CF-IPCountry": "BR", "CF-Ray": "230b030023ae2822-GRU", "cf-bot-score": "87", "cf-verified-bot": "true",
			},
			want: cloudflare.Visitor{Country: "BR", Ray: "230b030023ae2822-GRU", BotScore: 87, VerifiedBot: true},
		},
		{name: "tor", header: map[string]string{"CF-IPCountry": "T1"}, want: cloudflare.Visitor{Country: "T1"}},
		{name: "none", want: cloudflare.Visitor{}},
		{
			name: "malformed values dropped",
			header: map[string]string{
				"CF-IPCountry": "<b>", "CF-Ray": "a b", "cf-bot-score": "100", "cf-verified-bot": "yes",
			},
			want: cloudflare.Visitor{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, ok := visitorOf(t, "172.64.1.1:443", tt.header)
			if !ok {
				t.Fatal("VisitorFrom() ok = false, want true")
			}

			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("VisitorFrom() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// A direct caller cannot claim to be a verified bot, or a country.
func TestSecurity_A01_VisitorHeadersOnlyFromCloudflare(t *testing.T) {
	t.Parallel()

	got, ok := visitorOf(t, "198.51.100.7:443", map[string]string{
		"CF-IPCountry": "BR", "cf-bot-score": "99", "cf-verified-bot": "true",
	})
	if ok || got != (cloudflare.Visitor{}) {
		t.Fatalf("VisitorFrom() = %+v, %v; want zero, false", got, ok)
	}
}
