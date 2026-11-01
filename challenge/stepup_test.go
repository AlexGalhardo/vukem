package challenge_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/AlexGalhardo/vukem/challenge"
	"github.com/AlexGalhardo/vukem/router"
	"github.com/AlexGalhardo/vukem/web"
)

const browser = "Mozilla/5.0 test"

type clock struct{ t atomic.Int64 }

func (c *clock) now() time.Time      { return time.Unix(c.t.Load(), 0) }
func (c *clock) add(d time.Duration) { c.t.Add(int64(d.Seconds())) }

// stepUpApp guards GET /games with s.Require and mounts s.Handle.
func stepUpApp(s *challenge.StepUp) http.Handler {
	r := router.New(&web.Services{})
	r.Get("/games", func(c *web.Ctx) error {
		if !s.Require(c) {
			return nil
		}

		return c.String(http.StatusOK, "games")
	})
	r.Post(s.Path, s.Handle)

	return r
}

func newStepUp(t *testing.T) (*challenge.StepUp, *clock) {
	t.Helper()

	var calls atomic.Int32

	clk := &clock{}
	clk.t.Store(1_800_000_000)

	s := challenge.NewStepUp(enabled(), []byte("0123456789abcdef0123456789abcdef"))
	s.Verifier = pointAt(s.Turnstile.Verifier(s.Action), fakeSiteverify(t, &calls))
	s.Now = clk.now

	return s, clk
}

func get(h http.Handler, ip, ua string, cookie *http.Cookie) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/games?page=2", http.NoBody)
	req.RemoteAddr, req.Header["User-Agent"] = ip+":1234", []string{ua}

	if cookie != nil {
		req.AddCookie(cookie)
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	return rec
}

func solve(h http.Handler, token, ret string) *httptest.ResponseRecorder {
	form := url.Values{"cf-turnstile-response": {token}, challenge.ReturnField: {ret}}
	req := httptest.NewRequest(http.MethodPost, "/challenge", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.RemoteAddr, req.Header["User-Agent"] = "192.0.2.1:1234", []string{browser}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	return rec
}

func clearance(t *testing.T, rec *httptest.ResponseRecorder) *http.Cookie {
	t.Helper()

	for _, c := range rec.Result().Cookies() {
		if c.Name == "vukem_clearance" {
			return c
		}
	}

	t.Fatalf("no clearance cookie; status %d", rec.Code)

	return nil
}

func TestStepUpInterstitial(t *testing.T) {
	t.Parallel()

	s, _ := newStepUp(t)

	rec := get(stepUpApp(s), "192.0.2.1", browser, nil)
	body := rec.Body.String()

	if rec.Code != http.StatusForbidden || rec.Header().Get("Cache-Control") != "no-store" ||
		!strings.Contains(rec.Header().Get("X-Robots-Tag"), "noindex") {
		t.Fatalf("GET /games = %d %v, want 403 no-store noindex", rec.Code, rec.Header())
	}

	for _, want := range []string{
		`<meta name="robots" content="noindex, nofollow">`,
		`action="/challenge"`,
		`name="return" value="/games?page=2"`,
		`data-sitekey="` + testSiteKey + `"`,
		`data-action="step-up"`,
		challenge.TurnstileScriptURL,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("interstitial misses %s:\n%s", want, body)
		}
	}
}

func TestStepUpClearance(t *testing.T) {
	t.Parallel()

	s, clk := newStepUp(t)
	h := stepUpApp(s)

	rec := solve(h, "step", "/games?page=2")
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/games?page=2" {
		t.Fatalf("POST /challenge = %d to %q, want 303 to /games?page=2", rec.Code, rec.Header().Get("Location"))
	}

	ck := clearance(t, rec)
	if !ck.HttpOnly || !ck.Secure || ck.SameSite != http.SameSiteLaxMode || ck.MaxAge != 1800 || ck.Path != "/" {
		t.Errorf("cookie = %+v, want HttpOnly Secure Lax MaxAge 1800 Path /", ck)
	}

	if got := get(h, "192.0.2.1", browser, ck).Code; got != http.StatusOK {
		t.Errorf("same client = %d, want 200", got)
	}

	if got := get(h, "192.0.2.77", browser, ck).Code; got != http.StatusOK {
		t.Errorf("same /24 = %d, want 200", got)
	}

	clk.add(31 * time.Minute)

	if got := get(h, "192.0.2.1", browser, ck).Code; got != http.StatusForbidden {
		t.Errorf("expired clearance = %d, want 403", got)
	}
}

func TestStepUpRejections(t *testing.T) {
	t.Parallel()

	s, _ := newStepUp(t)
	h := stepUpApp(s)

	if rec := solve(h, "bad", "/games"); rec.Code != http.StatusForbidden ||
		len(rec.Result().Cookies()) != 0 || !strings.Contains(rec.Body.String(), "cf-turnstile") {
		t.Errorf("bad token = %d, cookies %v; want the interstitial again, no cookie", rec.Code, rec.Result().Cookies())
	}

	if rec := solve(h, "good", "/games"); rec.Code != http.StatusForbidden {
		t.Errorf("token for another action = %d, want 403", rec.Code)
	}

	if rec := solve(h, "down", "/games"); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("outage = %d, want 503", rec.Code)
	}

	for _, ret := range []string{"//evil.example/", "https://evil.example/", "/\\evil.example"} {
		if loc := solve(h, "step", ret).Header().Get("Location"); loc != "/" {
			t.Errorf("return %q redirected to %q, want /", ret, loc)
		}
	}

	s.Skip = func(c *web.Ctx) bool { return c.Request().UserAgent() == "Googlebot" }
	if got := get(h, "192.0.2.1", "Googlebot", nil).Code; got != http.StatusOK {
		t.Errorf("skipped client = %d, want 200", got)
	}
}

func TestSecurity_A07_StepUpClearanceCannotBeForgedOrMoved(t *testing.T) {
	t.Parallel()

	s, _ := newStepUp(t)
	h := stepUpApp(s)
	ck := clearance(t, solve(h, "step", "/games"))

	other, _ := newStepUp(t)
	ipv6 := *ck
	forged := clearance(
		t,
		solve(stepUpApp(withKey(other, "another-key-another-key-another-k")), "step", "/"),
	)
	tampered := *ck
	tampered.Value = strings.Replace(ck.Value, ".", "x.", 1)

	tests := []struct {
		name   string
		ip, ua string
		cookie *http.Cookie
	}{
		{"other /24", "198.51.100.1", browser, ck},
		{"other user agent", "192.0.2.1", "curl/8", ck},
		{"ipv6 client", "2001:db8::1", browser, &ipv6},
		{"signed with another key", "192.0.2.1", browser, forged},
		{"tampered", "192.0.2.1", browser, &tampered},
		{"garbage", "192.0.2.1", browser, &http.Cookie{Name: "vukem_clearance", Value: "9999999999|x"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := get(h, tt.ip, tt.ua, tt.cookie).Code; got != http.StatusForbidden {
				t.Errorf("GET with %s = %d, want 403", tt.name, got)
			}
		})
	}
}

func withKey(s *challenge.StepUp, key string) *challenge.StepUp {
	n := challenge.NewStepUp(s.Turnstile, []byte(key))
	n.Verifier, n.Now = s.Verifier, s.Now

	return n
}
