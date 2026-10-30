package vukem

import (
	"bytes"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AlexGalhardo/vukem/router"
	"github.com/AlexGalhardo/vukem/ssg"
	"github.com/AlexGalhardo/vukem/web"
)

func TestSSGGenerateCommand(t *testing.T) {
	secret := false

	var buf bytes.Buffer

	app := newTestApp(t, WithStdout(&buf), WithRoutes(func(r *router.Router, _ *App) error {
		r.Get("/", func(c *web.Ctx) error {
			if !ssg.FromContext(c.Context()) {
				return c.String(http.StatusInternalServerError, "live")
			}

			return c.String(http.StatusOK, "home")
		})
		r.Get("/about", func(c *web.Ctx) error {
			return c.String(http.StatusOK, "about-page")
		})
		r.Get("/secret", func(c *web.Ctx) error {
			secret = true

			return c.String(http.StatusOK, "secret")
		})

		return nil
	}))

	parent := t.TempDir()
	dir := filepath.Join(parent, "dist")

	if err := app.Call(
		t.Context(),
		"ssg:generate",
		[]string{"-out", dir, "-path", "/", "-path", "/about"},
	); err != nil {
		t.Fatal(err)
	}

	home, err := os.ReadFile(filepath.Join(dir, "index.html"))
	if err != nil {
		t.Fatal(err)
	}

	if string(home) != "home" {
		t.Fatalf("index.html = %q", home)
	}

	about, err := os.ReadFile(filepath.Join(dir, "about", "index.html"))
	if err != nil {
		t.Fatal(err)
	}

	if string(about) != "about-page" {
		t.Fatalf("about = %q", about)
	}

	if secret {
		t.Fatal("ssg:generate crawled /secret")
	}

	if _, err := os.Stat(filepath.Join(dir, "secret", "index.html")); !os.IsNotExist(err) {
		t.Fatalf("secret file: %v", err)
	}

	if !strings.Contains(buf.String(), "wrote index.html") ||
		!strings.Contains(buf.String(), "wrote about/index.html") {
		t.Fatalf("stdout = %q", buf.String())
	}

	if err := app.Call(t.Context(), "ssg:generate", []string{"-out", dir}); !errors.Is(err, errUsage) {
		t.Fatalf("missing -path = %v, want usage error", err)
	}

	if err := app.Call(
		t.Context(),
		"ssg:generate",
		[]string{"-out", dir, "-path", "/", "extra"},
	); !errors.Is(
		err,
		errUsage,
	) {
		t.Fatalf("extra arg = %v, want usage error", err)
	}

	for _, p := range []string{"../secret", "https://example.com/x", "/../secret"} {
		buf.Reset()

		err := app.Call(t.Context(), "ssg:generate", []string{"-out", dir, "-path", p})
		if err == nil {
			t.Fatalf("path %q was accepted", p)
		}

		if _, statErr := os.Stat(filepath.Join(parent, "secret")); !os.IsNotExist(statErr) {
			t.Fatalf("path %q wrote %s: %v", p, filepath.Join(parent, "secret"), statErr)
		}

		if _, statErr := os.Stat(filepath.Join(parent, "x")); !os.IsNotExist(statErr) {
			t.Fatalf("path %q wrote outside: %v", p, statErr)
		}
	}
}
