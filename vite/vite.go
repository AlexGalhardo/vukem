// Package vite renders the tags that load a Vite frontend, following the
// Laravel "hot file" model.
//
// In development the starter's Vite plugin writes public/hot with the dev
// server URL; Tags then points the browser at that server. Otherwise Tags
// reads public/build/.vite/manifest.json and emits modulepreload, stylesheet
// and script tags for each entry and its recursive imports, deduplicated
// across entries. Every tag gets the CSP nonce, and the manifest's
// "integrity" values become SRI attributes.
package vite

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"net/url"
	"path"
	"slices"
	"strings"
	"sync"
)

// Config configures a Vite.
type Config struct {
	// FS is the public directory (os.DirFS("public") or an embed.FS) holding
	// the "hot" file and the build directory.
	FS fs.FS
	// Dev honours the hot file and re-reads the manifest on every call. Off
	// (production), a stray hot file is ignored and the manifest is cached.
	Dev bool
	// BuildDir is the build directory inside FS and the URL prefix of built
	// files (default "build").
	BuildDir string
}

// Vite renders Vite tags; it is safe for concurrent use.
type Vite struct {
	cfg Config

	mu       sync.Mutex
	manifest manifest
	version  string
}

type chunk struct {
	File      string   `json:"file"`
	Imports   []string `json:"imports"`
	CSS       []string `json:"css"`
	Integrity string   `json:"integrity"`
}

type manifest map[string]chunk

// New returns a Vite. The manifest is read on first use, so an app can boot
// before the first build while the dev server runs.
func New(cfg Config) (*Vite, error) {
	if cfg.FS == nil {
		return nil, errors.New("vite: Config.FS is required")
	}

	if cfg.BuildDir == "" {
		cfg.BuildDir = "build"
	}

	return &Vite{cfg: cfg}, nil
}

// Tags returns the HTML that loads entries (manifest keys such as
// "resources/js/app.ts"). nonce is the request's CSP nonce ("" for none).
func (v *Vite) Tags(nonce string, entries ...string) (template.HTML, error) {
	hot, err := v.hot()
	if err != nil {
		return "", err
	}

	var b strings.Builder

	if hot != "" {
		b.WriteString(script(hot+"/@vite/client", "", nonce))

		for _, e := range entries {
			u := hot + "/" + strings.TrimPrefix(e, "/")
			if isCSS(e) {
				b.WriteString(stylesheet(u, "", nonce))
			} else {
				b.WriteString(script(u, "", nonce))
			}
		}

		return trustedHTML(b.String()), nil
	}

	m, err := v.load()
	if err != nil {
		return "", err
	}

	var preloads, styles, scripts []string

	seen := map[string]bool{}
	sri := m.integrityByFile()

	for _, e := range entries {
		c, ok := m[strings.TrimPrefix(e, "/")]
		if !ok {
			return "", fmt.Errorf("vite: entry %q not in manifest", e)
		}

		for _, imp := range m.imports(c) {
			if !seen[imp.File] {
				seen[imp.File] = true
				preloads = append(preloads, preload(v.url(imp.File), imp.Integrity, nonce))
			}
		}

		for _, css := range m.css(c) {
			if !seen[css] {
				seen[css] = true
				styles = append(styles, stylesheet(v.url(css), sri[css], nonce))
			}
		}

		if seen[c.File] {
			continue
		}

		seen[c.File] = true

		if isCSS(c.File) {
			styles = append(styles, stylesheet(v.url(c.File), c.Integrity, nonce))
		} else {
			scripts = append(scripts, script(v.url(c.File), c.Integrity, nonce))
		}
	}

	for _, tags := range [][]string{preloads, styles, scripts} {
		for _, t := range tags {
			b.WriteString(t)
		}
	}

	return trustedHTML(b.String()), nil
}

// ReactRefresh returns the @vitejs/plugin-react preamble while the dev
// server runs, and nothing otherwise. Place it before Tags.
func (v *Vite) ReactRefresh(nonce string) template.HTML {
	hot, err := v.hot()
	if err != nil || hot == "" {
		return ""
	}

	return trustedHTML(`<script type="module"` + nonceAttr(nonce) + `>
import RefreshRuntime from "` + template.JSEscapeString(hot) + `/@react-refresh"
RefreshRuntime.injectIntoGlobalHook(window)
window.$RefreshReg$ = () => {}
window.$RefreshSig$ = () => (type) => type
window.__vite_plugin_react_preamble_installed__ = true
</script>
`)
}

