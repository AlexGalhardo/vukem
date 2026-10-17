package vukem

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/AlexGalhardo/vukem/router"
	"github.com/AlexGalhardo/vukem/web"
)

// clientProvider opens its client in Boot, like a Mongo or S3 provider.
type clientProvider struct {
	client *string
	rec    *recorder
}

func (p *clientProvider) Register(*App) error { return nil }

func (p *clientProvider) Boot(context.Context, *App) error {
	p.rec.add("boot")
	p.client = new("connected")

	return nil
}

func TestRoutesAfterBootSeeBootedServices(t *testing.T) {
	t.Parallel()

	rec := &recorder{}
	p := &clientProvider{rec: rec}

	app, err := New(WithSource(testSource(nil)), WithLogger(discardLogger()), WithProviders(p),
		WithRoutes(func(*router.Router, *App) error { rec.add("routes"); return nil }),
		WithRoutesAfterBoot(func(r *router.Router, _ *App) error {
			rec.add("routes-after-boot")

			if p.client == nil {
				return errors.New("client is nil")
			}

			client := *p.client
			r.Get("/client", func(c *web.Ctx) error { return c.String(http.StatusOK, client) }).Name("client")

			return nil
		}),
	)
	if err != nil {
		t.Fatal(err)
	}

	if hasRoute(app, "client") {
		t.Fatal("after-boot route registered before Boot")
	}

	if err := app.Boot(t.Context()); err != nil {
		t.Fatal(err)
	}

	if err := app.Boot(t.Context()); err != nil { // idempotent: routes are added once
		t.Fatal(err)
	}

	if want, got := []string{"routes", "boot", "routes-after-boot"}, rec.get(); !slices.Equal(got, want) {
		t.Fatalf("order = %v, want %v", got, want)
	}

	h, err := app.Handler()
	if err != nil {
		t.Fatal(err)
	}

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/client", nil))

	if rr.Code != http.StatusOK || rr.Body.String() != "connected" {
		t.Fatalf("GET /client = %d %q", rr.Code, rr.Body.String())
	}
}

func TestRoutesAfterBootErrors(t *testing.T) {
	t.Parallel()

	failing := WithRoutesAfterBoot(func(*router.Router, *App) error { return errors.New("boom") })

	app, err := New(WithSource(testSource(nil)), WithLogger(discardLogger()), failing)
	if err != nil {
		t.Fatal(err)
	}

	if err := app.Boot(t.Context()); err == nil || !strings.Contains(err.Error(), "routes after boot: boom") {
		t.Fatalf("Boot() = %v, want routes after boot error", err)
	}

	// A router compiled before Boot (here through Router.Handler) would
	// silently miss the late routes.
	late := WithRoutesAfterBoot(func(r *router.Router, _ *App) error {
		r.Get("/late", func(*web.Ctx) error { return nil })
		return nil
	})

	app, err = New(WithSource(testSource(nil)), WithLogger(discardLogger()), late)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := app.Router.Handler(); err != nil {
		t.Fatal(err)
	}

	if err := app.Boot(t.Context()); !errors.Is(err, ErrRoutesCompiled) {
		t.Fatalf("Boot() after Handler() = %v, want ErrRoutesCompiled", err)
	}
}

func TestRoutesAfterBootInWarmup(t *testing.T) {
	t.Parallel()

	p := &clientProvider{rec: &recorder{}}

	app, err := New(WithSource(testSource(nil)), WithLogger(discardLogger()), WithKind(KindWarmup),
		WithProviders(p),
		WithRoutesAfterBoot(func(r *router.Router, _ *App) error {
			r.Get("/client", func(*web.Ctx) error { return nil }).Name("client")
			return nil
		}),
	)
	if err != nil {
		t.Fatal(err)
	}

	// Warmup never boots, yet codegen:routes needs the full route table.
	if !hasRoute(app, "client") {
		t.Fatal("after-boot route missing in warmup")
	}

	if got := p.rec.get(); len(got) != 0 {
		t.Fatalf("warmup booted providers: %v", got)
	}
}

func hasRoute(app *App, name string) bool {
	return slices.ContainsFunc(app.Router.Routes(), func(r router.RouteInfo) bool { return r.Name == name })
}

func TestBootErrorIsSticky(t *testing.T) {
	t.Parallel()

	p := &testProvider{name: "x", rec: &recorder{}, fail: "boot"}

	app, err := New(WithSource(testSource(nil)), WithLogger(discardLogger()), WithProviders(p))
	if err != nil {
		t.Fatal(err)
	}

	first := app.Boot(t.Context())
	if first == nil {
		t.Fatal("Boot() = nil, want boot error")
	}

	if again := app.Boot(t.Context()); !errors.Is(again, first) {
		t.Fatalf("second Boot() = %v, want %v", again, first)
	}

	if got := p.rec.get(); len(got) != 2 { // register + one boot
		t.Fatalf("provider calls = %v, want Boot once", got)
	}
}

func TestHandlerRefusesPendingRoutesAfterBoot(t *testing.T) {
	t.Parallel()

	app, err := New(WithSource(testSource(nil)), WithLogger(discardLogger()),
		WithRoutesAfterBoot(func(*router.Router, *App) error { return nil }))
	if err != nil {
		t.Fatal(err)
	}

	if _, err := app.Handler(); !errors.Is(err, ErrNotBooted) {
		t.Fatalf("Handler() before Boot = %v, want ErrNotBooted", err)
	}

	if app.Router.Compiled() {
		t.Fatal("Handler() compiled the router while routes were pending")
	}

	if err := app.Boot(t.Context()); err != nil {
		t.Fatal(err)
	}

	if _, err := app.Handler(); err != nil {
		t.Fatalf("Handler() after Boot = %v", err)
	}
}

func TestRoutesAfterBootPanicInWarmup(t *testing.T) {
	t.Parallel()

	p := &clientProvider{rec: &recorder{}}

	_, err := New(WithSource(testSource(nil)), WithLogger(discardLogger()), WithKind(KindWarmup),
		WithProviders(p),
		WithRoutesAfterBoot(func(r *router.Router, _ *App) error {
			client := *p.client // nil: warmup never boots
			r.Get("/client", func(c *web.Ctx) error { return c.String(http.StatusOK, client) })

			return nil
		}),
	)
	if err == nil || !strings.Contains(err.Error(), "must not use booted services during warmup") {
		t.Fatalf("New() in warmup = %v, want warmup panic error", err)
	}
}

func TestErrRoutesCompiledHint(t *testing.T) {
	t.Parallel()

	if msg := ErrRoutesCompiled.Error(); !strings.Contains(msg, "call Boot before Handler/Router.URL") {
		t.Fatalf("ErrRoutesCompiled = %q, want a hint", msg)
	}
}
