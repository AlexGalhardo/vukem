package challenge_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/AlexGalhardo/vukem/challenge"
	"github.com/AlexGalhardo/vukem/config"
	"github.com/AlexGalhardo/vukem/middleware"
	"github.com/AlexGalhardo/vukem/router"
	"github.com/AlexGalhardo/vukem/web"
)

// Cloudflare's published test keys (always pass); tests never call Cloudflare.
const (
	testSiteKey   = "1x00000000000000000000AA"
	testSecretKey = "1x0000000000000000000000000000000AA"
)

func enabled() challenge.TurnstileConfig {
	return challenge.TurnstileConfig{
		SiteKey: testSiteKey, SecretKey: config.NewSecret(testSecretKey),
		Hostname: "example.com", Timeout: time.Second,
	}
}

// fakeSiteverify answers like Cloudflare: "good" passes for action "login"
// on example.com, "flaky" fails once with 500 then passes (same
// idempotency key required), "down" always answers 500.
func fakeSiteverify(t *testing.T, calls *atomic.Int32) *httptest.Server {
	t.Helper()

	var mu sync.Mutex

	keys := map[string]string{}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)

		if r.PostFormValue("secret") != testSecretKey {
			_ = json.NewEncoder(w).
				Encode(map[string]any{"success": false, "error-codes": []string{"invalid-input-secret"}})
			return
		}

		tok, key := r.PostFormValue("response"), r.PostFormValue("idempotency_key")
		if key == "" {
			t.Error("siteverify called without idempotency_key")
		}

		switch tok {
		case "good", "step":
			_ = json.NewEncoder(w).
				Encode(map[string]any{"success": true, "hostname": "example.com", "action": map[string]string{"good": "login", "step": "step-up"}[tok]})
		case "flaky":
			mu.Lock()
			prev, seen := keys[tok]
			keys[tok] = key
			mu.Unlock()

			if !seen {
				w.WriteHeader(http.StatusBadGateway)
				return
			}

			if prev != key {
				t.Errorf("retry used idempotency_key %q, first call %q", key, prev)
			}

			_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "hostname": "example.com", "action": "login"})
		case "other-host":
			_ = json.NewEncoder(w).
				Encode(map[string]any{"success": true, "hostname": "evil.example", "action": "login"})
		case "down":
			w.WriteHeader(http.StatusInternalServerError)
		case "internal":
			_ = json.NewEncoder(w).Encode(map[string]any{"success": false, "error-codes": []string{"internal-error"}})
		default:
			_ = json.NewEncoder(w).
				Encode(map[string]any{"success": false, "error-codes": []string{"invalid-input-response"}})
		}
	}))
	t.Cleanup(srv.Close)

	return srv
}

func pointAt(v *challenge.SiteVerify, srv *httptest.Server) *challenge.SiteVerify {
	v.URL, v.Client = srv.URL, srv.Client()
	return v
}

func TestTurnstileOffWhenUnset(t *testing.T) {
	t.Parallel()

	cfg, err := config.Load[challenge.TurnstileConfig](config.MapSource(nil))
	if err != nil {
		t.Fatal(err)
	}

	if cfg.Enabled() || cfg.Validate() != nil || cfg.Timeout != 5*time.Second {
		t.Fatalf("unset config = %+v, enabled %v, validate %v", cfg, cfg.Enabled(), cfg.Validate())
	}

	if cfg.Script("n") != "" || cfg.Widget("login") != "" || cfg.CSPSources() != nil || cfg.Verifier("x") != nil {
		t.Error("helpers render or allow something while Turnstile is off")
	}

	h := cfg.Protect(
		"login",
		nil,
	)(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) }),
	)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/login", http.NoBody))

	if rec.Code != http.StatusTeapot {
		t.Errorf("Protect off: POST = %d, want pass-through", rec.Code)
	}

	if !challenge.NewStepUp(cfg, make([]byte, 32)).
		Require(web.NewCtx(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", http.NoBody), &web.Services{})) {
		t.Error("StepUp off: Require = false")
	}

	half := challenge.TurnstileConfig{SiteKey: testSiteKey}
	if half.Validate() == nil || half.Enabled() {
		t.Error("a site key without a secret must fail Validate and stay off")
	}

	loaded, err := config.Load[challenge.TurnstileConfig](config.MapSource(map[string]string{
		"TURNSTILE_SITE_KEY": testSiteKey, "TURNSTILE_SECRET_KEY": testSecretKey,
		"TURNSTILE_HOSTNAME": "example.com", "TURNSTILE_TIMEOUT": "2s", "TURNSTILE_INSECURE_FAIL_OPEN": "true",
	}))
	if err != nil || !loaded.Enabled() || loaded.Timeout != 2*time.Second || !loaded.InsecureFailOpen ||
		loaded.Hostname != "example.com" {
		t.Errorf("Load = %+v, %v", loaded, err)
	}
}

