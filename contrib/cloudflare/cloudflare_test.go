package cloudflare_test

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"

	"github.com/AlexGalhardo/vukem/contrib/cloudflare"
)

func TestRanges(t *testing.T) {
	t.Parallel()

	got := cloudflare.Ranges()

	var v4, v6 int

	for _, p := range got {
		if p.Bits() == 0 || p != p.Masked() {
			t.Errorf("Ranges() has %s", p)
		}

		if p.Addr().Is4() {
			v4++
		} else {
			v6++
		}
	}

	if v4 == 0 || v6 == 0 {
		t.Fatalf("Ranges() = %d IPv4, %d IPv6 prefixes, want both", v4, v6)
	}

	for _, want := range []string{"173.245.48.0/20", "2400:cb00::/32"} {
		found := false

		for _, p := range got {
			found = found || p.String() == want
		}

		if !found {
			t.Errorf("Ranges() lacks %s", want)
		}
	}
}

func TestFetchRanges(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v4":
			_, _ = w.Write([]byte("173.245.48.0/20\n\n103.21.244.0/22\n"))
		case "/v6":
			_, _ = w.Write([]byte("2400:cb00::/32"))
		case "/bad":
			_, _ = w.Write([]byte("<html>not a list</html>"))
		case "/all":
			_, _ = w.Write([]byte("0.0.0.0/0\n"))
		case "/empty":
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	got, err := cloudflare.FetchRanges(t.Context(), srv.Client(), srv.URL+"/v4", srv.URL+"/v6")
	if err != nil || len(got) != 3 {
		t.Fatalf("FetchRanges() = %v, %v; want 3 prefixes", got, err)
	}

	for _, path := range []string{"/bad", "/all", "/empty", "/missing"} {
		if got, err := cloudflare.FetchRanges(t.Context(), srv.Client(), srv.URL+path); err == nil {
			t.Errorf("FetchRanges(%s) = %v, want an error", path, got)
		}
	}
}

// Only a Cloudflare peer (or an allowed network) reaches the app; a
// header claiming Cloudflare does not help a direct caller.
func TestSecurity_A05_OriginLockRefusesDirectRequests(t *testing.T) {
	t.Parallel()

	h := cloudflare.OriginLock(netip.MustParsePrefix("127.0.0.0/8"))(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) }))

	for peer, want := range map[string]int{
		"173.245.48.10:443":        http.StatusOK,
		"[2400:cb00::7]:443":       http.StatusOK,
		"[::ffff:173.245.48.10]:1": http.StatusOK,
		"127.0.0.1:5000":           http.StatusOK,
		"198.51.100.7:443":         http.StatusForbidden,
		"[2001:db8::1]:443":        http.StatusForbidden,
		"garbage":                  http.StatusForbidden,
	} {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody)
		req.RemoteAddr = peer
		req.Header.Set(cloudflare.ClientIPHeader, "173.245.48.10")
		req.Header.Set("CF-Ray", "8f00000000000000-GRU")

		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		if rec.Code != want {
			t.Errorf("OriginLock() from %s = %d, want %d", peer, rec.Code, want)
		}

		if want == http.StatusForbidden && strings.Contains(rec.Body.String(), "ok") {
			t.Errorf("OriginLock() from %s ran the handler", peer)
		}
	}
}

func TestFromCloudflare(t *testing.T) {
	t.Parallel()

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody)

	req.RemoteAddr = "104.16.0.1:443"
	if !cloudflare.FromCloudflare(req) {
		t.Error("FromCloudflare(104.16.0.1) = false, want true")
	}

	req.RemoteAddr = "8.8.8.8:443"
	if cloudflare.FromCloudflare(req) {
		t.Error("FromCloudflare(8.8.8.8) = true, want false")
	}
}
