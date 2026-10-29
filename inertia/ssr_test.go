package inertia_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/AlexGalhardo/vukem/cache"
	"github.com/AlexGalhardo/vukem/inertia"
	"github.com/AlexGalhardo/vukem/router"
	"github.com/AlexGalhardo/vukem/web"
)

type ssrServer struct {
	*httptest.Server

	mu    sync.Mutex
	paths []string
	pages []map[string]any
}

func newSSRServer(t *testing.T, h func(w http.ResponseWriter, r *http.Request)) *ssrServer {
	t.Helper()

	s := &ssrServer{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)

		var p map[string]any
		_ = json.Unmarshal(body, &p)

		s.mu.Lock()
		s.paths = append(s.paths, r.Method+" "+r.URL.Path)
		s.pages = append(s.pages, p)
		s.mu.Unlock()

		h(w, r)
	}))
	t.Cleanup(s.Close)

	return s
}

func rendered(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/health" {
		_, _ = io.WriteString(w, `{"status":"OK"}`)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(w, `{"head":["<title>Event</title>","<meta name=\"x\" content=\"y\">"],`+
		`"body":"<script data-page=\"app\" type=\"application/json\">{}</script><div data-server-rendered=\"true\" id=\"app\"><h1>Event</h1></div>"}`)
}

func ssrHarness(t *testing.T, ssr *inertia.SSRConfig) (*harness, *[]inertia.SSRError) {
	t.Helper()

	var (
		mu       sync.Mutex
		failures []inertia.SSRError
	)

	ssr.OnFailure = func(_ context.Context, e inertia.SSRError) {
		mu.Lock()
		failures = append(failures, e)
		mu.Unlock()
	}

	cfg := baseConfig()
	cfg.SSR = ssr

	return newHarness(t, cfg, func(r *router.Router) {
		r.Get("/events/{id}", func(c *web.Ctx) error {
			return inertia.Render(c, "Event", inertia.Props{"id": c.Param("id")})
		})
		r.Get("/admin", func(c *web.Ctx) error { return inertia.Render(c, "Admin", nil) })
		r.Get("/plain", func(c *web.Ctx) error {
			inertia.DisableSSR(c.Context())
			return inertia.Render(c, "Plain", nil)
		})
		r.Get("/required", func(c *web.Ctx) error {
			inertia.RequireSSR(c.Context())
			return inertia.Render(c, "Required", nil)
		})
	}), &failures
}

const csrMarker = `<div id="app"></div>`

func TestSSRRendersHeadAndBody(t *testing.T) {
	t.Parallel()

	srv := newSSRServer(t, rendered)
	hs, failures := ssrHarness(t, &inertia.SSRConfig{URL: srv.URL})

	body := hs.do(t, http.MethodGet, "/events/80", nil).Body.String()
	want := `<html><head><title>Event</title>` + "\n" + `<meta name="x" content="y"></head><body>` +
		`<script data-page="app" type="application/json">{}</script><div data-server-rendered="true" id="app"><h1>Event</h1></div></body></html>`

	if body != want {
		t.Errorf("SSR page =\n%s\nwant\n%s", body, want)
	}

	if len(srv.paths) != 1 || srv.paths[0] != "POST /render" || srv.pages[0]["component"] != "Event" ||
		len(*failures) != 0 {
		t.Errorf("SSR requests = %v pages = %v failures = %v", srv.paths, srv.pages, *failures)
	}

	if p := page(t, hs.do(t, http.MethodGet, "/events/80", xhr())); p["component"] != "Event" || len(srv.paths) != 1 {
		t.Errorf("Inertia visit hit SSR: %v", srv.paths)
	}
}

func TestSSRFallsBackToClientRendering(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		handler  func(http.ResponseWriter, *http.Request)
		ssr      func(url string) *inertia.SSRConfig
		wantType string
	}{
		{
			name: "render error payload",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = io.WriteString(w, `{"error":"window is not defined","type":"browser-api","hint":"wrap it"}`)
			},
			ssr:      func(u string) *inertia.SSRConfig { return &inertia.SSRConfig{URL: u} },
			wantType: "browser-api",
		},
		{
			name:     "timeout",
			handler:  func(_ http.ResponseWriter, r *http.Request) { <-r.Context().Done() },
			ssr:      func(u string) *inertia.SSRConfig { return &inertia.SSRConfig{URL: u, Timeout: 50 * time.Millisecond} },
			wantType: "connection",
		},
		{
			name:     "unreachable server",
			handler:  rendered,
			ssr:      func(string) *inertia.SSRConfig { return &inertia.SSRConfig{URL: "http://127.0.0.1:1"} },
			wantType: "connection",
		},
		{
			name: "malformed body",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				_, _ = io.WriteString(w, `not json`)
			},
			ssr:      func(u string) *inertia.SSRConfig { return &inertia.SSRConfig{URL: u} },
			wantType: "render",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			srv := newSSRServer(t, tt.handler)
			hs, failures := ssrHarness(t, tt.ssr(srv.URL))

			rec := hs.do(t, http.MethodGet, "/events/80", nil)
			if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), csrMarker) {
				t.Fatalf("fallback = %d %s", rec.Code, rec.Body.String())
			}

			if len(*failures) != 1 || (*failures)[0].Type != tt.wantType || (*failures)[0].Component != "Event" {
				t.Errorf("failures = %+v, want one of type %q", *failures, tt.wantType)
			}
		})
	}
}

