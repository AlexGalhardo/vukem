package inertia_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/AlexGalhardo/vukem/inertia"
	"github.com/AlexGalhardo/vukem/router"
	"github.com/AlexGalhardo/vukem/web"
)

type audit struct {
	By string `json:"by"`
}

type dashProps struct {
	audit
	Title   string                                  `json:"title"`
	Stats   *int                                    `json:"stats"             inertia:"optional"`
	Charts  []int                                   `json:"charts"            inertia:"defer=charts"`
	Feed    []string                                `json:"feed"              inertia:"merge"`
	Banner  string                                  `json:"banner"            inertia:"once"`
	Count   func(context.Context) (int, error)      `json:"count"`
	Slow    func(context.Context) ([]string, error) `json:"slow"              inertia:"defer,merge"`
	Flash   string                                  `json:"flash"             inertia:"always"`
	Hidden  string                                  `json:"-"`
	Skipped string                                  `json:"skipped,omitempty"`
}

func TestDefinePanicsOnBadProps(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		fn   func()
	}{
		{"not a struct", func() { inertia.Define[map[string]any]("X") }},
		{"pointer", func() { inertia.Define[*struct{}]("X") }},
		{"unknown option", func() {
			inertia.Define[struct {
				A string `inertia:"bogus"`
			}]("X")
		}},
		{"optional with merge", func() {
			inertia.Define[struct {
				A string `inertia:"optional,merge"`
			}]("X")
		}},
		{"two kinds", func() {
			inertia.Define[struct {
				A string `inertia:"defer,optional"`
			}]("X")
		}},
		{"bad func prop", func() {
			inertia.Define[struct {
				A func() string
			}]("X")
		}},
		{"no name", func() { inertia.Define[struct{}]("") }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			defer func() {
				if recover() == nil {
					t.Error("Define did not panic")
				}
			}()

			tt.fn()
		})
	}
}

func TestComponentRenderMatchesMapRender(t *testing.T) {
	t.Parallel()

	count := 7
	dash := inertia.Define[dashProps]("Dash")
	slow := func(context.Context) ([]string, error) { return []string{"a"}, nil }

	hs := newHarness(t, baseConfig(), func(r *router.Router) {
		r.Get("/typed", func(c *web.Ctx) error {
			return dash.Render(c, dashProps{
				By:      "ana",
				Title:   "Hi",
				Stats:   &count,
				Charts:  []int{1, 2},
				Feed:    []string{"x"},
				Banner:  "b",
				Count:   func(context.Context) (int, error) { return 3, nil },
				Slow:    slow,
				Flash:   "ok",
				Hidden:  "secret",
				Skipped: "",
			})
		})
		r.Get("/map", func(c *web.Ctx) error {
			return inertia.Render(c, "Dash", inertia.Props{
				"by":     "ana",
				"title":  "Hi",
				"stats":  inertia.Optional(&count),
				"charts": inertia.Defer([]int{1, 2}).Group("charts"),
				"feed":   inertia.Merge([]string{"x"}),
				"banner": inertia.Once("b"),
				"count":  func(context.Context) (any, error) { return 3, nil },
				"slow":   inertia.Defer(func(context.Context) (any, error) { return []string{"a"}, nil }).Merge(),
				"flash":  inertia.Always("ok"),
			})
		})
	})

	for name, headers := range map[string]map[string]string{
		"first visit":     xhr(),
		"partial reload":  xhr("X-Inertia-Partial-Component", "Dash", "X-Inertia-Partial-Data", "stats,charts,slow"),
		"non-inertia GET": nil,
	} {
		typed := hs.do(t, http.MethodGet, "/typed", headers)
		plain := hs.do(t, http.MethodGet, "/map", headers)

		if typed.Code != plain.Code {
			t.Fatalf("%s: status %d, want %d", name, typed.Code, plain.Code)
		}

		if name == "non-inertia GET" {
			continue
		}

		pp, pt := page(t, plain), page(t, typed)
		delete(pp, "url")
		delete(pt, "url")

		if diff := cmp.Diff(pp, pt); diff != "" {
			t.Errorf("%s: typed page differs from the map page (-map +typed):\n%s", name, diff)
		}
	}
}

func TestComponentLazyFuncOnlyWhenSent(t *testing.T) {
	t.Parallel()

	type props struct {
		Always func(context.Context) (int, error) `json:"always"`
		Opt    func(context.Context) (int, error) `json:"opt"    inertia:"optional"`
	}

	var always, opt int

	pg := inertia.Define[props]("Lazy")
	hs := newHarness(t, baseConfig(), func(r *router.Router) {
		r.Get("/", func(c *web.Ctx) error {
			return pg.Render(c, props{
				Always: func(context.Context) (int, error) { always++; return 1, nil },
				Opt:    func(context.Context) (int, error) { opt++; return 2, nil },
			})
		})
	})

	hs.do(t, http.MethodGet, "/", xhr())

	if always != 1 || opt != 0 {
		t.Errorf("calls on a full visit = %d, %d; want 1, 0", always, opt)
	}

	hs.do(t, http.MethodGet, "/", xhr("X-Inertia-Partial-Component", "Lazy", "X-Inertia-Partial-Data", "opt"))

	if opt != 1 {
		t.Errorf("optional func called %d times on a partial reload, want 1", opt)
	}
}

