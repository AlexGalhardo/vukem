package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Vite's assets stage publishes one directory. A font, an SVG or any other
// file a tool writes beside that directory is not in the image unless the
// Dockerfile copies it too. Secrets, the database and private output stay
// out through .dockerignore, not through a second public copy.
func TestAssetsStageCopiesOnlyTheViteBuild(t *testing.T) {
	t.Parallel()

	body, err := os.ReadFile(filepath.Join("docker", "Dockerfile.tmpl"))
	if err != nil {
		t.Fatal(err)
	}

	var copies []string

	for line := range strings.SplitSeq(string(body), "\n") {
		if strings.Contains(line, "COPY --from=assets") {
			copies = append(copies, strings.TrimSpace(line))
		}
	}

	want := "COPY --from=assets /src/resources/public/build resources/public/build"
	if len(copies) != 1 || copies[0] != want {
		t.Fatalf("assets copies = %q, want only %q", copies, want)
	}

	ignore, err := os.ReadFile(filepath.Join("docker", "dockerignore"))
	if err != nil {
		t.Fatal(err)
	}

	text := string(ignore)
	for _, kept := range []string{".env\n", ".env.*\n", "storage/\n", "/resources/public/build/\n"} {
		if !strings.Contains(text, kept) {
			t.Errorf(".dockerignore does not keep %q out of the build context", strings.TrimRight(kept, "\n"))
		}
	}

	for _, published := range []string{"generated-scores", "brand/"} {
		if strings.Contains(text, published) {
			t.Errorf(".dockerignore excludes custom public dir %q", published)
		}
	}
}

// The starter resources package is the code a generated app ships. In
// production a request for a public file that Vite did not emit is answered
// from the embed, including after the copy on disk has been replaced.
func TestProductionServesCustomPublicAsset(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles the starter resources package")
	}

	repo, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}

	tmpl, err := os.ReadFile(filepath.Join("starter", "resources", "resources.go.tmpl"))
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	sum, err := os.ReadFile(filepath.Join(repo, "go.sum"))
	if err != nil {
		t.Fatal(err)
	}

	writeFiles(t, dir, map[string]string{
		"go.mod": "module example.com/assetfix\n\n" +
			"go 1.27.0\n\n" +
			"require github.com/AlexGalhardo/vukem v0.0.0\n\n" +
			"replace github.com/AlexGalhardo/vukem => " + filepath.ToSlash(repo) + "\n",
		"resources/resources.go":                string(tmpl),
		"resources/views/home.html":             "home",
		"resources/public/brand/mark.txt":       "embedded-mark\n",
		"resources/public/build/.vite/keep.txt": "dotdir",
		"asset_test.go":                         customAssetTest,
	})

	if err := os.WriteFile(filepath.Join(dir, "go.sum"), sum, 0o600); err != nil {
		t.Fatal(err)
	}

	env := append(os.Environ(), "GOWORK=off", "GOFLAGS=-mod=mod")
	run := func(args ...string) {
		t.Helper()

		cmd := exec.CommandContext(t.Context(), args[0], args[1:]...)
		cmd.Dir = dir
		cmd.Env = env

		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}

	run("go", "mod", "tidy")
	run("go", "test", "-count=1", ".")
}

const customAssetTest = `package assetfix

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"example.com/assetfix/resources"
	"github.com/AlexGalhardo/vukem/middleware"
)

func TestCustomAssetFromEmbed(t *testing.T) {
	if err := os.WriteFile(filepath.Join("resources", "public", "brand", "mark.txt"), []byte("host-only\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := os.MkdirAll(filepath.Join("resources", "public", "scratch"), 0o750); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join("resources", "public", "scratch", "note.txt"), []byte("not-embedded\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(".env", []byte("APP_KEY=secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Setenv("APP_ENV", "production")

	h := middleware.StaticFS(resources.Public(), middleware.StaticConfig{Prefix: "/"})(http.NotFoundHandler())

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/brand/mark.txt", nil))

	body, err := io.ReadAll(rec.Body)
	if err != nil {
		t.Fatal(err)
	}

	if rec.Code != http.StatusOK || string(body) != "embedded-mark\n" {
		t.Fatalf("GET /brand/mark.txt = %d %q, want 200 %q", rec.Code, body, "embedded-mark\n")
	}

	for _, path := range []string{"/scratch/note.txt", "/.env", "/storage/database.sqlite"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))

		if rec.Code != http.StatusNotFound {
			t.Fatalf("GET %s = %d, want 404", path, rec.Code)
		}
	}
}
`
