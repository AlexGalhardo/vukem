package mail

import (
	"html/template"
	"maps"
	"net/http"
	"slices"
	"strings"
)

var previewIndex = template.Must(template.New("index").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><title>Mail previews</title></head>
<body><h1>Mail previews</h1><ul>
{{range .}}<li><a href="{{.}}">{{.}}</a> (<a href="{{.}}?text=1">text</a>)</li>{{end}}
</ul></body></html>`))

// Preview serves rendered mailables for development: the index lists
// names, "<name>" shows the HTML part and "<name>?text=1" the text part.
// Mount it only outside production:
//
//	if !app.Config.IsProduction() {
//		r.Handle("/_mail/", http.StripPrefix("/_mail/", mail.Preview(mailer, map[string]mail.Mailable{...})))
//	}
func Preview(m *Mailer, mailables map[string]Mailable) http.Handler {
	names := slices.Sorted(maps.Keys(mailables))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.Trim(r.URL.Path, "/")
		if name == "" {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_ = previewIndex.Execute(w, names)

			return
		}

		mailable, ok := mailables[name]
		if !ok {
			http.NotFound(w, r)
			return
		}

		msg, err := m.Render(r.Context(), mailable)
		if err != nil {
			http.Error(w, "render failed: "+err.Error(), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Cache-Control", "no-store")

		if r.URL.Query().Get("text") != "" {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			_, _ = w.Write([]byte(msg.Text))

			return
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(msg.HTML))
	})
}
