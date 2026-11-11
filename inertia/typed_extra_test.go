package inertia_test

import (
	"context"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/AlexGalhardo/vukem/inertia"
	"github.com/AlexGalhardo/vukem/router"
	"github.com/AlexGalhardo/vukem/web"
)

type base struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

type recursive struct {
	*recursive
	A string `json:"a"`
}

func TestDefineRejectsMistakesThatHideProps(t *testing.T) {
	t.Parallel()

	tests := map[string]func(){
		"omitempty with an inertia tag": func() {
			inertia.Define[struct {
				A []int `json:"a,omitempty" inertia:"optional"`
			}]("X")
		},
		"omitzero with an inertia tag": func() {
			inertia.Define[struct {
				A int `json:"a,omitzero" inertia:"always"`
			}]("X")
		},
		"inertia tag on an embedded struct": func() {
			inertia.Define[struct {
				base `inertia:"defer"`
			}]("X")
		},
		"errors key": func() {
			inertia.Define[struct {
				Errors map[string]string `json:"errors"`
			}]("X")
		},
		"same depth duplicate through embedding": func() {
			type left struct{ Name string }

			type right struct{ Name string }

			inertia.Define[struct {
				left
				right
			}]("X")
		},
		"always with once": func() {
			inertia.Define[struct {
				A int `inertia:"always,once"`
			}]("X")
		},
		"merge with deepmerge": func() {
			inertia.Define[struct {
				A []int `inertia:"merge,deepmerge"`
			}]("X")
		},
		"optional with defer": func() {
			inertia.Define[struct {
				A int `inertia:"optional,defer"`
			}]("X")
		},
		"defer without group name": func() {
			inertia.Define[struct {
				A int `inertia:"defer="`
			}]("X")
		},
	}

	for name, fn := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			defer func() {
				if recover() == nil {
					t.Error("Define did not panic")
				}
			}()

			fn()
		})
	}
}

// The shallower field wins, as in encoding/json, and a self-embedding type
// does not overflow the stack.
func TestDefineEmbedding(t *testing.T) {
	t.Parallel()

	type outer struct {
		base
		Name string `json:"name"`
	}

	if _, err := inertia.Define[outer]("X").Info().Shape(); err != nil {
		t.Fatal(err)
	}

	pg := inertia.Define[outer]("Outer")
	hs := newHarness(t, baseConfig(), func(r *router.Router) {
		r.Get("/", func(c *web.Ctx) error {
			return pg.Render(c, outer{base: base{ID: 1, Name: "inner"}, Name: "outer"})
		})
	})

	got := page(t, hs.do(t, http.MethodGet, "/", xhr()))["props"]
	if diff := cmp.Diff(map[string]any{"errors": map[string]any{}, "id": 1.0, "name": "outer"}, got); diff != "" {
		t.Errorf("props (-want +got):\n%s", diff)
	}

	rec := inertia.Define[recursive]("Rec")
	_ = recursive{recursive: nil}
	if rec.Name() != "Rec" {
		t.Error("recursive type was not defined")
	}
}

// A nil embedded pointer drops its props, and the shape marks them optional.
func TestComponentNilEmbeddedPointer(t *testing.T) {
	t.Parallel()

	type props struct {
		*base
		Lazy func(context.Context) (int, error) `json:"lazy"`
		T    string                             `json:"t"`
	}

	pg := inertia.Define[props]("Emb")
	hs := newHarness(t, baseConfig(), func(r *router.Router) {
		r.Get("/nil", func(c *web.Ctx) error { return pg.Render(c, props{T: "x"}) })
		r.Get("/set", func(c *web.Ctx) error { return pg.Render(c, props{base: &base{ID: 2, Name: "n"}, T: "x"}) })
	})

	if diff := cmp.Diff(map[string]any{"errors": map[string]any{}, "t": "x"},
		page(t, hs.do(t, http.MethodGet, "/nil", xhr()))["props"]); diff != "" {
		t.Errorf("nil embedded props (-want +got):\n%s", diff)
	}

	if diff := cmp.Diff(map[string]any{"errors": map[string]any{}, "t": "x", "id": 2.0, "name": "n"},
		page(t, hs.do(t, http.MethodGet, "/set", xhr()))["props"]); diff != "" {
		t.Errorf("embedded props (-want +got):\n%s", diff)
	}

	shape, err := pg.Info().Shape()
	if err != nil {
		t.Fatal(err)
	}

	tags := map[string]string{}
	for f := range shape.Fields() {
		key, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		tags[key] = f.Tag.Get("json")
	}

	want := map[string]string{"id": "id,omitempty", "name": "name,omitempty", "lazy": "lazy,omitempty", "t": "t"}
	if diff := cmp.Diff(want, tags); diff != "" {
		t.Errorf("shape tags (-want +got):\n%s", diff)
	}
}

