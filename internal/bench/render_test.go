package bench

import (
	"bytes"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"testing"
	"testing/fstest"

	"github.com/AlexGalhardo/vukem/view"
	"github.com/AlexGalhardo/vukem/web"
)

func users(n int) []user {
	out := make([]user, n)
	for i := range out {
		out[i] = user{
			ID:    int64(i + 1),
			Name:  fmt.Sprintf("User %d", i+1),
			Email: fmt.Sprintf("user%d@example.com", i+1),
		}
	}

	return out
}

// BenchmarkRenderJSON encodes 20 users: c.JSON against json.Marshal plus a
// plain write.
func BenchmarkRenderJSON(b *testing.B) {
	data := users(20)
	services := &web.Services{Log: slog.New(slog.DiscardHandler)}

	run(b, []impl{
		{"encoding_json", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, data) })},
		{"vukem", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			web.NewCtx(w, r, services).Serve(func(c *web.Ctx) error { return c.JSON(http.StatusOK, data) })
		})},
	}, "/")
}

var pages = fstest.MapFS{
	"layouts/app.html": {Data: []byte(`<!doctype html><html><head><title>{{block "title" .}}App{{end}}</title></head>` +
		`<body>{{template "partials/nav" .}}<main>{{block "content" .}}{{end}}</main></body></html>`)},
	"partials/nav.html": {Data: []byte(`<nav><a href="/">Home</a> · {{.Title}}</nav>`)},
	"users/index.html": {Data: []byte(`{{define "title"}}{{.Title}}{{end}}{{define "content"}}<h1>{{.Title}}</h1>` +
		`<table>{{range .Users}}<tr><td>{{.ID}}</td><td>{{.Name}}</td><td>{{.Email}}</td></tr>{{end}}</table>{{end}}`)},
}

type page struct {
	Title string
	Users []user
}

// BenchmarkRenderHTML renders a layout, a partial and a 20-row table: the
// view engine through c.Render against one pre-parsed html/template set.
// Both buffer the page before writing it.
func BenchmarkRenderHTML(b *testing.B) {
	data := page{Title: "Users", Users: users(20)}

	raw := template.New("")
	for _, name := range []string{"layouts/app", "partials/nav", "users/index"} {
		template.Must(raw.New(name).Parse(string(pages[name+".html"].Data)))
	}

	engine, err := view.New(view.Config{FS: pages})
	if err != nil {
		b.Fatal(err)
	}

	services := &web.Services{Log: slog.New(slog.DiscardHandler), Views: engine}

	run(b, []impl{
		{"html_template", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			var buf bytes.Buffer
			if err := raw.ExecuteTemplate(&buf, "layouts/app", data); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}

			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(buf.Bytes())
		})},
		{"vukem_view", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			web.NewCtx(w, r, services).Serve(func(c *web.Ctx) error { return c.Render("users/index", data) })
		})},
	}, "/")
}
