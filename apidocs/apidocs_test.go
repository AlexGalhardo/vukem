package apidocs_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"io"
	"log/slog"
	"maps"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/AlexGalhardo/vukem"
	"github.com/AlexGalhardo/vukem/apidocs"
	"github.com/AlexGalhardo/vukem/config"
	"github.com/AlexGalhardo/vukem/middleware"
	"github.com/AlexGalhardo/vukem/router"
)

var update = flag.Bool("update", false, "rewrite golden files")

func get(t *testing.T, h http.Handler, target string) *httptest.ResponseRecorder {
	t.Helper()

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, target, http.NoBody))

	return rec
}

func TestScalarBundlePinned(t *testing.T) {
	t.Parallel()

	r := router.New(nil)
	apidocs.Register(r, "/docs", apidocs.Config{})

	rec := get(t, r, "/docs/scalar.js?v="+apidocs.ScalarVersion)
	sum := sha256.Sum256(rec.Body.Bytes())

	if got := hex.EncodeToString(sum[:]); got != apidocs.ScalarSHA256 {
		t.Fatalf("scalar.js sha256 = %s, want %s", got, apidocs.ScalarSHA256)
	}

	if rec.Header().Get("Content-Type") != "text/javascript; charset=utf-8" ||
		!strings.Contains(rec.Header().Get("Cache-Control"), "immutable") {
		t.Errorf("headers = %v", rec.Header())
	}
}

func TestPageGolden(t *testing.T) {
	t.Parallel()

	r := router.New(nil)
	apidocs.Register(r, "/reference/", apidocs.Config{
		SpecURL: "/api/openapi.json", Title: "Shop </title>", Theme: "purple", Layout: "classic",
		Authentication: map[string]any{"preferredSecurityScheme": "bearer"}, HideClientButton: true,
	})

	got := get(t, r, "/reference").Body.Bytes()
	golden := filepath.Join("testdata", "page.golden.html")

	if *update {
		if err := os.WriteFile(golden, got, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}

	if diff := cmp.Diff(string(bytes.ReplaceAll(want, []byte("\r\n"), []byte("\n"))), string(got)); diff != "" {
		t.Errorf("page (-want +got):\n%s", diff)
	}
}

func newApp(t *testing.T, env map[string]string) *vukem.App {
	t.Helper()

	return newAppWith(t, env, apidocs.Config{})
}

func newAppWith(t *testing.T, env map[string]string, cfg apidocs.Config) *vukem.App {
	t.Helper()

	m := map[string]string{"APP_KEY": config.GenerateKey(), "APP_ENV": "development", "APP_NAME": "Shop"}
	maps.Copy(m, env)

	app, err := vukem.New(
		vukem.WithSource(config.MapSource(m)),
		vukem.WithLogger(slog.New(slog.DiscardHandler)),
		vukem.WithStdout(io.Discard),
		vukem.WithRoutes(func(r *router.Router, app *vukem.App) error {
			if err := vukem.DefaultMiddleware(app, r); err != nil {
				return err
			}

			vukem.ServeOpenAPIWith(app, r, "/openapi.json", vukem.OpenAPIOptions{InProduction: cfg.InProduction})
			apidocs.Serve(app, r, "/docs", cfg)

			return nil
		}),
	)
	if err != nil {
		t.Fatal(err)
	}

	return app
}

func handler(t *testing.T, app *vukem.App) http.Handler {
	t.Helper()

	h, err := app.Handler()
	if err != nil {
		t.Fatal(err)
	}

	return h
}

func TestPageWorksUnderTheDefaultSecureHeaders(t *testing.T) {
	t.Parallel()

	h := handler(t, newApp(t, nil))
	rec := get(t, h, "/docs")
	body := rec.Body.String()

	if rec.Code != 200 || !strings.Contains(body, "<title>Shop API</title>") {
		t.Fatalf("GET /docs = %d %s", rec.Code, rec.Body)
	}

	csp := rec.Header().Get("Content-Security-Policy")
	m := regexp.MustCompile(`'nonce-([^']+)'`).FindStringSubmatch(csp)

	// Three scripts and the theme stylesheet carry the nonce, and Scalar
	// reads it from the csp-nonce meta for the styles it injects.
	if len(m) < 2 || strings.Count(body, `nonce="`+m[1]+`"`) != 4 ||
		!strings.Contains(body, `<meta property="csp-nonce" content="`+m[1]+`">`) {
		t.Fatalf("page does not carry the CSP nonce: %q\n%s", csp, body)
	}

	for _, want := range []string{"style-src 'self' 'nonce-" + m[1] + "';", "connect-src 'self'", "frame-ancestors 'none'"} {
		if !strings.Contains(csp, want) {
			t.Errorf("CSP %q lacks %q", csp, want)
		}
	}

	if rec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Error("secure headers missing")
	}

	for _, path := range []string{"/docs/scalar.js", "/docs/page.js", "/openapi.json"} {
		if get(t, h, path).Code != 200 {
			t.Errorf("GET %s not served", path)
		}
	}

	if js := get(t, h, "/docs/page.js"); js.Header().Get("Content-Type") != "text/javascript; charset=utf-8" ||
		!strings.Contains(js.Body.String(), "createElement") {
		t.Errorf("page.js = %v %.80q", js.Header(), js.Body)
	}
}

