//go:build fuzzintegration

package vukem

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"sync/atomic"
	"testing"
)

// Run with: go test -tags fuzzintegration -run TestFuzzRealSchemathesis .
// It pulls the pinned Schemathesis image and needs a running Docker daemon.
func TestFuzzRealSchemathesis(t *testing.T) {
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker not installed")
	}

	app := newTestApp(t, WithRoutes(openAPIRoutes))

	h, err := app.Handler()
	if err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)

	_, err = runFuzz(t, execFuzzRunner{}, openAPIRoutes, nil,
		"-url", srv.URL, "-prefix", "/api", "-max-examples", "3", "-report-dir", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
}

// The credential reaches Schemathesis only through the environment and the
// ${VUKEM_FUZZ_AUTH} reference in schemathesis.toml; this proves the
// interpolation works on the pinned image.
func TestFuzzRealSchemathesisSendsAuthHeader(t *testing.T) {
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker not installed")
	}

	app := newTestApp(t, WithRoutes(openAPIRoutes))

	h, err := app.Handler()
	if err != nil {
		t.Fatal(err)
	}

	var withAuth, apiCalls atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/ping" {
			apiCalls.Add(1)

			if r.Header.Get("Authorization") == "Bearer s3cret" {
				withAuth.Add(1)
			}
		}

		h.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)

	auth := WithFuzzAuth(func(context.Context, *App) (FuzzAuth, error) {
		return FuzzAuth{Header: "Authorization", Value: "Bearer s3cret"}, nil
	})

	_, err = runFuzz(t, execFuzzRunner{}, openAPIRoutes, []Option{auth},
		"-url", srv.URL, "-prefix", "/api", "-max-examples", "3", "-report-dir", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	if apiCalls.Load() == 0 || withAuth.Load() != apiCalls.Load() {
		t.Fatalf("authorised calls = %d of %d, want all", withAuth.Load(), apiCalls.Load())
	}
}
