package viewcheck

import (
	"fmt"
	"maps"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/google/go-cmp/cmp"

	"github.com/AlexGalhardo/vukem/router"
	"github.com/AlexGalhardo/vukem/view"
)

type User struct{ Name string }

type BelongsTo struct{}

func (BelongsTo) Get() (*User, error) { return nil, nil }

type Base struct {
	ID      int64
	Created string
}

type Post struct {
	Base

	Title string
	User  BelongsTo
	Tags  []string
	Meta  map[string]any
}

func (*Post) Slug() string { return "" }

type indexData struct {
	Posts  []Post
	Search string
	Labels map[string]string
	Meta   map[string]any
	Any    any
	Page   *Post
	Grid   [][]Post
	ByTag  map[string][]Post
}

type urls struct{}

func (urls) URL(string, ...any) (string, error) { return "/", nil }

var routes = []router.RouteInfo{
	{Name: "posts.index", Path: "/posts"},
	{Name: "posts.create", Path: "/posts/create"},
	{Name: "posts.show", Path: "/posts/{post}"},
	{Name: "posts.destroy", Path: "/posts/{post}"},
	{Name: "files.show", Path: "/files/{path...}"},
	{Name: "home", Path: "/{$}"},
}

const layout = `<html><body>{{block "content" .}}{{end}}</body></html>`

// blogIndex is a faithful copy of examples/blog/resources/views/posts/index.html.
const blogIndex = `{{define "title"}}Posts{{end}}
{{define "content"}}
<div class="toolbar">
	<h1>Posts</h1>
	<a class="button" href="{{route "posts.create"}}">New post</a>
</div>

<input type="search" name="q" value="{{.Search}}" placeholder="Search posts" aria-label="Search posts"
	hx-get="{{route "posts.index"}}" hx-trigger="input changed delay:300ms, search" hx-target="#post-list">

{{block "post-list" .}}
<ul id="post-list" class="list">
	{{range .Posts}}
	<li>
		<a href="{{route "posts.show" "post" .ID}}">{{.Title}}</a>
		{{with .User.Get}}<span class="empty">{{.Name}}</span>{{end}}
		{{if can "delete" .}}<button type="button" class="link danger" hx-delete="{{route "posts.destroy" "post" .ID}}"
			hx-target="closest li" hx-swap="outerHTML" hx-confirm="Delete this post?">Delete</button>{{end}}
	</li>
	{{else}}
	<li class="empty">No posts yet.</li>
	{{end}}
</ul>
{{end}}
{{end}}
`

// want is a problem located by the first occurrence of at in file.
type want struct {
	file, at, code, msg string
	warning             bool
}

type tc struct {
	name   string
	files  map[string]string
	data   view.Decl
	strict bool
	want   []want
}

func locate(t *testing.T, src, at string) (line, col int) {
	t.Helper()

	i := strings.Index(src, at)
	if i < 0 {
		t.Fatalf("marker %q not in the template", at)
	}

	line = strings.Count(src[:i], "\n") + 1
	col = i - strings.LastIndex(src[:i], "\n")

	return line, col
}

func fmtProblem(file string, line, col int, code, msg string, warn bool) string {
	return fmt.Sprintf("%s:%d:%d: %s: %s warning=%v", file, line, col, code, msg, warn)
}

func run(t *testing.T, c tc) {
	t.Helper()

	files := map[string]string{"layouts/app": layout, "partials/any": `{{.Whatever}}`}
	maps.Copy(files, c.files)

	fsys := fstest.MapFS{}
	for name, src := range files {
		fsys[name+".html"] = &fstest.MapFile{Data: []byte(src)}
	}

	eng, err := view.New(view.Config{FS: fsys, URLs: urls{}})
	if err != nil {
		t.Fatal(err)
	}

	got := Check(Options{
		Engine: eng, Decls: []view.Decl{c.data}, Routes: routes, Funcs: eng.FuncTypes(), Ext: ".html", Strict: c.strict,
	})

	var gotS, wantS []string

	for _, p := range got {
		gotS = append(gotS, fmtProblem(p.File, p.Line, p.Col, p.Code, p.Message, p.Warning))
	}

	for _, w := range c.want {
		line, col := 1, 1
		if w.at != "" {
			line, col = locate(t, files[strings.TrimSuffix(w.file, ".html")], w.at)
		}

		wantS = append(wantS, fmtProblem(w.file, line, col, w.code, w.msg, w.warning))
	}

	if diff := cmp.Diff(wantS, gotS); diff != "" {
		t.Errorf("problems (-want +got):\n%s", diff)
	}
}