func TestSecurity_A02_APIDocsCSPNonceOnly(t *testing.T) {
	t.Parallel()

	h := handler(t, newApp(t, nil))
	nonce := regexp.MustCompile(`'nonce-[^']+'`)

	docs := get(t, h, "/docs").Header().Get("Content-Security-Policy")
	if strings.Contains(docs, "'unsafe-") || !strings.Contains(docs, "style-src 'self' 'nonce-") {
		t.Errorf("docs CSP = %q, want nonce-only styles and no 'unsafe-*'", docs)
	}

	if got, want := nonce.ReplaceAllString(
		docs,
		"N",
	), strings.ReplaceAll(
		apidocs.CSP,
		"'nonce-{nonce}'",
		"N",
	); got != want {
		t.Errorf("docs CSP = %q, want %q", got, want)
	}

	// The relaxation (data: fonts) is scoped to the docs page.
	for _, path := range []string{"/openapi.json", "/up"} {
		got := nonce.ReplaceAllString(get(t, h, path).Header().Get("Content-Security-Policy"), "N")
		if want := strings.ReplaceAll(middleware.DefaultCSP, "'nonce-{nonce}'", "N"); got != want {
			t.Errorf("GET %s CSP = %q, want the default %q", path, got, want)
		}
	}
}

func TestDefaultThemeColorsMeetAA(t *testing.T) {
	t.Parallel()

	css, err := os.ReadFile("page.css")
	if err != nil {
		t.Fatal(err)
	}

	// Backgrounds of Scalar's default theme (--scalar-background-1/2 and the
	// sidebar search box) that axe measured the text against.
	backgrounds := map[string][]string{
		"light-mode": {"#ffffff", "#f6f6f6", "#fbfbfb"},
		"dark-mode":  {"#0f0f0f", "#151515", "#1a1a1a"},
	}
	block := regexp.MustCompile(`\.([a-z]+-mode)\.[a-z]+-mode \{([^}]*)\}`)
	decl := regexp.MustCompile(`(--scalar-color-[a-z0-9]+): (#[0-9a-f]{6});`)
	checked := 0

	for _, b := range block.FindAllStringSubmatch(string(css), -1) {
		for _, d := range decl.FindAllStringSubmatch(b[2], -1) {
			for _, bg := range backgrounds[b[1]] {
				if r := contrast(t, d[2], bg); r < 4.5 {
					t.Errorf("%s %s %s on %s = %.2f:1, want >= 4.5", b[1], d[1], d[2], bg, r)
				}

				checked++
			}
		}
	}

	if checked < 20 {
		t.Fatalf("checked %d color pairs; page.css format changed?", checked)
	}

	r := router.New(nil)
	apidocs.Register(r, "/docs", apidocs.Config{})

	if !strings.Contains(get(t, r, "/docs").Body.String(), "--scalar-color-3: #676767") {
		t.Error("default theme page lacks the contrast overrides")
	}
}

// contrast is the WCAG 2 contrast ratio of two #rrggbb colors.
func contrast(t *testing.T, a, b string) float64 {
	t.Helper()

	lum := func(hexColor string) float64 {
		raw, err := hex.DecodeString(strings.TrimPrefix(hexColor, "#"))
		if err != nil || len(raw) != 3 {
			t.Fatalf("bad color %q", hexColor)
		}

		var l float64

		for i, w := range []float64{0.2126, 0.7152, 0.0722} {
			c := float64(raw[i]) / 255
			if c <= 0.03928 {
				c /= 12.92
			} else {
				c = math.Pow((c+0.055)/1.055, 2.4)
			}

			l += w * c
		}

		return l
	}

	la, lb := lum(a), lum(b)

	return (max(la, lb) + 0.05) / (min(la, lb) + 0.05)
}

func TestSecurity_A02_APIDocsOffInProduction(t *testing.T) {
	t.Parallel()

	h := handler(t, newApp(t, map[string]string{"APP_ENV": "production"}))

	for _, path := range []string{"/docs", "/docs/scalar.js", "/docs/page.js", "/openapi.json"} {
		if rec := get(t, h, path); rec.Code != http.StatusNotFound {
			t.Errorf("production GET %s = %d, want 404", path, rec.Code)
		}
	}

	optIns := map[string]*vukem.App{
		"OPENAPI_IN_PRODUCTION": newApp(t, map[string]string{"APP_ENV": "production", "OPENAPI_IN_PRODUCTION": "true"}),
		"Config.InProduction": newAppWith(t, map[string]string{"APP_ENV": "production"},
			apidocs.Config{InProduction: true}),
	}

	for name, app := range optIns {
		h := handler(t, app)
		for _, path := range []string{"/docs", "/openapi.json"} {
			if rec := get(t, h, path); rec.Code != 200 {
				t.Errorf("%s: production GET %s = %d, want 200", name, path, rec.Code)
			}
		}
	}
}

func TestServeCommand(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	cmd := apidocs.Command()

	fs := flag.NewFlagSet("openapi:serve", flag.ContinueOnError)
	cmd.Flags(fs)

	if err := fs.Parse([]string{"-addr", "127.0.0.1:0"}); err != nil {
		t.Fatal(err)
	}

	go func() { done <- cmd.Run(ctx, newApp(t, nil), nil) }()

	cancel()

	// Cancelled before or after listening: both end cleanly.
	if err := <-done; err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("openapi:serve = %v", err)
	}

	if cmd.Name() != "openapi:serve" {
		t.Errorf("name = %q", cmd.Name())
	}
}
