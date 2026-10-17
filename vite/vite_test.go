package vite_test

import (
	"flag"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/google/go-cmp/cmp"

	"github.com/AlexGalhardo/vukem/vite"
)

var update = flag.Bool("update", false, "rewrite golden files")

const manifest = `{
  "resources/js/app.tsx": {
    "file": "assets/app-1.js", "src": "resources/js/app.tsx", "isEntry": true,
    "imports": ["_vendor-2.js", "_shared-3.js"], "dynamicImports": ["resources/js/lazy.ts"],
    "css": ["assets/app-4.css"], "integrity": "sha384-APP"
  },
  "resources/js/admin.ts": {
    "file": "assets/admin-5.js", "src": "resources/js/admin.ts", "isEntry": true,
    "imports": ["_shared-3.js"]
  },
  "resources/js/lazy.ts": {"file": "assets/lazy-9.js", "src": "resources/js/lazy.ts", "isDynamicEntry": true},
  "_vendor-2.js": {
    "file": "assets/vendor-2.js", "imports": ["_shared-3.js"],
    "css": ["assets/vendor-6.css"], "integrity": "sha384-VENDOR"
  },
  "_shared-3.js": {"file": "assets/shared-3.js", "imports": ["_vendor-2.js"], "css": ["assets/vendor-6.css"]},
  "resources/css/app.css": {
    "file": "assets/app-7.css", "src": "resources/css/app.css", "isEntry": true, "integrity": "sha384-CSS"
  },
  "resources/img/logo.png": {"file": "assets/logo-8.png", "src": "resources/img/logo.png"}
}`

func prodFS() fstest.MapFS {
	return fstest.MapFS{"build/.vite/manifest.json": {Data: []byte(manifest)}}
}

func devFS() fstest.MapFS {
	fsys := prodFS()
	fsys["hot"] = &fstest.MapFile{Data: []byte("http://localhost:5173\r\n")}

	return fsys
}

// devPublicFS is a dev public directory that also holds a vendored file the
// app serves itself; the dev server does not serve the public dir.
func devPublicFS() fstest.MapFS {
	fsys := devFS()
	fsys["js/htmx.min.js"] = &fstest.MapFile{Data: []byte("htmx")}

	return fsys
}

func newVite(t *testing.T, cfg vite.Config) *vite.Vite {
	t.Helper()

	v, err := vite.New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	return v
}

func golden(t *testing.T, name, got string) {
	t.Helper()

	path := filepath.Join("testdata", name)

	if *update {
		if err := os.WriteFile(path, []byte(got), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	if diff := cmp.Diff(strings.ReplaceAll(string(want), "\r\n", "\n"), got); diff != "" {
		t.Errorf("%s mismatch (-want +got):\n%s", name, diff)
	}
}

func TestTagsGolden(t *testing.T) {
	t.Parallel()

	entries := []string{"resources/css/app.css", "resources/js/app.tsx", "resources/js/admin.ts"}

	tests := []struct {
		name   string
		cfg    vite.Config
		golden string
	}{
		{"production", vite.Config{FS: prodFS()}, "prod.golden.html"},
		{"production ignores hot file", vite.Config{FS: devFS()}, "prod.golden.html"},
		{"dev server", vite.Config{FS: devFS(), Dev: true}, "dev.golden.html"},
		{"dev without hot file uses build", vite.Config{FS: prodFS(), Dev: true}, "prod.golden.html"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			v := newVite(t, tt.cfg)

			got, err := v.Tags("n0nce", entries...)
			if err != nil {
				t.Fatalf("Tags() error = %v", err)
			}

			golden(t, tt.golden, string(got)+string(v.ReactRefresh("n0nce")))
		})
	}
}

func TestTagsWithoutNonce(t *testing.T) {
	t.Parallel()

	got, err := newVite(t, vite.Config{FS: prodFS()}).Tags("", "resources/js/admin.ts")
	if err != nil {
		t.Fatal(err)
	}

	want := `<link rel="modulepreload" href="/build/assets/shared-3.js">` + "\n" +
		`<link rel="modulepreload" href="/build/assets/vendor-2.js" integrity="sha384-VENDOR">` + "\n" +
		`<link rel="stylesheet" href="/build/assets/vendor-6.css">` + "\n" +
		`<script type="module" src="/build/assets/admin-5.js"></script>` + "\n"

	if diff := cmp.Diff(want, string(got)); diff != "" {
		t.Errorf("Tags() mismatch (-want +got):\n%s", diff)
	}
}

func TestTagsErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		fsys fstest.MapFS
		want string
	}{
		{"unknown entry", prodFS(), `vite: entry "resources/js/nope.ts" not in manifest`},
		{"missing manifest", fstest.MapFS{}, "vite: read manifest"},
		{"broken manifest", fstest.MapFS{"build/.vite/manifest.json": {Data: []byte("{")}}, "vite: parse manifest"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := newVite(t, vite.Config{FS: tt.fsys}).Tags("", "resources/js/nope.ts")
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("Tags() error = %v, want containing %q", err, tt.want)
			}
		})
	}
}

func TestTagsDevWithoutBuild(t *testing.T) {
	t.Parallel()

	got, err := newVite(t, vite.Config{FS: fstest.MapFS{}, Dev: true}).Tags("n", "resources/js/app.ts")
	if err != nil || !strings.HasPrefix(string(got), "<!-- vite:") {
		t.Errorf("Tags() in dev without build = %q, %v", got, err)
	}
}