func TestComponentInfoAndShape(t *testing.T) {
	t.Parallel()

	dash := inertia.Define[dashProps]("Dash")

	if dash.Name() != "Dash" || dash.Info().Component != "Dash" || dash.Info().Props != reflect.TypeFor[dashProps]() {
		t.Errorf("Info() = %+v", dash.Info())
	}

	shape, err := dash.Info().Shape()
	if err != nil {
		t.Fatal(err)
	}

	got := map[string]string{}
	for f := range shape.Fields() {
		got[f.Tag.Get("json")] = f.Type.String()
	}

	want := map[string]string{
		"by": "string", "title": "string", "stats,omitempty": "*int", "charts,omitempty": "[]int",
		"feed": "[]string", "banner": "string", "count,omitempty": "int", "slow,omitempty": "[]string",
		"flash": "string", "skipped,omitempty": "string",
	}

	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("shape (-want +got):\n%s", diff)
	}
}

type sharedProps struct {
	App  string `json:"app"`
	User *audit `json:"user"`
}

func TestNewRejectsBadTypedConfig(t *testing.T) {
	t.Parallel()

	page := inertia.Define[struct{}]("Home").Info()

	tests := map[string]inertia.Config{
		"duplicate page":         {Pages: []inertia.PageInfo{page, page}},
		"shared type not struct": {SharedType: reflect.TypeFor[int]()},
		"shared key outside type": {
			SharedType: reflect.TypeFor[sharedProps](),
			Shared:     inertia.Props{"app": "x", "extra": 1},
		},
		"bad page": {Pages: []inertia.PageInfo{{Component: "X", Props: reflect.TypeFor[int]()}}},
	}

	for name, cfg := range tests {
		if _, err := inertia.New(cfg); err == nil {
			t.Errorf("%s: New accepted the config", name)
		}
	}
}

// A key shared with Share that SharedType does not declare is refused, not sent.
func TestSecurity_A01_InertiaSharedTypeFailsClosed(t *testing.T) {
	t.Parallel()

	cfg := baseConfig()
	cfg.SharedType = reflect.TypeFor[sharedProps]()
	cfg.Shared = inertia.Props{"app": "Vukem"}

	hs := newHarness(t, cfg, func(r *router.Router) {
		r.Get("/ok", func(c *web.Ctx) error {
			inertia.Share(c.Context(), "user", map[string]any{"by": "ana"})
			inertia.Share(c.Context(), "user.by", "ana")

			return inertia.Render(c, "Home", nil)
		})
		r.Get("/leak", func(c *web.Ctx) error {
			inertia.Share(c.Context(), "apiToken", "secret")

			return inertia.Render(c, "Home", nil)
		})
	})

	if rec := hs.do(t, http.MethodGet, "/ok", xhr()); rec.Code != http.StatusOK {
		t.Errorf("declared shared keys: status %d, want 200", rec.Code)
	}

	rec := hs.do(t, http.MethodGet, "/leak", xhr())
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("undeclared shared key: status %d, want 500", rec.Code)
	}

	if body := rec.Body.String(); strings.Contains(body, "secret") || strings.Contains(body, "apiToken") {
		t.Errorf("the response carries the undeclared shared value: %s", body)
	}
}

func BenchmarkComponentRender(b *testing.B) {
	type props struct {
		Title string `json:"title"`
		N     int    `json:"n"`
		Opt   string `json:"opt"   inertia:"optional"`
	}

	pg := inertia.Define[props]("Bench")
	in, _ := inertia.New(baseConfig())
	r := router.New(&web.Services{})
	r.Use(in.Middleware)
	r.Get("/typed", func(c *web.Ctx) error { return pg.Render(c, props{Title: "t", N: 1}) })
	r.Get("/map", func(c *web.Ctx) error {
		return inertia.Render(c, "Bench", inertia.Props{"title": "t", "n": 1, "opt": inertia.Optional("")})
	})

	h, err := r.Handler()
	if err != nil {
		b.Fatal(err)
	}

	for _, path := range []string{"/typed", "/map"} {
		b.Run(path, func(b *testing.B) {
			for range b.N {
				req := httptest.NewRequestWithContext(b.Context(), http.MethodGet, path, http.NoBody)
				for k, v := range xhr() {
					req.Header.Set(k, v)
				}

				h.ServeHTTP(httptest.NewRecorder(), req)
			}
		})
	}
}
