package router_test

import (
	"bytes"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/AlexGalhardo/vukem/middleware"
	"github.com/AlexGalhardo/vukem/router"
	"github.com/AlexGalhardo/vukem/web"
)

func TestSecurity_A09_SecretParamsNeverLogged(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&buf, nil))

	r := router.New(&web.Services{Log: log})
	r.UseServer(middleware.Logger(log))
	r.Get("/reset-password/{token}", func(c *web.Ctx) error {
		web.SecurityEvent(c.Request(), "test.event")
		return errors.New("boom") // logged by the error handler too
	}).SecretParams("token")
	r.Get("/files/{key...}", func(c *web.Ctx) error { return c.String(200, "ok") }).SecretParams("key")
	r.Get("/items/{id}", func(c *web.Ctx) error { return c.String(200, "ok") })

	h, err := r.Handler()
	if err != nil {
		t.Fatal(err)
	}

	for _, target := range []string{"/reset-password/s3cr3t-T0KEN", "/files/a/s3cr3t-K3Y.pdf", "/items/42"} {
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequestWithContext(t.Context(), http.MethodGet, target, nil))
	}

	out := buf.String()
	for _, secret := range []string{"s3cr3t-T0KEN", "s3cr3t-K3Y"} {
		if strings.Contains(out, secret) {
			t.Errorf("log contains %q:\n%s", secret, out)
		}
	}

	for _, want := range []string{`"path":"/reset-password/{token}"`, `"path":"/files/{key}"`, `"path":"/items/42"`} {
		if !strings.Contains(out, want) {
			t.Errorf("log lacks %s:\n%s", want, out)
		}
	}
}
