package inertia_test

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/google/go-cmp/cmp"

	"github.com/AlexGalhardo/vukem/i18n"
	"github.com/AlexGalhardo/vukem/inertia"
	"github.com/AlexGalhardo/vukem/router"
	"github.com/AlexGalhardo/vukem/view"
	"github.com/AlexGalhardo/vukem/web"
)

type fakeSession struct {
	data      map[string]any
	reflashed bool
}

func (f *fakeSession) Get(k string) any    { return f.data[k] }
func (f *fakeSession) Put(k string, v any) { f.data[k] = v }
func (f *fakeSession) Forget(ks ...string) {
	for _, k := range ks {
		delete(f.data, k)
	}
}
func (f *fakeSession) Pull(k string) any     { v := f.data[k]; delete(f.data, k); return v }
func (f *fakeSession) Flash(k string, v any) { f.data[k] = v }
func (*fakeSession) Regenerate()             {}
func (*fakeSession) Invalidate()             {}
func (*fakeSession) ID() string              { return "" }
func (f *fakeSession) Reflash()              { f.reflashed = true }

type harness struct {
	h    http.Handler
	sess *fakeSession
}

func newHarness(t *testing.T, cfg inertia.Config, routes func(r *router.Router)) *harness {
	t.Helper()

	views, err := view.New(view.Config{FS: fstest.MapFS{
		"app.html": {
			Data: []byte(`{{/* layout: none */}}<html><head>{{.Head}}</head><body>{{.Inertia}}</body></html>`),
		},
	}})
	if err != nil {
		t.Fatal(err)
	}

	in, err := inertia.New(cfg)
	if err != nil {
		t.Fatal(err)
	}

	hs := &harness{sess: &fakeSession{data: map[string]any{}}}
	r := router.New(&web.Services{Views: views})
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			next.ServeHTTP(w, req.WithContext(web.WithSession(req.Context(), hs.sess)))
		})
	}, in.Middleware)
	routes(r)

	if hs.h, err = r.Handler(); err != nil {
		t.Fatal(err)
	}

	return hs
}

func (hs *harness) do(t *testing.T, method, target string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequestWithContext(t.Context(), method, target, http.NoBody)
	req.Host = "example.com"
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	rec := httptest.NewRecorder()
	hs.h.ServeHTTP(rec, req)

	return rec
}

func xhr(extra ...string) map[string]string {
	h := map[string]string{"X-Inertia": "true", "X-Requested-With": "XMLHttpRequest", "X-Inertia-Version": "v1"}
	for i := 0; i+1 < len(extra); i += 2 {
		h[extra[i]] = extra[i+1]
	}

	return h
}

func page(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()

	var p map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatalf("body %q is not a page object: %v", rec.Body.String(), err)
	}

	return p
}

func baseConfig() inertia.Config { return inertia.Config{Version: func() string { return "v1" }} }

func eventRoutes(r *router.Router) {
	r.Get("/events/{id}", func(c *web.Ctx) error {
		return inertia.Render(c, "Event", inertia.Props{
			"event": map[string]any{"id": 80, "title": "</script><b>"},
		})
	})
}

func TestInitialVisitHTML(t *testing.T) {
	t.Parallel()

	rec := newHarness(t, baseConfig(), eventRoutes).do(t, http.MethodGet, "/events/80", nil)

	title, err := json.Marshal("</script><b>") // json escapes <, > and &
	if err != nil {
		t.Fatal(err)
	}

	escapedTitle := strings.ReplaceAll(string(title), "/", `\/`)
	want := `<html><head></head><body><script data-page="app" type="application/json">` +
		`{"component":"Event","props":{"errors":{},"event":{"id":80,"title":` + escapedTitle + `}},` +
		`"url":"\/events\/80","version":"v1"}</script><div id="app"></div></body></html>`

	if diff := cmp.Diff(want, rec.Body.String()); diff != "" || rec.Code != http.StatusOK {
		t.Errorf("GET /events/80 = %d (-want +got):\n%s", rec.Code, diff)
	}

	if got := rec.Header().Get("Vary"); got != "X-Inertia" {
		t.Errorf("Vary = %q, want X-Inertia", got)
	}

	if rec.Header().Get("X-Inertia") != "" {
		t.Error("HTML response carries X-Inertia")
	}
}

