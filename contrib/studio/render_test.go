package studio

import (
	"io/fs"
	"net/http"
	"regexp"
	"strings"
	"testing"
)

func TestTemplatesParse(t *testing.T) {
	t.Parallel()

	pages, err := loadTemplates()
	if err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{"overview", "error"} {
		if pages[name] == nil {
			t.Errorf("page %q is missing", name)
		}
	}
}

// TestSecurity_A05_StudioTemplatesHaveNoInlineCode: the CSP forbids inline
// script, inline style and event handlers, so no template may carry them.
func TestSecurity_A05_StudioTemplatesHaveNoInlineCode(t *testing.T) {
	t.Parallel()

	script := regexp.MustCompile(`(?i)<script\b[^>]*>`)
	handler := regexp.MustCompile(`(?i)\son[a-z]+\s*=`)

	err := fs.WalkDir(templatesFS, "templates", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}

		b, err := fs.ReadFile(templatesFS, p)
		if err != nil {
			return err
		}

		src := string(b)

		for _, tag := range script.FindAllString(src, -1) {
			if !strings.Contains(tag, "src=") {
				t.Errorf("%s: inline <script> (%s)", p, tag)
			}
		}

		if strings.Contains(strings.ToLower(src), ` style="`) || strings.Contains(strings.ToLower(src), " style='") {
			t.Errorf("%s: inline style attribute", p)
		}

		if handler.MatchString(src) {
			t.Errorf("%s: inline event handler", p)
		}

		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestAssetsEmbedded(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"studio.css", "studio.js", "htmx.min.js"} {
		b, err := fs.ReadFile(assetsFS, "assets/"+name)
		if err != nil || len(b) == 0 {
			t.Errorf("asset %s: %d bytes, %v", name, len(b), err)
		}
	}
}

func TestAssetsNeedASessionAndAreNotCached(t *testing.T) {
	t.Parallel()

	s := newStudio(t)

	if rec := serve(s, request(http.MethodGet, "/assets/studio.css")); rec.Code != http.StatusUnauthorized {
		t.Errorf("asset without a session = %d, want 401", rec.Code)
	}

	r := request(http.MethodGet, "/assets/studio.css")
	r.AddCookie(session(t, s))

	rec := serve(s, r)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Header().Get("Content-Type"), "text/css") ||
		rec.Header().Get("Cache-Control") != "no-cache" {
		t.Errorf("asset = %d %v", rec.Code, rec.Header())
	}

	r = request(http.MethodGet, "/assets/nope.css")
	r.AddCookie(session(t, s))

	if rec := serve(s, r); rec.Code != http.StatusNotFound {
		t.Errorf("unknown asset = %d, want 404", rec.Code)
	}
}

func TestOverviewUsesTheLayout(t *testing.T) {
	t.Parallel()

	s := newStudio(t)
	r := request(http.MethodGet, "/")
	r.AddCookie(session(t, s))

	body := serve(s, r).Body.String()

	for _, want := range []string{
		`/assets/studio.css`, `/assets/studio.js`, `/assets/htmx.min.js`, "read-only", "v0.0.0-test",
		"sqlite  storage/app.db", `href="/s/db"`, "online",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("overview lacks %q:\n%s", want, body)
		}
	}
}