func TestSSRSkips(t *testing.T) {
	t.Parallel()

	srv := newSSRServer(t, rendered)
	missing := filepath.Join(t.TempDir(), "ssr.js")

	hs, _ := ssrHarness(t, &inertia.SSRConfig{URL: srv.URL, Except: []string{"/admin"}})
	for _, p := range []string{"/admin", "/plain"} {
		if body := hs.do(t, http.MethodGet, p, nil).Body.String(); !strings.Contains(body, csrMarker) {
			t.Errorf("%s was server-rendered: %s", p, body)
		}
	}

	noBundle, _ := ssrHarness(t, &inertia.SSRConfig{URL: srv.URL, Bundle: missing})
	if body := noBundle.do(t, http.MethodGet, "/events/1", nil).Body.String(); !strings.Contains(body, csrMarker) {
		t.Errorf("missing bundle was server-rendered: %s", body)
	}

	if len(srv.paths) != 0 {
		t.Errorf("SSR server called for skipped pages: %v", srv.paths)
	}

	if err := os.WriteFile(missing, []byte("//"), 0o600); err != nil {
		t.Fatal(err)
	}

	if body := noBundle.do(t, http.MethodGet, "/events/1", nil).Body.String(); strings.Contains(body, csrMarker) {
		t.Errorf("existing bundle not used: %s", body)
	}
}

func TestSSRDevEndpointAndHealth(t *testing.T) {
	t.Parallel()

	dev := newSSRServer(t, rendered)
	prod := newSSRServer(t, rendered)

	hs, _ := ssrHarness(t, &inertia.SSRConfig{
		URL:    prod.URL,
		Bundle: filepath.Join(t.TempDir(), "absent.js"), // ignored while the dev server runs
		HotURL: func() string { return dev.URL },
	})

	if body := hs.do(t, http.MethodGet, "/events/1", nil).Body.String(); strings.Contains(body, csrMarker) {
		t.Fatalf("dev SSR not used: %s", body)
	}

	if len(dev.paths) != 1 || dev.paths[0] != "POST /__inertia_ssr" || len(prod.paths) != 0 {
		t.Errorf("dev=%v prod=%v", dev.paths, prod.paths)
	}

	in, err := inertia.New(inertia.Config{SSR: &inertia.SSRConfig{URL: prod.URL}})
	if err != nil {
		t.Fatal(err)
	}

	if err := in.SSRHealthy(t.Context()); err != nil {
		t.Errorf("SSRHealthy() = %v", err)
	}

	down, _ := inertia.New(inertia.Config{SSR: &inertia.SSRConfig{URL: "http://127.0.0.1:1"}})
	if err := down.SSRHealthy(t.Context()); err == nil {
		t.Error("SSRHealthy() on a closed port = nil")
	}
}

func TestSSRStrictAnswers503(t *testing.T) {
	t.Parallel()

	broken := func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, `{"error":"window is not defined","type":"browser-api"}`)
	}
	missing := filepath.Join(t.TempDir(), "ssr.js")

	tests := []struct {
		name     string
		ssr      func(url string) *inertia.SSRConfig
		path     string
		wantCode int
		wantType string
	}{
		{"strict render error", func(u string) *inertia.SSRConfig {
			return &inertia.SSRConfig{URL: u, Strict: true}
		}, "/events/1", http.StatusServiceUnavailable, "browser-api"},
		{"strict unreachable", func(string) *inertia.SSRConfig {
			return &inertia.SSRConfig{URL: "http://127.0.0.1:1", Strict: true}
		}, "/events/1", http.StatusServiceUnavailable, "connection"},
		{"strict missing bundle", func(u string) *inertia.SSRConfig {
			return &inertia.SSRConfig{URL: u, Bundle: missing, Strict: true}
		}, "/events/1", http.StatusServiceUnavailable, "bundle"},
		{"RequireSSR route", func(u string) *inertia.SSRConfig {
			return &inertia.SSRConfig{URL: u}
		}, "/required", http.StatusServiceUnavailable, "browser-api"},
		{"other route falls back", func(u string) *inertia.SSRConfig {
			return &inertia.SSRConfig{URL: u}
		}, "/events/1", http.StatusOK, "browser-api"},
		{"DisableSSR beats Strict", func(u string) *inertia.SSRConfig {
			return &inertia.SSRConfig{URL: u, Strict: true}
		}, "/plain", http.StatusOK, ""},
		{"Except beats Strict", func(u string) *inertia.SSRConfig {
			return &inertia.SSRConfig{URL: u, Strict: true, Except: []string{"/admin"}}
		}, "/admin", http.StatusOK, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			srv := newSSRServer(t, broken)
			hs, failures := ssrHarness(t, tt.ssr(srv.URL))

			rec := hs.do(t, http.MethodGet, tt.path, nil)
			if rec.Code != tt.wantCode {
				t.Fatalf("GET %s = %d, want %d: %s", tt.path, rec.Code, tt.wantCode, rec.Body.String())
			}

			if tt.wantCode == http.StatusServiceUnavailable {
				if got := rec.Header().Get("Retry-After"); got == "" {
					t.Error("Retry-After header missing on 503")
				}

				if body := rec.Body.String(); strings.Contains(body, csrMarker) ||
					strings.Contains(body, "window is not defined") {
					t.Errorf("503 body leaks a client shell or SSR detail: %s", body)
				}
			}

			var gotType string
			if len(*failures) > 0 {
				gotType = (*failures)[0].Type
			}

			if len(*failures) > 1 || gotType != tt.wantType {
				t.Errorf("failures = %+v, want type %q", *failures, tt.wantType)
			}
		})
	}
}