// The client finds the page script by data-page="<id>" and mounts on
// #<id>, so a custom RootID must name both.
func TestInitialVisitCustomRootID(t *testing.T) {
	t.Parallel()

	cfg := baseConfig()
	cfg.RootID = "root"

	body := newHarness(t, cfg, eventRoutes).do(t, http.MethodGet, "/events/80", nil).Body.String()

	if !strings.Contains(body, `<script data-page="root" type="application/json">`) ||
		!strings.Contains(body, `</script><div id="root"></div>`) || strings.Contains(body, `"app"`) {
		t.Errorf("body = %s, want data-page and mount id \"root\"", body)
	}
}

func TestInertiaVisitJSON(t *testing.T) {
	t.Parallel()

	rec := newHarness(t, baseConfig(), eventRoutes).do(t, http.MethodGet, "/events/80?tab=info", xhr())

	if rec.Code != http.StatusOK || rec.Header().Get("X-Inertia") != "true" ||
		!strings.HasPrefix(
			rec.Header().Get("Content-Type"),
			"application/json",
		) || rec.Header().Get("Vary") != "X-Inertia" {
		t.Fatalf("response = %d %v", rec.Code, rec.Header())
	}

	want := map[string]any{
		"component": "Event",
		"props": map[string]any{
			"errors": map[string]any{},
			"event":  map[string]any{"id": 80.0, "title": "</script><b>"},
		},
		"url":     "/events/80?tab=info",
		"version": "v1",
	}
	if diff := cmp.Diff(want, page(t, rec)); diff != "" {
		t.Errorf("page mismatch (-want +got):\n%s", diff)
	}
}

func TestVersionMismatch(t *testing.T) {
	t.Parallel()

	called := 0
	hs := newHarness(t, baseConfig(), func(r *router.Router) {
		r.Get("/events", func(c *web.Ctx) error { called++; return inertia.Render(c, "Events", nil) })
		r.Post("/events", func(c *web.Ctx) error { called++; return c.Redirect("/events") })
	})

	rec := hs.do(t, http.MethodGet, "/events?page=2", xhr("X-Inertia-Version", "old"))
	if rec.Code != http.StatusConflict ||
		rec.Header().Get("X-Inertia-Location") != "http://example.com/events?page=2" ||
		rec.Header().Get("X-Inertia-Version") != "v1" ||
		rec.Header().Get("X-Inertia") != "" {
		t.Fatalf("GET with stale version = %d %v", rec.Code, rec.Header())
	}

	if called != 0 || !hs.sess.reflashed {
		t.Errorf("handler called %d times, reflashed %v; want 0, true", called, hs.sess.reflashed)
	}

	if rec := hs.do(t, http.MethodPost, "/events", xhr("X-Inertia-Version", "old")); rec.Code != http.StatusSeeOther {
		t.Errorf("POST with stale version = %d, want 303", rec.Code)
	}

	if rec := hs.do(
		t,
		http.MethodGet,
		"/events",
		map[string]string{"X-Inertia-Version": "old"},
	); rec.Code != http.StatusOK {
		t.Errorf("non-Inertia GET with stale version = %d, want 200", rec.Code)
	}
}

// Behind a TLS-terminating proxy the version reload must stay on https.
func TestVersionMismatchBehindProxy(t *testing.T) {
	t.Parallel()

	_, proxies, _ := net.ParseCIDR("10.0.0.0/8")

	in, err := inertia.New(baseConfig())
	if err != nil {
		t.Fatal(err)
	}

	for _, tt := range []struct {
		name, remote, want string
	}{
		{"trusted proxy", "10.0.0.1:1", "https://example.com/events"},
		{"untrusted peer", "198.51.100.7:1", "http://example.com/events"},
	} {
		r := router.New(&web.Services{TrustedProxies: []*net.IPNet{proxies}, TrustForwardedProto: true})
		r.Use(in.Middleware)
		r.Get("/events", func(c *web.Ctx) error { return inertia.Render(c, "Events", nil) })

		h, err := r.Handler()
		if err != nil {
			t.Fatal(err)
		}

		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/events", http.NoBody)
		req.Host, req.RemoteAddr = "example.com", tt.remote
		req.Header.Set("X-Forwarded-Proto", "https")

		for k, v := range xhr("X-Inertia-Version", "old") {
			req.Header.Set(k, v)
		}

		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		if got := rec.Header().Get("X-Inertia-Location"); rec.Code != http.StatusConflict || got != tt.want {
			t.Errorf("%s: %d X-Inertia-Location = %q, want 409 %q", tt.name, rec.Code, got, tt.want)
		}
	}
}

