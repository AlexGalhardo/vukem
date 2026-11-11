package studio

import (
	"bytes"
	"embed"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

//go:embed templates
var templatesFS embed.FS

//go:embed assets
var assetsFS embed.FS

// loadTemplates parses the layout and the partials once, then gives every page
// its own copy of that set (each page defines "content" and "title").
func loadTemplates() (map[string]*template.Template, error) {
	funcs := template.FuncMap{"dict": dict}

	base, err := template.New("base").
		Funcs(funcs).
		ParseFS(templatesFS, "templates/layouts/*.html", "templates/partials/*.html")
	if err != nil {
		return nil, fmt.Errorf("studio: templates: %w", err)
	}

	files, err := fs.Glob(templatesFS, "templates/pages/*.html")
	if err != nil {
		return nil, err
	}

	pages := make(map[string]*template.Template, len(files))

	for _, f := range files {
		t, err := base.Clone()
		if err != nil {
			return nil, err
		}

		if _, err := t.ParseFS(templatesFS, f); err != nil {
			return nil, fmt.Errorf("studio: template %s: %w", f, err)
		}

		pages[strings.TrimSuffix(path.Base(f), ".html")] = t
	}

	return pages, nil
}

// dict builds a map for a partial: dict "Title" "x" "Text" "y".
func dict(kv ...any) (map[string]any, error) {
	if len(kv)%2 != 0 {
		return nil, errors.New("dict: odd number of arguments")
	}

	m := make(map[string]any, len(kv)/2)

	for i := 0; i < len(kv); i += 2 {
		k, ok := kv[i].(string)
		if !ok {
			return nil, errors.New("dict: keys must be strings")
		}

		m[k] = kv[i+1]
	}

	return m, nil
}

// render writes page inside the layout; the output is buffered so a template
// error becomes a clean 500.
func (s *Studio) render(w http.ResponseWriter, r *http.Request, status int, page string, data map[string]any) {
	t := s.pages[page]
	if t == nil {
		http.Error(w, "unknown page", http.StatusInternalServerError)
		return
	}

	if data == nil {
		data = map[string]any{}
	}

	data["Version"], data["Write"], data["Sources"] = s.cfg.Version, false, s.sourceViews(
		r,
	) // read-only: editing is a later release

	if _, ok := data["Current"]; !ok {
		data["Current"] = r.PathValue("source")
	}

	var buf bytes.Buffer
	if err := t.ExecuteTemplate(&buf, "layout", data); err != nil {
		s.cfg.Log.ErrorContext(r.Context(), "studio: render", "page", page, "error", err)
		http.Error(w, "render error", http.StatusInternalServerError)

		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(buf.Bytes())
}

type sourceView struct {
	Name, Kind, Label string
	Online            bool
}

func (s *Studio) sourceViews(r *http.Request) []sourceView {
	out := make([]sourceView, 0, len(s.cfg.Sources))

	for _, src := range s.cfg.Sources {
		out = append(out, sourceView{
			Name: src.Name(), Kind: string(src.Kind()), Label: src.Label(), Online: src.Ping(r.Context()) == nil,
		})
	}

	return out
}

// assets serves the embedded CSS, script and htmx. They sit behind the
// session like every route, and are never cached so an upgrade shows at once.
func (s *Studio) assets() http.Handler {
	sub, err := fs.Sub(assetsFS, "assets")
	if err != nil {
		return http.NotFoundHandler()
	}

	files := http.StripPrefix("/assets/", http.FileServerFS(sub))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache")
		files.ServeHTTP(w, r)
	})
}