func TestSSRCacheReusesIdenticalRenders(t *testing.T) {
	t.Parallel()

	srv := newSSRServer(t, rendered)
	store := cache.NewMemory(100)
	hs, failures := ssrHarness(t, &inertia.SSRConfig{URL: srv.URL, Cache: store, CacheTTL: time.Minute})

	first := hs.do(t, http.MethodGet, "/events/80", nil).Body.String()
	second := hs.do(t, http.MethodGet, "/events/80", nil).Body.String()

	if first != second || !strings.Contains(second, "<h1>Event</h1>") || len(srv.paths) != 1 {
		t.Fatalf("cached render differs or SSR called %d times (want 1):\n%s\n%s", len(srv.paths), first, second)
	}

	// Other props are another key.
	hs.do(t, http.MethodGet, "/events/81", nil)

	if len(srv.paths) != 2 || len(*failures) != 0 {
		t.Errorf("SSR requests = %v failures = %v, want 2 renders", srv.paths, *failures)
	}
}

func TestSSRCacheSkipsFailuresAndCacheWhen(t *testing.T) {
	t.Parallel()

	var fail atomic.Bool

	fail.Store(true)

	srv := newSSRServer(t, func(w http.ResponseWriter, r *http.Request) {
		if fail.Load() {
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}

		rendered(w, r)
	})

	hs, _ := ssrHarness(t, &inertia.SSRConfig{
		URL: srv.URL, Cache: cache.NewMemory(100),
		CacheWhen: func(_ *web.Ctx, p *inertia.Page) bool { return p.Component != "Admin" },
	})

	if body := hs.do(t, http.MethodGet, "/events/80", nil).Body.String(); !strings.Contains(body, csrMarker) {
		t.Fatalf("failed render not on the client: %s", body)
	}

	fail.Store(false)

	// The failure was not cached: the next visit renders on the server.
	if body := hs.do(t, http.MethodGet, "/events/80", nil).Body.String(); !strings.Contains(body, "<h1>Event</h1>") {
		t.Fatalf("render after a failure not server-side: %s", body)
	}

	hs.do(t, http.MethodGet, "/admin", nil)
	hs.do(t, http.MethodGet, "/admin", nil)

	if len(srv.paths) != 4 {
		t.Errorf("SSR requests = %v, want 4 (failure, render, Admin twice)", srv.paths)
	}
}

// TestSecurity_A01_SSRCacheNeverSharesPersonalisedRenders checks that the
// SSR cache key covers every prop: two users on the same URL with different
// shared props get their own render, never the other user's.
func TestSecurity_A01_SSRCacheNeverSharesPersonalisedRenders(t *testing.T) {
	t.Parallel()

	var srv *ssrServer

	srv = newSSRServer(t, func(w http.ResponseWriter, _ *http.Request) {
		srv.mu.Lock()
		props, _ := srv.pages[len(srv.pages)-1]["props"].(map[string]any)
		srv.mu.Unlock()

		user, _ := props["user"].(string)

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"head": []string{}, "body": "<p>hi " + user + "</p>"})
	})

	cfg := baseConfig()
	cfg.SSR = &inertia.SSRConfig{URL: srv.URL, Cache: cache.NewMemory(100)}

	hs := newHarness(t, cfg, func(r *router.Router) {
		r.Get("/me", func(c *web.Ctx) error {
			inertia.Share(c.Context(), "user", c.Query("u"))
			return inertia.Render(c, "Me", nil)
		})
	})

	for range 2 {
		for _, u := range []string{"ana", "bob"} {
			if body := hs.do(
				t,
				http.MethodGet,
				"/me?u="+u,
				nil,
			).Body.String(); !strings.Contains(
				body,
				"<p>hi "+u+"</p>",
			) {
				t.Fatalf("user %s got %s", u, body)
			}
		}
	}

	if len(srv.paths) != 2 {
		t.Errorf("SSR renders = %d, want 2 (one per user, then cached)", len(srv.paths))
	}
}