func TestRedirects(t *testing.T) {
	t.Parallel()

	hs := newHarness(t, baseConfig(), func(r *router.Router) {
		found := func(c *web.Ctx) error {
			http.Redirect(c.Writer(), c.Request(), "/users", http.StatusFound)
			return nil
		}
		r.Put("/users/1", found)
		r.Patch("/users/1", found)
		r.Delete("/users/1", found)
		r.Get("/found", found)
		r.Post("/fragment", func(c *web.Ctx) error { return c.Redirect("/docs#install") })
		r.Get("/away", func(c *web.Ctx) error { return inertia.Location(c, "https://github.com/login") })
	})

	tests := []struct {
		name, method, target string
		headers              map[string]string
		status               int
		header, value        string
	}{
		{"PUT 302 becomes 303", http.MethodPut, "/users/1", xhr(), http.StatusSeeOther, "Location", "/users"},
		{"PATCH 302 becomes 303", http.MethodPatch, "/users/1", xhr(), http.StatusSeeOther, "Location", "/users"},
		{"DELETE 302 becomes 303", http.MethodDelete, "/users/1", xhr(), http.StatusSeeOther, "Location", "/users"},
		{"GET 302 stays", http.MethodGet, "/found", xhr(), http.StatusFound, "Location", "/users"},
		{
			"fragment redirect",
			http.MethodPost,
			"/fragment",
			xhr(),
			http.StatusConflict,
			"X-Inertia-Redirect",
			"/docs#install",
		},
		{
			"fragment redirect on prefetch", http.MethodPost, "/fragment", xhr("Purpose", "prefetch"),
			http.StatusSeeOther, "Location", "/docs#install",
		},
		{
			"fragment redirect outside Inertia",
			http.MethodPost,
			"/fragment",
			nil,
			http.StatusSeeOther,
			"Location",
			"/docs#install",
		},
		{
			"external location",
			http.MethodGet,
			"/away",
			xhr(),
			http.StatusConflict,
			"X-Inertia-Location",
			"https://github.com/login",
		},
		{
			"external location outside Inertia",
			http.MethodGet,
			"/away",
			nil,
			http.StatusFound,
			"Location",
			"https://github.com/login",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rec := hs.do(t, tt.method, tt.target, tt.headers)
			if rec.Code != tt.status || rec.Header().Get(tt.header) != tt.value {
				t.Errorf("%s %s = %d %s=%q, want %d %q", tt.method, tt.target, rec.Code, tt.header,
					rec.Header().Get(tt.header), tt.status, tt.value)
			}

			if rec.Code == http.StatusConflict && (rec.Header().Get("Location") != "" || rec.Body.Len() != 0) {
				t.Errorf("409 carries Location %q or body %q", rec.Header().Get("Location"), rec.Body.String())
			}
		})
	}
}

func TestValidationErrors(t *testing.T) {
	t.Parallel()

	hs := newHarness(t, baseConfig(), func(r *router.Router) {
		r.Get("/users/create", func(c *web.Ctx) error { return inertia.Render(c, "Users/Create", nil) })
	})
	hs.sess.data[web.ErrorsKey] = map[string]any{"name": []any{"Too short.", "Letters only."}, "email": []any{"Taken."}}

	got := page(t, hs.do(t, http.MethodGet, "/users/create", xhr()))["props"]
	want := map[string]any{"errors": map[string]any{"name": "Too short.", "email": "Taken."}}

	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("errors mismatch (-want +got):\n%s", diff)
	}

	got = page(t, hs.do(t, http.MethodGet, "/users/create", xhr("X-Inertia-Error-Bag", "createUser")))["props"]
	want = map[string]any{
		"errors": map[string]any{"createUser": map[string]any{"name": "Too short.", "email": "Taken."}},
	}

	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("error bag mismatch (-want +got):\n%s", diff)
	}
}

