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

			vukem.ServeOpenAPI(app, r, "/openapi.json")
			apidocs.Serve(app, r, "/docs", apidocs.Config{})

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

	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "<title>Shop API</title>") {
		t.Fatalf("GET /docs = %d %s", rec.Code, rec.Body)
	}

	csp := rec.Header().Get("Content-Security-Policy")
	m := regexp.MustCompile(`'nonce-([^']+)'`).FindStringSubmatch(csp)

	if len(m) < 2 || strings.Count(rec.Body.String(), `nonce="`+m[1]+`"`) != 2 {
		t.Fatalf("scripts do not carry the CSP nonce: %q\n%s", csp, rec.Body)
	}

	for _, want := range []string{"style-src 'self' 'unsafe-inline'", "connect-src 'self'", "frame-ancestors 'none'"} {
		if !strings.Contains(csp, want) {
			t.Errorf("CSP %q lacks %q", csp, want)
		}
	}

	if rec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Error("secure headers missing")
	}

	if get(t, h, "/docs/scalar.js").Code != 200 || get(t, h, "/openapi.json").Code != 200 {
		t.Error("bundle or document not served")
	}
}

func TestSecurity_A02_APIDocsOffInProduction(t *testing.T) {
	t.Parallel()

	h := handler(t, newApp(t, map[string]string{"APP_ENV": "production"}))

	for _, path := range []string{"/docs", "/docs/scalar.js", "/openapi.json"} {
		if rec := get(t, h, path); rec.Code != http.StatusNotFound {
			t.Errorf("production GET %s = %d, want 404", path, rec.Code)
		}
	}

	h = handler(t, newApp(t, map[string]string{"APP_ENV": "production", "OPENAPI_IN_PRODUCTION": "true"}))
	if rec := get(t, h, "/docs"); rec.Code != 200 {
		t.Errorf("opted-in production GET /docs = %d", rec.Code)
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
