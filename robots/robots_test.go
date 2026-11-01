package robots_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/AlexGalhardo/vukem/robots"
	"github.com/AlexGalhardo/vukem/router"
	"github.com/AlexGalhardo/vukem/web"
)

func TestWrite(t *testing.T) {
	t.Parallel()

	f := robots.File{
		Groups: []robots.Group{
			{
				UserAgents: []string{"Googlebot", "Bingbot"},
				Allow:      []string{"/public/"},
				Disallow:   []string{"/admin/", "/*.pdf$"},
			},
			{UserAgents: []string{"*"}, Disallow: []string{""}},
		},
		Sitemaps: []string{"https://shop.example/sitemap.xml"},
	}

	var b strings.Builder
	if err := robots.Write(&b, f); err != nil {
		t.Fatal(err)
	}

	want := "User-agent: Googlebot\nUser-agent: Bingbot\nAllow: /public/\nDisallow: /admin/\nDisallow: /*.pdf$\n\n" +
		"User-agent: *\nDisallow:\n\nSitemap: https://shop.example/sitemap.xml\n"
	if diff := cmp.Diff(want, b.String()); diff != "" {
		t.Errorf("Write (-want +got):\n%s", diff)
	}

	b.Reset()
	f.NoIndex = true

	if err := robots.Write(&b, f); err != nil || b.String() != robots.DisallowAll {
		t.Errorf("Write(NoIndex) = %q, %v; want DisallowAll", b.String(), err)
	}
}

func TestSecurity_A03_RobotsRefusesInjectedLines(t *testing.T) {
	t.Parallel()

	for name, f := range map[string]robots.File{
		"no agent":     {Groups: []robots.Group{{Disallow: []string{"/"}}}},
		"agent break":  {Groups: []robots.Group{{UserAgents: []string{"x\nDisallow: /"}}}},
		"rule break":   {Groups: []robots.Group{{UserAgents: []string{"*"}, Allow: []string{"/a\nSitemap: https://evil.test"}}}},
		"rule no path": {Groups: []robots.Group{{UserAgents: []string{"*"}, Disallow: []string{"admin"}}}},
		"rule comment": {Groups: []robots.Group{{UserAgents: []string{"*"}, Disallow: []string{"/a#b"}}}},
		"sitemap path": {Sitemaps: []string{"/sitemap.xml"}},
		"sitemap js":   {Sitemaps: []string{"javascript:alert(1)"}},
	} {
		if err := f.Validate(); err == nil {
			t.Errorf("Validate(%s) = nil, want an error", name)
		}

		if _, err := robots.Handler(f); err == nil {
			t.Errorf("Handler(%s) = nil error, want an error", name)
		}
	}
}

func TestRegister(t *testing.T) {
	t.Parallel()

	r := router.New(&web.Services{})
	if err := robots.Register(r, robots.File{NoIndex: true}); err != nil {
		t.Fatal(err)
	}

	h, err := r.Handler()
	if err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/robots.txt", nil))

	if rec.Code != http.StatusOK || rec.Body.String() != robots.DisallowAll ||
		rec.Header().Get("Content-Type") != "text/plain; charset=utf-8" || rec.Header().Get("ETag") == "" {
		t.Errorf("GET /robots.txt = %d %q %v", rec.Code, rec.Body.String(), rec.Header())
	}

	if _, err := robots.Handler(robots.File{}); err == nil {
		t.Error("Handler(empty) = nil error, want an error")
	}
}
