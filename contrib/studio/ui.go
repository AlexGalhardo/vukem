package studio

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// uiFS is the committed build of web/ (task studio:ui). "all:" keeps bundler
// files whose names start with "_" or ".", which plain "ui" would skip.
//
//go:embed all:ui
var uiFS embed.FS

// uiTypes maps extensions to content types explicitly: the host's registry
// (Windows) can disagree about .js, and nosniff makes a wrong type fatal.
var uiTypes = map[string]string{ //nolint:gochecknoglobals // read-only
	".html":  "text/html; charset=utf-8",
	".js":    "text/javascript; charset=utf-8",
	".css":   "text/css; charset=utf-8",
	".svg":   "image/svg+xml",
	".woff2": "font/woff2",
	".json":  "application/json",
	".txt":   "text/plain; charset=utf-8",
}

// uiRoutes serves the React app: files under /ui/assets/ come from the build,
// every other path under /ui/ is a client route and gets index.html.
func (s *Studio) uiRoutes() {
	sub, err := fs.Sub(uiFS, "ui")
	if err != nil {
		return
	}

	s.mux.Handle("GET /ui/{path...}", uiHandler(sub))
}

func uiHandler(files fs.FS) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("path")

		if strings.HasPrefix(name, "assets/") {
			serveUIFile(w, r, files, name)
			return
		}

		serveUIFile(w, r, files, "index.html")
	})
}

func serveUIFile(w http.ResponseWriter, r *http.Request, files fs.FS, name string) {
	if !fs.ValidPath(name) {
		http.NotFound(w, r)
		return
	}

	data, err := fs.ReadFile(files, name)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	ct, ok := uiTypes[path.Ext(name)]
	if !ok {
		ct = "application/octet-stream"
	}

	w.Header().Set("Content-Type", ct)
	_, _ = w.Write(data)
}
