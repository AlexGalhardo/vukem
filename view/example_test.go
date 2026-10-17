package view_test

import (
	"context"
	"fmt"
	"os"
	"testing/fstest"

	"github.com/AlexGalhardo/vukem/view"
)

func Example() {
	// Apps use os.DirFS("resources/views") or an embed.FS.
	views := fstest.MapFS{
		"layouts/app.html": {Data: []byte(`<main>{{block "content" .}}{{end}}</main>`)},
		"posts/index.html": {Data: []byte(`{{define "content"}}{{range .}}<p>{{.}}</p>{{end}}{{end}}`)},
	}
	e, err := view.New(view.Config{FS: views})
	if err != nil {
		fmt.Println(err)
		return
	}

	// Values are escaped by html/template.
	_ = e.Render(context.Background(), os.Stdout, "posts/index", []string{"Hello", "<b>world</b>"})
	// Output: <main><p>Hello</p><p>&lt;b&gt;world&lt;/b&gt;</p></main>
}

func ExampleEngine_RenderBlock() {
	views := fstest.MapFS{
		"posts/index.html": {Data: []byte(
			`{{/* layout: none */}}<ul>{{block "items" .}}{{range .}}<li>{{.}}</li>{{end}}{{end}}</ul>`,
		)},
	}
	e, _ := view.New(view.Config{FS: views})

	// An htmx request gets only the fragment.
	_ = e.RenderBlock(context.Background(), os.Stdout, "posts/index", "items", []string{"a", "b"})
	// Output: <li>a</li><li>b</li>
}