func TestTurnstileWidgetNonceAndCSP(t *testing.T) {
	t.Parallel()

	cfg := enabled()

	script := string(cfg.Script(`n0nce"x`))
	if !strings.Contains(script, `src="https://challenges.cloudflare.com/turnstile/v0/api.js"`) ||
		!strings.Contains(script, `nonce="n0nce&#34;x"`) {
		t.Errorf("Script = %s", script)
	}

	if s := string(cfg.Script("")); strings.Contains(s, "nonce") {
		t.Errorf("Script without nonce = %s", s)
	}

	widget := string(cfg.Widget(`login"><script>`))
	if !strings.Contains(widget, `data-sitekey="`+testSiteKey+`"`) || strings.Contains(widget, "<script>") {
		t.Errorf("Widget = %s", widget)
	}

	h := middleware.SecureHeaders(middleware.SecureHeadersConfig{CSPSources: cfg.CSPSources()})(
		http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", http.NoBody))

	csp := rec.Header().Get("Content-Security-Policy")
	for _, dir := range []string{"script-src ", "frame-src "} {
		found := false
		for d := range strings.SplitSeq(csp, ";") {
			d = strings.TrimSpace(d)
			found = found || strings.HasPrefix(d, dir) && strings.Contains(d, challenge.TurnstileOrigin)
		}

		if !found {
			t.Errorf("CSP %q misses %s%s", csp, dir, challenge.TurnstileOrigin)
		}
	}
}

func TestTurnstileVerify(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32

	srv := fakeSiteverify(t, &calls)
	cfg := enabled()
	login := func(c challenge.TurnstileConfig) challenge.Verifier { return pointAt(c.Verifier("login"), srv) }
	open := enabled()
	open.InsecureFailOpen = true
	badSecret := enabled()
	badSecret.SecretKey = config.NewSecret("2x0000000000000000000000000000000AA")
	badSecret.InsecureFailOpen = true

	tests := []struct {
		name  string
		v     challenge.Verifier
		token string
		want  int
	}{
		{"valid", login(cfg), "good", http.StatusOK},
		{"missing", login(cfg), "", http.StatusUnprocessableEntity},
		{"invalid", login(cfg), "bad", http.StatusUnprocessableEntity},
		{"action mismatch", pointAt(cfg.Verifier("signup"), srv), "good", http.StatusUnprocessableEntity},
		{"hostname mismatch", login(cfg), "other-host", http.StatusUnprocessableEntity},
		{"retried once with the same idempotency key", login(cfg), "flaky", http.StatusOK},
		{"outage fails closed", login(cfg), "down", http.StatusServiceUnavailable},
		{"provider internal-error fails closed", login(cfg), "internal", http.StatusServiceUnavailable},
		{"outage with InsecureFailOpen", login(open), "down", http.StatusOK},
		{"InsecureFailOpen still refuses a bad token", login(open), "bad", http.StatusUnprocessableEntity},
		{"InsecureFailOpen never covers a bad secret", login(badSecret), "good", http.StatusServiceUnavailable},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := post(serve(t, tt.v, nil), nil, tt.token).Code; got != tt.want {
				t.Errorf("POST = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestTurnstileVerifierTimeout(t *testing.T) {
	t.Parallel()

	release := make(chan struct{})
	slow := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(slow.Close)
	t.Cleanup(func() { close(release) }) // runs first: unblock handlers so Close returns

	v := enabled().Verifier("login")
	v.URL, v.Client = slow.URL, slow.Client()
	v.Client.Timeout = 50 * time.Millisecond

	if got := post(serve(t, v, nil), nil, "good").Code; got != http.StatusServiceUnavailable {
		t.Errorf("slow provider = %d, want 503", got)
	}
}

// logServices returns services logging to buf.
func logServices(buf *bytes.Buffer) *web.Services {
	return &web.Services{Log: slog.New(slog.NewTextHandler(&lockedWriter{w: buf}, nil))}
}

type lockedWriter struct {
	mu sync.Mutex
	w  *bytes.Buffer
}

func (l *lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	return l.w.Write(p)
}

func TestSecurity_A09_TurnstileSecretNeverLogged(t *testing.T) {
	t.Parallel()

	var (
		calls atomic.Int32
		buf   bytes.Buffer
	)

	srv := fakeSiteverify(t, &calls)
	cfg := enabled()
	cfg.InsecureFailOpen = true
	bad := enabled()
	bad.SecretKey = config.NewSecret("2x0000000000000000000000000000000AA")

	for _, v := range []challenge.Verifier{pointAt(cfg.Verifier("login"), srv), pointAt(bad.Verifier("login"), srv)} {
		r := router.New(logServices(&buf))
		r.Use(challenge.Middleware(v, nil))
		r.Post("/register", func(c *web.Ctx) error { return c.String(http.StatusOK, "ok") })

		for _, tok := range []string{"down", "bad", "good"} {
			post(r, nil, tok)
		}
	}

	slog.New(slog.NewTextHandler(&lockedWriter{w: &buf}, nil)).Info("cfg", "turnstile", cfg)
	fmt.Fprintf(&buf, "%v %+v %#v", cfg, cfg, cfg)

	out := buf.String()
	if !strings.Contains(out, "challenge.fail_open") {
		t.Errorf("fail-open not logged as a security event:\n%s", out)
	}

	for _, secret := range []string{testSecretKey, "2x0000000000000000000000000000000AA"} {
		if strings.Contains(out, secret) {
			t.Errorf("secret %q leaked into logs:\n%s", secret, out)
		}
	}
}

func TestLocalPath(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"/items?page=2":         "/items?page=2",
		"/":                     "/",
		"":                      "/",
		"//evil.example/x":      "/",
		"/\\evil.example":       "/",
		"https://evil.example/": "/",
		"javascript:alert(1)":   "/",
		"/ok\r\nSet-Cookie: x":  "/",
		"evil.example":          "/",
		"/%2F%2Fevil.example":   "/%2F%2Fevil.example",
	}

	for in, want := range tests {
		if got := challenge.LocalPath(in); got != want {
			t.Errorf("LocalPath(%q) = %q, want %q", in, got, want)
		}
	}
}