func TestSharedProps(t *testing.T) {
	t.Parallel()

	cfg := baseConfig()
	cfg.Shared = inertia.Props{
		"app":  "Vukem",
		"user": func(context.Context) (any, error) { return "global", nil },
	}

	hs := newHarness(t, cfg, func(r *router.Router) {
		r.Get("/", func(c *web.Ctx) error {
			inertia.Share(c.Context(), "user", map[string]any{"name": "Ana"})
			inertia.Share(c.Context(), "locale", "pt-BR")

			return inertia.Render(c, "Home", inertia.Props{"app": "Page wins"})
		})
	})

	p := page(t, hs.do(t, http.MethodGet, "/", xhr()))
	want := map[string]any{
		"errors": map[string]any{}, "app": "Page wins", "user": map[string]any{"name": "Ana"}, "locale": "pt-BR",
	}

	if diff := cmp.Diff(want, p["props"]); diff != "" {
		t.Errorf("props mismatch (-want +got):\n%s", diff)
	}

	if diff := cmp.Diff([]any{"app", "locale", "user"}, p["sharedProps"]); diff != "" {
		t.Errorf("sharedProps mismatch (-want +got):\n%s", diff)
	}
}

func TestLazyPropsAndErrors(t *testing.T) {
	t.Parallel()

	boom := errors.New("boom")
	hs := newHarness(t, baseConfig(), func(r *router.Router) {
		r.Get("/ok", func(c *web.Ctx) error {
			return inertia.Render(c, "Ok", inertia.Props{"n": func(context.Context) (any, error) { return 3, nil }})
		})
		r.Get("/fail", func(c *web.Ctx) error {
			err := inertia.Render(
				c,
				"Fail",
				inertia.Props{"n": func(context.Context) (any, error) { return nil, boom }},
			)
			if !errors.Is(err, boom) {
				t.Errorf("Render() error = %v, want boom", err)
			}

			return err
		})
	})

	if got := page(t, hs.do(t, http.MethodGet, "/ok", xhr()))["props"].(map[string]any)["n"]; got != 3.0 {
		t.Errorf("lazy prop = %v, want 3", got)
	}

	if rec := hs.do(t, http.MethodGet, "/fail", xhr()); rec.Code != http.StatusInternalServerError {
		t.Errorf("failing prop status = %d, want 500", rec.Code)
	}
}

func TestPreserveFragment(t *testing.T) {
	t.Parallel()

	hs := newHarness(t, baseConfig(), func(r *router.Router) {
		r.Post("/save", func(c *web.Ctx) error {
			inertia.PreserveFragment(c)
			return c.Redirect("/edit")
		})
		r.Get("/edit", func(c *web.Ctx) error { return inertia.Render(c, "Edit", nil) })
	})

	hs.do(t, http.MethodPost, "/save", xhr())

	if got := page(t, hs.do(t, http.MethodGet, "/edit", xhr()))["preserveFragment"]; got != true {
		t.Errorf("preserveFragment = %v, want true", got)
	}
}

