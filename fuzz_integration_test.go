//go:build fuzzintegration

package vukem

import (
	"net/http/httptest"
	"os/exec"
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