func TestAsset(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		cfg    vite.Config
		path   string
		want   string
		wantOK bool
	}{
		{"manifest", vite.Config{FS: prodFS()}, "resources/img/logo.png", "/build/assets/logo-8.png", true},
		{"leading slash", vite.Config{FS: prodFS()}, "/resources/img/logo.png", "/build/assets/logo-8.png", true},
		{"not in manifest", vite.Config{FS: prodFS()}, "img/x.png", "", false},
		{"no manifest", vite.Config{FS: fstest.MapFS{}}, "img/x.png", "", false},
		{
			"dev server",
			vite.Config{FS: devFS(), Dev: true},
			"resources/img/logo.png",
			"http://localhost:5173/resources/img/logo.png",
			true,
		},
		{"dev server, public file", vite.Config{FS: devPublicFS(), Dev: true}, "js/htmx.min.js", "", false},
		{"dev server, public file with slash", vite.Config{FS: devPublicFS(), Dev: true}, "/js/htmx.min.js", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, ok := newVite(t, tt.cfg).Asset(tt.path)
			if got != tt.want || ok != tt.wantOK {
				t.Errorf("Asset(%q) = %q, %v, want %q, %v", tt.path, got, ok, tt.want, tt.wantOK)
			}
		})
	}
}

func TestManifestReloadInDev(t *testing.T) {
	t.Parallel()

	fsys := prodFS()
	dev := newVite(t, vite.Config{FS: fsys, Dev: true})
	prod := newVite(t, vite.Config{FS: fsys})

	for _, v := range []*vite.Vite{dev, prod} {
		if _, ok := v.Asset("resources/img/logo.png"); !ok {
			t.Fatal("Asset() before rebuild not found")
		}
	}

	fsys["build/.vite/manifest.json"] = &fstest.MapFile{
		Data: []byte(`{"resources/img/logo.png": {"file": "assets/logo-new.png"}}`),
	}

	if got, _ := dev.Asset("resources/img/logo.png"); got != "/build/assets/logo-new.png" {
		t.Errorf("dev Asset() = %q, want rebuilt file", got)
	}

	if got, _ := prod.Asset("resources/img/logo.png"); got != "/build/assets/logo-8.png" {
		t.Errorf("production Asset() = %q, want cached file", got)
	}
}

func TestVersion(t *testing.T) {
	t.Parallel()

	v := newVite(t, vite.Config{FS: prodFS()})
	got := v.Version()

	if len(got) != 32 || got != v.Version() {
		t.Errorf("Version() = %q, want a stable 32-char hash", got)
	}

	other := fstest.MapFS{"build/.vite/manifest.json": {Data: []byte(`{}`)}}
	if newVite(t, vite.Config{FS: other}).Version() == got {
		t.Error("Version() did not change with the manifest")
	}

	if got := newVite(t, vite.Config{FS: fstest.MapFS{}}).Version(); got != "" {
		t.Errorf("Version() without manifest = %q, want empty", got)
	}
}

func TestHotURL(t *testing.T) {
	t.Parallel()

	if got := newVite(t, vite.Config{FS: devFS(), Dev: true}).HotURL(); got != "http://localhost:5173" {
		t.Errorf("HotURL() = %q", got)
	}

	if got := newVite(t, vite.Config{FS: devFS()}).HotURL(); got != "" {
		t.Errorf("HotURL() in production = %q, want empty", got)
	}
}

func TestContextMiddleware(t *testing.T) {
	t.Parallel()

	if got := vite.FromContext(t.Context()); got != nil {
		t.Fatalf("FromContext(empty) = %p, want nil", got)
	}

	v := newVite(t, vite.Config{FS: prodFS()})

	var got *vite.Vite

	h := v.Middleware()(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got = vite.FromContext(r.Context())
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequestWithContext(t.Context(), "GET", "/", http.NoBody))

	if got != v {
		t.Fatalf("FromContext() = %p, want %p", got, v)
	}
}

func TestNewRequiresFS(t *testing.T) {
	t.Parallel()

	if _, err := vite.New(vite.Config{}); err == nil {
		t.Error("New(Config{}) error = nil, want error")
	}
}

func TestSecurity_A02_ViteHotFileIgnoredInProduction(t *testing.T) {
	t.Parallel()

	got, err := newVite(t, vite.Config{FS: devFS()}).Tags("", "resources/js/admin.ts")
	if err != nil {
		t.Fatal(err)
	}

	if strings.Contains(string(got), "localhost:5173") {
		t.Errorf("Tags() = %s, want no dev server URL outside Dev", got)
	}
}

func TestSecurity_A02_ViteTagsCarryNonce(t *testing.T) {
	t.Parallel()

	for _, fsys := range []fstest.MapFS{prodFS(), devFS()} {
		v := newVite(t, vite.Config{FS: fsys, Dev: true})

		tags, err := v.Tags(`a"b`, "resources/js/app.tsx")
		if err != nil {
			t.Fatal(err)
		}

		html := string(tags) + string(v.ReactRefresh(`a"b`))
		for line := range strings.Lines(html) {
			if strings.HasPrefix(line, "<script") && !strings.Contains(line, `nonce="a&#34;b"`) {
				t.Errorf("script tag %q lacks the escaped nonce", line)
			}
		}
	}
}

func TestSecurity_A05_ViteRejectsUnsafeHotURL(t *testing.T) {
	t.Parallel()

	for _, hot := range []string{"javascript:alert(1)", `http://x"><script>`, "//evil.test", ""} {
		fsys := prodFS()
		fsys["hot"] = &fstest.MapFile{Data: []byte(hot)}

		if _, err := newVite(t, vite.Config{FS: fsys, Dev: true}).Tags("", "resources/js/admin.ts"); err == nil {
			t.Errorf("Tags() with hot %q error = nil, want error", hot)
		}
	}
}
