package middleware

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path"
	"strings"
	"sync"
	"time"
)

// StaticConfig configures Static.
type StaticConfig struct {
	// Prefix is the URL prefix ("/" serves files at the root).
	Prefix string
	// ImmutablePrefixes get a one-year immutable cache (hashed build output).
	// Elsewhere only fingerprinted names (see Static) are immutable.
	ImmutablePrefixes []string
}

// Static serves files from root when they exist and passes other requests
// on. Files are opened through os.Root, so ".." and symlinks cannot escape
// the directory (A01); dotfiles and directory listings are never served.
// The caller owns root and closes it on shutdown.
//
// Files under ImmutablePrefixes ("/build/" by default) and fingerprinted
// names get a one-year immutable cache; every other file must revalidate.
// A name is fingerprinted when its last "-" or "." segment before the
// extension is 8+ lowercase hex characters with a digit and a letter
// (app-3f9a1c2b.js, app.4f2a9c1e.css) or 8 base64url characters with a
// digit, an upper and a lower case letter (Vite's index-BkS3pZ8_.js);
// hero-mountains.jpg or report-20240101.pdf are not. Revalidation uses
// Last-Modified, or an ETag (a content hash) for files without a
// modification time, such as embed.FS files.
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
	etags := &etagCache{}

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

			st, err := fs.Stat(fsys, name)
			if err != nil || st.IsDir() {
				next.ServeHTTP(w, r)
				return
			}

			// embed.FS has no modification times: without an ETag every
			// visit would download the file again.
			if st.ModTime().IsZero() {
				if tag, ok := etags.get(fsys, name, st); ok {
					w.Header().Set("ETag", tag)
				}
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

	return fingerprinted(path.Base(urlPath))
}

// fingerprinted reports a content hash in the last "-" or "." segment
// before the extension. The rules are strict on purpose: a false positive
// caches a changing file for a year, a false negative only revalidates.
func fingerprinted(base string) bool {
	ext := path.Ext(base)
	stem := strings.TrimSuffix(base, ext)

	i := strings.LastIndexAny(stem, ".-")
	if ext == "" || i <= 0 {
		return false
	}

	seg := stem[i+1:]

	digit, lower, upper := false, false, false
	hexOnly, b64 := true, true

	for _, c := range seg {
		switch {
		case c >= '0' && c <= '9':
			digit = true
		case c >= 'a' && c <= 'f':
			lower = true
		case c >= 'g' && c <= 'z':
			lower, hexOnly = true, false
		case c >= 'A' && c <= 'Z':
			upper, hexOnly = true, false
		case c == '_':
			hexOnly = false
		default:
			hexOnly, b64 = false, false
		}
	}

	if hexOnly && len(seg) >= 8 {
		return digit && lower
	}

	return b64 && len(seg) == 8 && digit && lower && upper
}

// etagCache holds content hashes of files without a modification time.
// Such files (embed.FS) do not change while the process runs; the key
// still carries the size so a changed in-memory file gets a new tag.
type etagCache struct {
	m sync.Map // etagKey -> string
}

type etagKey struct {
	name string
	size int64
	mod  time.Time
}

func (c *etagCache) get(fsys fs.FS, name string, st fs.FileInfo) (string, bool) {
	key := etagKey{name: name, size: st.Size(), mod: st.ModTime()}
	if v, ok := c.m.Load(key); ok {
		tag, isString := v.(string)
		return tag, isString
	}

	f, err := fsys.Open(name)
	if err != nil {
		return "", false
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", false
	}

	tag := `"` + hex.EncodeToString(h.Sum(nil)[:16]) + `"`
	c.m.Store(key, tag)

	return tag, true
}
