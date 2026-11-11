package view

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/AlexGalhardo/vukem/web"
)

type postsData struct {
	User  string
	Posts []string
}

func typedRun(t *testing.T, h web.HandlerFunc) *httptest.ResponseRecorder {
	t.Helper()

	s := &web.Services{Views: newEngine(t, views, false), Log: slog.New(slog.DiscardHandler)}
	rec := httptest.NewRecorder()
	web.NewCtx(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody), s).Serve(h)

	return rec
}

func TestPageRenderMatchesCtxRender(t *testing.T) {
	t.Parallel()

	page := Of[postsData]("posts/index", "post-list")
	data := postsData{Posts: []string{"a", "<b>"}}

	typed := typedRun(t, func(c *web.Ctx) error { return page.Render(c, data) })
	plain := typedRun(t, func(c *web.Ctx) error { return c.Render("posts/index", data) })

	if typed.Code != http.StatusOK || typed.Body.String() != plain.Body.String() || typed.Body.Len() == 0 {
		t.Errorf("typed = %d %q, plain = %q", typed.Code, typed.Body.String(), plain.Body.String())
	}

	frag := typedRun(t, func(c *web.Ctx) error { return page.Fragment(c, "post-list", data) })
	if got := frag.Body.String(); got != `<ul><li>a</li><li>&lt;b&gt;</li></ul>` {
		t.Errorf("Fragment = %q", got)
	}
}

func TestPageFragmentRejectsUndeclaredBlock(t *testing.T) {
	t.Parallel()

	page := Of[postsData]("posts/index", "post-list")

	var got error

	typedRun(t, func(c *web.Ctx) error {
		got = page.Fragment(c, "other", postsData{})
		return nil
	})

	if got == nil || !strings.Contains(got.Error(), `"other"`) {
		t.Errorf("Fragment(undeclared) = %v, want an error naming the block", got)
	}
}

func TestPageDecl(t *testing.T) {
	t.Parallel()

	blocks := []string{"a", "b"}
	d := Of[*postsData]("posts/index", blocks...).Decl()

	if d.Name != "posts/index" || d.Data != reflect.TypeFor[*postsData]() || !slices.Equal(d.Blocks, blocks) {
		t.Errorf("Decl() = %+v", d)
	}

	blocks[0] = "changed"

	if d.Blocks[0] != "a" {
		t.Error("Decl shares the caller's blocks slice")
	}
}

func TestEngineTrees(t *testing.T) {
	t.Parallel()

	e := newEngine(t, views, false)

	root, trees, err := e.Trees("posts/index")
	if err != nil {
		t.Fatal(err)
	}

	var names []string
	for n := range trees {
		names = append(names, n)
	}

	slices.Sort(names)

	// The page, its layout, the partials and components, and its own defines.
	for _, want := range []string{"layouts/app", "posts/index", "partials/nav", "title", "content", "post-list"} {
		if !slices.Contains(names, want) {
			t.Errorf("Trees names = %v, missing %q", names, want)
		}
	}

	if root != "layouts/app" {
		t.Errorf("root = %q, want layouts/app", root)
	}

	if diff := cmp.Diff("posts/index", trees["post-list"].ParseName); diff != "" {
		t.Errorf("define ParseName (-want +got):\n%s", diff)
	}

	plainRoot, _, err := e.Trees("plain")
	if err != nil || plainRoot != "plain" {
		t.Errorf("Trees(plain) root = %q, %v, want the page itself", plainRoot, err)
	}

	if _, _, err := e.Trees("missing"); err == nil {
		t.Error("Trees(missing) = nil error")
	}
}

func TestEngineExt(t *testing.T) {
	t.Parallel()

	if got := newEngine(t, views, false).Ext(); got != ".html" {
		t.Errorf("Ext() = %q, want .html", got)
	}
}
