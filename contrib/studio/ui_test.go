package studio

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"path"
	"strings"
	"testing"
)

// uiClient logs in once (the token is single-use) and returns a GET helper.
func uiClient(t *testing.T, s *Studio) func(target string) *httptest.ResponseRecorder {
	t.Helper()

	login := session(t, s)

	return func(target string) *httptest.ResponseRecorder {
		r := request(http.MethodGet, target)
		r.AddCookie(login)

		return serve(s, r)
	}
}

// builtAssets lists the files of the embedded build under assets/.
func builtAssets(t *testing.T) []string {
	t.Helper()

	var out []string

	err := fs.WalkDir(uiFS, "ui/assets", func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			out = append(out, strings.TrimPrefix(p, "ui/"))
		}

		return err
	})
	if err != nil {
		t.Fatal(err)
	}

	return out
}

func TestUIAssetsEmbedded(t *testing.T) {
	t.Parallel()

	if _, err := fs.Stat(uiFS, "ui/index.html"); err != nil {
		t.Fatalf("ui/index.html is not embedded: %v", err)
	}

	var js, css int

	for _, f := range builtAssets(t) {
		switch path.Ext(f) {
		case ".js":
			js++
		case ".css":
			css++
		}
	}

	if js == 0 || css == 0 {
		t.Errorf("embedded assets = %d js, %d css, want at least one of each", js, css)
	}
}

func TestUIServed(t *testing.T) {
	t.Parallel()

	s := newStudio(t)
	get := uiClient(t, s)

	rec := get("/ui/")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /ui/ = %d, want 200", rec.Code)
	}

	if got := rec.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/html") {
		t.Errorf("GET /ui/ Content-Type = %q, want text/html", got)
	}

	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("GET /ui/ Cache-Control = %q, want no-store", got)
	}

	if got := rec.Header().Get("Content-Security-Policy"); got != strictCSP {
		t.Errorf("GET /ui/ CSP = %q, want the strict policy", got)
	}

	for _, f := range builtAssets(t) {
		rec := get("/ui/" + f)
		if rec.Code != http.StatusOK {
			t.Errorf("GET /ui/%s = %d, want 200", f, rec.Code)
			continue
		}

		want := map[string]string{".js": "text/javascript", ".css": "text/css"}[path.Ext(f)]
		if got := rec.Header().Get("Content-Type"); !strings.HasPrefix(got, want) {
			t.Errorf("GET /ui/%s Content-Type = %q, want %q", f, got, want)
		}
	}
}

func TestUIFallback(t *testing.T) {
	t.Parallel()

	s := newStudio(t)
	get := uiClient(t, s)
	index := get("/ui/").Body.String()

	for _, target := range []string{"/ui/s/db", "/ui/s/db/t/users", "/ui/anything/else"} {
		rec := get(target)
		if rec.Code != http.StatusOK || rec.Body.String() != index {
			t.Errorf("GET %s = %d with a different body, want the index page", target, rec.Code)
		}
	}

	for _, target := range []string{"/ui/assets/missing.js", "/ui/assets/../index.html"} {
		if rec := get(target); rec.Code == http.StatusOK && rec.Body.String() == index {
			t.Errorf("GET %s served the index page, want a miss", target)
		}
	}

	if rec := serve(s, request(http.MethodGet, "/ui/")); rec.Code != http.StatusUnauthorized {
		t.Errorf("GET /ui/ without a session = %d, want 401", rec.Code)
	}
}