// Asset returns the URL of a file Vite processed ("resources/img/logo.png"),
// and false when neither the dev server nor the manifest knows it.
func (v *Vite) Asset(p string) (string, bool) {
	p = strings.TrimPrefix(p, "/")

	if hot, err := v.hot(); err == nil && hot != "" {
		return hot + "/" + p, true
	}

	m, err := v.load()
	if err != nil {
		return "", false
	}

	c, ok := m[p]
	if !ok {
		return "", false
	}

	return v.url(c.File), true
}

// HotURL returns the dev server URL while it runs (Dev on and a valid hot
// file), and "" otherwise; inertia.SSRConfig.HotURL uses it.
func (v *Vite) HotURL() string {
	u, err := v.hot()
	if err != nil {
		return ""
	}

	return u
}

// hot returns the dev server URL, or "" when not in Dev or no hot file
// exists. A hot file that is not an http(s) origin is an error.
func (v *Vite) hot() (string, error) {
	if !v.cfg.Dev {
		return "", nil
	}

	b, err := fs.ReadFile(v.cfg.FS, "hot")
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}

	if err != nil {
		return "", fmt.Errorf("vite: read hot file: %w", err)
	}

	raw := strings.TrimSpace(string(b))

	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" ||
		strings.ContainsAny(raw, "\"'<> ") {
		return "", fmt.Errorf("vite: hot file holds %q, want an http(s) URL", raw)
	}

	return strings.TrimSuffix(raw, "/"), nil
}

func (v *Vite) load() (manifest, error) {
	v.mu.Lock()
	defer v.mu.Unlock()

	if v.manifest != nil && !v.cfg.Dev {
		return v.manifest, nil
	}

	b, err := fs.ReadFile(v.cfg.FS, path.Join(v.cfg.BuildDir, ".vite", "manifest.json"))
	if err != nil {
		return nil, fmt.Errorf("vite: read manifest (run vite build): %w", err)
	}

	var m manifest
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("vite: parse manifest: %w", err)
	}

	sum := sha256.Sum256(b)
	v.manifest, v.version = m, hex.EncodeToString(sum[:16])

	return m, nil
}

// Version returns a hash of the manifest, which changes with every build
// (the Inertia asset version), or "" when there is no manifest.
func (v *Vite) Version() string {
	if _, err := v.load(); err != nil {
		return ""
	}

	v.mu.Lock()
	defer v.mu.Unlock()

	return v.version
}

func (v *Vite) url(file string) string {
	return "/" + v.cfg.BuildDir + "/" + file
}

// imports returns c's static imports, depth-first, each once.
func (m manifest) imports(c chunk) []chunk {
	var (
		out  []chunk
		seen = map[string]bool{}
		walk func(keys []string)
	)

	walk = func(keys []string) {
		for _, k := range keys {
			if seen[k] {
				continue
			}

			seen[k] = true

			if imp, ok := m[k]; ok {
				out = append(out, imp)
				walk(imp.Imports)
			}
		}
	}
	walk(c.Imports)

	return out
}

// css returns the stylesheets of c and of its imports.
func (m manifest) css(c chunk) []string {
	out := slices.Clone(c.CSS)
	for _, imp := range m.imports(c) {
		out = append(out, imp.CSS...)
	}

	return out
}

func (m manifest) integrityByFile() map[string]string {
	out := make(map[string]string, len(m))

	for _, c := range m {
		if c.Integrity != "" {
			out[c.File] = c.Integrity
		}
	}

	return out
}

func isCSS(p string) bool {
	switch path.Ext(p) {
	case ".css", ".scss", ".sass", ".less", ".styl", ".stylus", ".pcss", ".postcss":
		return true
	}

	return false
}

func script(src, integrity, nonce string) string {
	return `<script type="module" src="` + esc(src) + `"` + attrs(integrity, nonce) + "></script>\n"
}

func stylesheet(href, integrity, nonce string) string {
	return `<link rel="stylesheet" href="` + esc(href) + `"` + attrs(integrity, nonce) + ">\n"
}

func preload(href, integrity, nonce string) string {
	return `<link rel="modulepreload" href="` + esc(href) + `"` + attrs(integrity, nonce) + ">\n"
}

func attrs(integrity, nonce string) string {
	s := ""
	if integrity != "" {
		s = ` integrity="` + esc(integrity) + `"`
	}

	return s + nonceAttr(nonce)
}

func nonceAttr(nonce string) string {
	if nonce == "" {
		return ""
	}

	return ` nonce="` + esc(nonce) + `"`
}

func esc(s string) string { return template.HTMLEscapeString(s) }

// trustedHTML marks markup built from escaped parts as safe.
func trustedHTML(s string) template.HTML {
	return template.HTML(s) //nolint:gosec // G203: every dynamic part is escaped
}