func TestRenderStatus(t *testing.T) {
	t.Parallel()

	hs := newHarness(t, baseConfig(), func(r *router.Router) {
		r.Get("/status/{code}", func(c *web.Ctx) error {
			code, err := strconv.Atoi(c.Param("code"))
			if err != nil {
				return err
			}

			return inertia.RenderStatus(c, code, "Errors/Status", inertia.Props{"code": code})
		})
	})

	tests := []struct {
		code    int
		inertia bool
		want    int
	}{
		{http.StatusGone, true, http.StatusGone},
		{http.StatusGone, false, http.StatusGone},
		{http.StatusNotFound, true, http.StatusNotFound},
		{http.StatusUnprocessableEntity, false, http.StatusUnprocessableEntity},
		{http.StatusServiceUnavailable, true, http.StatusServiceUnavailable},
		{http.StatusCreated, true, http.StatusCreated},
		// Statuses the protocol or HTTP gives another meaning are refused.
		{http.StatusFound, true, http.StatusInternalServerError},
		{http.StatusConflict, true, http.StatusInternalServerError},
		{http.StatusNoContent, false, http.StatusInternalServerError},
		{http.StatusNotModified, false, http.StatusInternalServerError},
		{http.StatusContinue, true, http.StatusInternalServerError},
		{600, true, http.StatusInternalServerError},
	}

	for _, tt := range tests {
		var headers map[string]string
		if tt.inertia {
			headers = xhr()
		}

		target := "/status/" + strconv.Itoa(tt.code)
		rec := hs.do(t, http.MethodGet, target, headers)

		if rec.Code != tt.want {
			t.Errorf("GET %s (inertia %t) = %d, want %d", target, tt.inertia, rec.Code, tt.want)
			continue
		}

		if tt.want == http.StatusInternalServerError {
			continue
		}

		if !tt.inertia {
			if !strings.Contains(rec.Body.String(), `"component":"Errors\/Status"`) {
				t.Errorf("GET %s HTML body = %q, want the page script", target, rec.Body.String())
			}

			continue
		}

		if rec.Header().Get("X-Inertia") != "true" {
			t.Errorf("GET %s X-Inertia = %q, want true", target, rec.Header().Get("X-Inertia"))
		}

		if p := page(t, rec); p["component"] != "Errors/Status" {
			t.Errorf("GET %s component = %v, want Errors/Status", target, p["component"])
		}
	}
}

func TestRenderWithoutMiddleware(t *testing.T) {
	t.Parallel()

	r := router.New(nil)
	r.Get("/", func(c *web.Ctx) error { return inertia.Render(c, "Home", nil) })

	h, err := r.Handler()
	if err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody))

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("Render without middleware = %d, want 500", rec.Code)
	}
}

func TestSecurity_A05_InertiaPageScriptCannotBreakOut(t *testing.T) {
	t.Parallel()

	hs := newHarness(t, baseConfig(), func(r *router.Router) {
		r.Get("/", func(c *web.Ctx) error {
			return inertia.Render(c, "Home", inertia.Props{"x": `</script><script>alert(1)</script><!--`})
		})
	})

	body := hs.do(t, http.MethodGet, "/", nil).Body.String()
	if strings.Count(body, "</script>") != 1 || strings.Contains(body, "<script>alert") ||
		strings.Contains(body, "<!--") {
		t.Errorf("page script leaked markup: %s", body)
	}
}

func TestI18nSharesLocaleAndMessages(t *testing.T) {
	t.Parallel()

	bundle, err := i18n.New(i18n.Options{Default: "en"})
	if err != nil {
		t.Fatal(err)
	}

	_ = bundle.Add("en", map[string]any{"nav": map[string]any{"home": "Home"}, "server.only": "x"})
	_ = bundle.Add("pt-BR", map[string]any{"nav": map[string]any{"home": "Início"}})

	hs := newHarness(t, baseConfig(), func(r *router.Router) {
		r.Get("/", func(c *web.Ctx) error { return inertia.Render(c, "Home", nil) }).Use(
			func(next http.Handler) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
					ctx := i18n.WithLocale(i18n.WithBundle(req.Context(), bundle), "pt-BR")
					next.ServeHTTP(w, req.WithContext(ctx))
				})
			},
			inertia.I18n("nav."),
		)
	})

	p := page(t, hs.do(t, http.MethodGet, "/", xhr()))
	props, _ := p["props"].(map[string]any)

	if props["locale"] != "pt-BR" {
		t.Errorf("locale prop = %v, want pt-BR", props["locale"])
	}

	if diff := cmp.Diff([]any{"en", "pt-BR"}, props["locales"]); diff != "" {
		t.Errorf("locales prop mismatch (-want +got):\n%s", diff)
	}

	if diff := cmp.Diff(map[string]any{"nav.home": "Início"}, props["messages"]); diff != "" {
		t.Errorf("messages prop mismatch (-want +got):\n%s", diff)
	}

	// Messages are a once prop keyed by locale: a client that has them skips them.
	again := page(t, hs.do(t, http.MethodGet, "/", xhr("X-Inertia-Except-Once-Props", "i18n:pt-BR")))
	if props, _ := again["props"].(map[string]any); props["messages"] != nil {
		t.Errorf("messages resent to a client that has them: %v", props["messages"])
	}
}