// A nil slice or map is an empty one on the wire, as the TypeScript type says.
func TestComponentNilCollectionsAreEmpty(t *testing.T) {
	t.Parallel()

	type props struct {
		List []string                                `json:"list"`
		Map  map[string]int                          `json:"map"`
		Lazy func(context.Context) ([]string, error) `json:"lazy"`
		Ptr  *[]string                               `json:"ptr"`
	}

	pg := inertia.Define[props]("Nil")
	hs := newHarness(t, baseConfig(), func(r *router.Router) {
		r.Get("/", func(c *web.Ctx) error {
			return pg.Render(c, props{Lazy: func(context.Context) ([]string, error) { return nil, nil }})
		})
	})

	want := map[string]any{
		"errors": map[string]any{}, "list": []any{}, "map": map[string]any{}, "lazy": []any{}, "ptr": nil,
	}
	if diff := cmp.Diff(want, page(t, hs.do(t, http.MethodGet, "/", xhr()))["props"]); diff != "" {
		t.Errorf("props (-want +got):\n%s", diff)
	}
}

// A typed page answers a partial reload with Partial-Except and an error bag
// like the page built from a map.
func TestComponentPartialExceptAndErrorBag(t *testing.T) {
	t.Parallel()

	type props struct {
		A string `json:"a"`
		B string `json:"b"`
		C string `json:"c" inertia:"always"`
	}

	pg := inertia.Define[props]("P")
	hs := newHarness(t, baseConfig(), func(r *router.Router) {
		r.Get("/typed", func(c *web.Ctx) error { return pg.Render(c, props{A: "a", B: "b", C: "c"}) })
		r.Get("/map", func(c *web.Ctx) error {
			return inertia.Render(c, "P", inertia.Props{"a": "a", "b": "b", "c": inertia.Always("c")})
		})
	})
	hs.sess.data[web.ErrorsKey] = map[string]any{"a": []any{"Bad."}}

	for _, headers := range []map[string]string{
		xhr("X-Inertia-Partial-Component", "P", "X-Inertia-Partial-Except", "a"),
		xhr("X-Inertia-Partial-Component", "P", "X-Inertia-Partial-Data", "a", "X-Inertia-Partial-Except", "a"),
		xhr("X-Inertia-Error-Bag", "bag"),
	} {
		typed := page(t, hs.do(t, http.MethodGet, "/typed", headers))
		plain := page(t, hs.do(t, http.MethodGet, "/map", headers))

		delete(typed, "url")
		delete(plain, "url")

		if diff := cmp.Diff(plain, typed); diff != "" {
			t.Errorf("headers %v: (-map +typed):\n%s", headers, diff)
		}
	}
}

func TestNewValidatesPagesAndCopiesThem(t *testing.T) {
	t.Parallel()

	if _, err := inertia.New(
		inertia.Config{Pages: []inertia.PageInfo{{Props: reflect.TypeFor[struct{}]()}}},
	); err == nil {
		t.Error("a page without a component name was accepted")
	}

	in, err := inertia.New(inertia.Config{Pages: []inertia.PageInfo{inertia.Define[struct{}]("Home").Info()}})
	if err != nil {
		t.Fatal(err)
	}

	in.Pages()[0].Component = "Changed"

	if got := in.Pages()[0].Component; got != "Home" {
		t.Errorf("Pages() exposes the adapter's own slice: component is now %q", got)
	}
}
