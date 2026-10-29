package inertia_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/AlexGalhardo/vukem/i18n"
	"github.com/AlexGalhardo/vukem/inertia"
	"github.com/AlexGalhardo/vukem/router"
	"github.com/AlexGalhardo/vukem/web"
)

func TestViewDataReachesRootView(t *testing.T) {
	t.Parallel()

	cfg := baseConfig()
	cfg.ViewData = func(r *http.Request) map[string]any {
		theme := "light"
		if c, err := r.Cookie("theme"); err == nil && c.Value == "dark" {
			theme = "dark"
		}

		return map[string]any{"theme": theme}
	}

	hs := newHarness(t, cfg, eventRoutes)

	body := hs.do(t, http.MethodGet, "/events/80", map[string]string{"Cookie": "theme=dark"}).Body.String()
	if !strings.Contains(body, `<meta name="theme" content="dark">`) {
		t.Fatalf("root view = %q, want the theme from ViewData", body)
	}

	// Inertia visits get JSON: ViewData is not part of the page object.
	if p := page(t, hs.do(t, http.MethodGet, "/events/80", xhr("Cookie", "theme=dark"))); p["theme"] != nil {
		t.Fatalf("page object carries view data: %v", p)
	}
}

func TestPageURLKeepsLocalePrefix(t *testing.T) {
	t.Parallel()

	hs := newHarness(t, baseConfig(), func(r *router.Router) {
		eventRoutes(r)
		r.Get("/", func(c *web.Ctx) error { return inertia.Render(c, "Home", nil) })
	})
	hs.h = i18n.StripPathPrefix(map[string]string{"pt-br": "pt-BR"})(hs.h)

	tests := map[string]string{
		"/pt-br/events/80?tab=info": "/pt-br/events/80?tab=info",
		"/pt-br":                    "/pt-br",
		"/pt-br/?q=1":               "/pt-br?q=1",
		"/events/80":                "/events/80",
	}

	for target, want := range tests {
		if got := page(t, hs.do(t, http.MethodGet, target, xhr()))["url"]; got != want {
			t.Errorf("GET %s: page url = %v, want %s", target, got, want)
		}
	}

	// A stale asset version reloads the prefixed URL, not the stripped one.
	rec := hs.do(t, http.MethodGet, "/pt-br/events/80", xhr("X-Inertia-Version", "old"))
	if got := rec.Header().Get(inertia.HeaderLocation); got != "http://example.com/pt-br/events/80" {
		t.Errorf("version conflict location = %q, want the prefixed URL", got)
	}
}
