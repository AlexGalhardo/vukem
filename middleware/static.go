package middleware

import (
	"io/fs"
	"net/http"
	"os"
	"path"
	"regexp"
	"strings"
)

// StaticConfig configures Static.
type StaticConfig struct {
	// Prefix is the URL prefix ("/" serves files at the root).
	Prefix string
	// ImmutablePrefixes get a one-year immutable cache (hashed build output).
	ImmutablePrefixes []string
}

// hashedName matches fingerprinted files such as app-3f9a1c2b.js.
var hashedName = regexp.MustCompile(`[.-][A-Za-z0-9_-]{8,}\.[a-z0-9]+$`)

// Static serves files from root when they exist and passes other requests
// on. Files are opened through os.Root, so ".." and symlinks cannot escape
// the directory (A01); dotfiles and directory listings are never served.
// The caller owns root and closes it on shutdown.
func Static(root *os.Root, cfg StaticConfig) Middleware {
	return StaticFS(root.FS(), cfg)
}

// StaticFS is Static over any fs.FS (e.g. an embed.FS in production).
func StaticFS(fsys fs.FS, cfg StaticConfig) Middleware {
	prefix := "/" + strings.Trim(cfg.Prefix, "/")
	if prefix != "/" {
		prefix += "/"
	}

	immutable := cfg.ImmutablePrefixes
	if immutable == nil {
		immutable = []string{"/build/"}
	}

	files := http.FileServerFS(fsys)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet && r.Method != http.MethodHead {
				next.ServeHTTP(w, r)
				return
			}

			rel, ok := strings.CutPrefix(r.URL.Path, prefix)
			if !ok || rel == "" || hasDotSegment(rel) {
				next.ServeHTTP(w, r)
				return
			}

			name := path.Clean(rel)
			if !fs.ValidPath(name) {
				next.ServeHTTP(w, r)
				return
			}

			if st, err := fs.Stat(fsys, name); err != nil || st.IsDir() {
				next.ServeHTTP(w, r)
				return
			}

			if isImmutable(r.URL.Path, immutable) {
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			} else {
				w.Header().Set("Cache-Control", "public, max-age=0, must-revalidate")
			}

			r2 := r.Clone(r.Context())
			r2.URL.Path = "/" + name
			files.ServeHTTP(w, r2)
		})
	}
}

func hasDotSegment(p string) bool {
	for seg := range strings.SplitSeq(p, "/") {
		if strings.HasPrefix(seg, ".") {
			return true
		}
	}

	return false
}

func isImmutable(urlPath string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(urlPath, p) {
			return true
		}
	}

	return hashedName.MatchString(urlPath)
}