func decl(name string, blocks ...string) view.Decl {
	return view.Of[indexData](name, blocks...).Decl()
}

func page(body string) map[string]string {
	return map[string]string{"p": `{{define "content"}}` + body + `{{end}}`}
}

func runAll(t *testing.T, cases []tc) {
	t.Helper()

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			run(t, c)
		})
	}
}

func TestCheck(t *testing.T) {
	t.Parallel()

	t.Run("types", func(t *testing.T) {
		t.Parallel()

		runAll(t, []tc{
			{name: "blog posts/index has no problems", files: map[string]string{"p": blogIndex}, data: decl("p")},
			{
				name: "blog posts/index with planted errors",
				files: map[string]string{"p": strings.NewReplacer(
					`{{.Title}}</a>`, `{{.Titel}}</a>`,
					`hx-target="#post-list"`, `hx-target="#post-lst"`,
					`route "posts.show"`, `route "posts.sho"`,
				).Replace(blogIndex)},
				data: decl("p"),
				want: []want{
					{
						"p.html",
						"#post-lst",
						"V005",
						`hx-target "#post-lst": no element with that id in this view`,
						true,
					},
					{"p.html", `"posts.sho"`, "V003", `route "posts.sho" is not registered`, false},
					{"p.html", ".Titel", "V002", `field or method "Titel" not found in viewcheck.Post`, false},
				},
			},
			{
				name:  "fields and embedded fields",
				files: page(`{{.Search}}{{range .Posts}}{{.ID}}{{.Title}}{{.Created}}{{end}}`), data: decl("p"),
			},
			{
				name:  "unknown field",
				files: page(`{{.Titel}}`),
				data:  decl("p"),
				want: []want{
					{"p.html", ".Titel", "V002", `field or method "Titel" not found in viewcheck.indexData`, false},
				},
			},
			{
				name:  "unknown field inside range",
				files: page(`{{range .Posts}}{{.Titel}}{{end}}`),
				data:  decl("p"),
				want: []want{
					{"p.html", ".Titel", "V002", `field or method "Titel" not found in viewcheck.Post`, false},
				},
			},
			{
				name:  "unexported name is not a field",
				files: page(`{{.title}}`),
				data:  decl("p"),
				want: []want{
					{"p.html", ".title", "V002", `field or method "title" not found in viewcheck.indexData`, false},
				},
			},
			{name: "method with two results", files: page(`{{with .Page.User.Get}}{{.Name}}{{end}}`), data: decl("p")},
			{
				name:  "field of the method result",
				files: page(`{{with .Page.User.Get}}{{.Nme}}{{end}}`),
				data:  decl("p"),
				want:  []want{{"p.html", ".Nme", "V002", `field or method "Nme" not found in viewcheck.User`, false}},
			},
			{
				name:  "pointer receiver method on a value",
				files: page(`{{range .Posts}}{{.Slug}}{{end}}`),
				data:  decl("p"),
			},
			{
				name:  "range else keeps the outer dot",
				files: page(`{{range .Posts}}{{.Title}}{{else}}{{.Search}}{{.Title}}{{end}}`),
				data:  decl("p"),
				want: []want{
					{
						"p.html",
						".Title}}{{end}}",
						"V002",
						`field or method "Title" not found in viewcheck.indexData`,
						false,
					},
				},
			},
			{
				name:  "with else keeps the outer dot",
				files: page(`{{with .Page}}{{.Title}}{{else}}{{.Search}}{{end}}`), data: decl("p"),
			},
			{
				name: "variables",
				files: page(
					`{{$p := .Page}}{{$p.Title}}{{range $i, $e := .Posts}}{{$i}}{{$e.Title}}{{end}}`,
				),
				data: decl("p"),
			},
			{
				name:  "bad field through a variable",
				files: page(`{{$p := .Page}}{{$p.Titel}}`),
				data:  decl("p"),
				want: []want{
					{"p.html", ".Titel", "V002", `field or method "Titel" not found in viewcheck.Post`, false},
				},
			},
			{
				name:  "dollar inside range is the root",
				files: page(`{{range .Posts}}{{$.Search}}{{$.Nope}}{{end}}`),
				data:  decl("p"),
				want: []want{
					{"p.html", ".Nope", "V002", `field or method "Nope" not found in viewcheck.indexData`, false},
				},
			},
			{
				name:  "map of strings is checked as values",
				files: page(`{{.Labels.any}}{{.Labels.any.Nope}}`),
				data:  decl("p"),
				want: []want{
					{"p.html", ".Nope", "V002", `field or method "Nope" not found in string`, false},
				},
			},
			{
				name:  "index through several levels",
				files: page(`{{(index .Grid 0 1).Title}}{{(index .ByTag "a" 0).Title}}{{(index .Grid 0 1).Titel}}`),
				data:  decl("p"),
				want: []want{
					{"p.html", ".Titel", "V002", `field or method "Titel" not found in viewcheck.Post`, false},
				},
			},
			{
				name:  "index through an any level is not checked",
				files: page(`{{(index .Meta "a" 0).Whatever}}`),
				data:  decl("p"),
			},
			{
				name:  "an assignment with another type makes the variable unknown",
				files: page(`{{$x := .Page}}{{$x = .Search}}{{$x.Whatever}}`), data: decl("p"),
			},
			{
				name:  "an assignment with the same type keeps checking",
				files: page(`{{$x := .Page}}{{$x = .Page}}{{$x.Titel}}`),
				data:  decl("p"),
				want: []want{
					{"p.html", ".Titel", "V002", `field or method "Titel" not found in viewcheck.Post`, false},
				},
			},
			{
				name:  "a declared fragment is checked with the page data",
				files: page(`{{block "frag" .Page}}{{.Title}}{{end}}`),
				data:  decl("p", "frag"),
				want: []want{
					{"p.html", ".Title", "V002", `field or method "Title" not found in viewcheck.indexData`, false},
				},
			},
			{
				name:  "a template that does not exist",
				files: page(`{{template "partials/nope" .}}`), data: decl("p"),
				want: []want{{"p.html", `"partials/nope"`, "V009", `template "partials/nope" is not defined`, false}},
			},
			{name: "map of any is not checked", files: page(`{{.Meta.a.b.c}}{{.Any.Whatever}}`), data: decl("p")},
			{
				name:  "argument of a function is checked",
				files: page(`{{printf "%s" .Nope}}`),
				data:  decl("p"),
				want: []want{
					{"p.html", ".Nope", "V002", `field or method "Nope" not found in viewcheck.indexData`, false},
				},
			},
			{
				name: "pipeline result type",
				files: page(
					`{{.Search | printf "%s" | len}}{{(index .Posts 0).Title}}{{(index .Posts 0).Nope}}`,
				),
				data: decl("p"),
				want: []want{{"p.html", ".Nope", "V002", `field or method "Nope" not found in viewcheck.Post`, false}},
			},
			{
				name: "partial called with dot and with a field",
				files: map[string]string{
					"p":            `{{define "content"}}{{template "partials/row" .Page}}{{template "partials/row" .}}{{end}}`,
					"partials/row": `{{.Title}}`,
				},
				data: decl("p"),
				want: []want{{
					"partials/row.html", ".Title", "V002",
					`field or method "Title" not found in viewcheck.indexData (rendering p)`, false,
				}},
			},
			{
				name: "a self-recursive template terminates",
				files: map[string]string{
					"p": `{{define "content"}}{{template "node" .Page}}{{end}}` +
						`{{define "node"}}{{.Title}}{{if .Tags}}{{template "node" .}}{{end}}{{end}}`,
				},
				data: decl("p"),
			},
			{
				name:  "template without a pipeline has an unknown dot",
				files: page(`{{template "partials/any"}}`),
				data:  decl("p"),
			},
			{
				name:   "strict reports unchecked expressions",
				files:  page(`{{.Meta.color}}`),
				data:   decl("p"),
				strict: true,
				want:   []want{{"p.html", ".Meta.color", "V008", ".Meta.color: type is any, not checked", true}},
			},
		})
	})

	t.Run("routes", func(t *testing.T) {
		t.Parallel()

		runAll(t, []tc{
			{
				name: "known routes",
				files: page(`{{route "posts.index"}}{{route "posts.show" "post" 3}}` +
					`{{route "files.show" "path" "a/b"}}{{route "home"}}`),
				data: decl("p"),
			},
			{
				name: "unknown route", files: page(`{{route "posts.sho" "post" 1}}`), data: decl("p"),
				want: []want{{"p.html", `"posts.sho"`, "V003", `route "posts.sho" is not registered`, false}},
			},
			{
				name: "wrong parameter", files: page(`{{route "posts.show" "id" 1}}`), data: decl("p"),
				want: []want{
					{"p.html", `"posts.show"`, "V004", `route "posts.show" needs parameter "post"`, false},
					{"p.html", `"id"`, "V004", `route "posts.show" has no parameter "id" (has: post)`, false},
				},
			},
			{
				name: "missing parameter", files: page(`{{route "posts.show"}}`), data: decl("p"),
				want: []want{{"p.html", `"posts.show"`, "V004", `route "posts.show" needs parameter "post"`, false}},
			},
			{
				name:  "names from a variable are not checked",
				files: page(`{{$r := "posts.nope"}}{{route $r}}{{route "posts.show" $r 1}}`), data: decl("p"),
			},
			{
				name:  "a piped value skips the parameter count",
				files: page(`{{.Page.ID | route "posts.show" "post"}}`), data: decl("p"),
			},
		})
	})

	t.Run("htmx", func(t *testing.T) {
		t.Parallel()

		runAll(t, []tc{
			{name: "target present", files: page(`<ul id="list"></ul><a hx-target="#list">x</a>`), data: decl("p")},
			{
				name:  "target missing",
				files: page(`<ul id="list"></ul><a hx-target="#lst">x</a>`),
				data:  decl("p"),
				want: []want{
					{"p.html", `#lst`, "V005", `hx-target "#lst": no element with that id in this view`, true},
				},
			},
			{
				name:  "select, indicator and include are checked",
				files: page(`<a hx-select="#a" hx-indicator="#b" hx-include="#c"></a><i id="a"></i>`), data: decl("p"),
				want: []want{
					{"p.html", `#b`, "V005", `hx-indicator "#b": no element with that id in this view`, true},
					{"p.html", `#c`, "V005", `hx-include "#c": no element with that id in this view`, true},
				},
			},
			{
				name:  "extended selectors are ignored",
				files: page(`<a hx-target="closest li" hx-select="this" hx-include=".x #y"></a>`), data: decl("p"),
			},
			{
				name:   "an id built by an action downgrades to a warning",
				files:  page(`<li id="row-{{.Search}}"></li><a hx-target="#nope"></a>`),
				data:   decl("p"),
				strict: true,
				want: []want{
					{"p.html", `#nope`, "V005", `hx-target "#nope": no element with that id in this view`, true},
				},
			},
			{
				name:  "an id built by an action is still a warning without strict",
				files: page(`<li id="row-{{.Search}}"></li><a hx-target="#nope"></a>`), data: decl("p"),
				want: []want{
					{"p.html", `#nope`, "V005", `hx-target "#nope": no element with that id in this view`, true},
				},
			},
			{
				name:   "a missing target is an error in strict",
				files:  page(`<ul id="list"></ul><a hx-target="#lst">x</a>`),
				data:   decl("p"),
				strict: true,
				want: []want{
					{"p.html", `#lst`, "V005", `hx-target "#lst": no element with that id in this view`, false},
				},
			},
			{
				name:  "ids and targets in single quotes or without quotes",
				files: page(`<ul id='a'></ul><ol id=b></ol><i hx-target='#a' hx-select="#b"></i>`), data: decl("p"),
			},
			{
				name: "a partial sees the ids of the whole page",
				files: map[string]string{
					"p":            `{{define "content"}}<ul id="list"></ul>{{template "partials/btn" .}}{{end}}`,
					"partials/btn": `<a hx-target="#list"></a>`,
				},
				data: decl("p"),
			},
		})
	})

	t.Run("declarations", func(t *testing.T) {
		t.Parallel()

		runAll(t, []tc{
			{
				name: "declared block missing", files: page(`x`), data: decl("p", "post-list"),
				want: []want{{"p.html", "", "V006", `block "post-list" is not defined in p`, false}},
			},
			{
				name: "view not found", files: page(`x`), data: decl("nope"),
				want: []want{{"nope.html", "", "V001", `view "nope" not found`, false}},
			},
			{
				name:  "an undeclared view warns in strict",
				files: map[string]string{"p": `{{define "content"}}x{{end}}`, "other": `{{define "content"}}y{{end}}`},
				data:  decl("p"), strict: true,
				want: []want{{"other.html", "", "V007", `view "other" has no typed declaration`, true}},
			},
		})
	})
}
