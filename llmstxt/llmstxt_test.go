package llmstxt_test

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/AlexGalhardo/vukem/llmstxt"
	"github.com/AlexGalhardo/vukem/router"
)

func page(s string) func(context.Context) (string, error) {
	return func(context.Context) (string, error) { return s, nil }
}

func doc() llmstxt.Doc {
	return llmstxt.Doc{
		Title:   "Shop",
		Summary: "A shop that sells games.",
		Details: "Prices are in BRL.\r\nStock updates hourly.",
		Sections: []llmstxt.Section{{
			Title: "Docs",
			Links: []llmstxt.Link{
				{
					Title:   "Quick start",
					URL:     "https://shop.test/docs/start",
					Notes:   "install and run",
					Content: page("# Start\n\nRun it."),
				},
				{Title: "API", URL: "/docs/api"},
			},
		}},
		Optional: []llmstxt.Link{
			{Title: "Changelog", URL: "https://shop.test/changelog", Content: page("All changes.")},
		},
		LastModified: time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC),
	}
}

func TestWrite(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	if err := llmstxt.Write(&buf, doc()); err != nil {
		t.Fatal(err)
	}

	want := `# Shop

> A shop that sells games.

Prices are in BRL.
Stock updates hourly.

## Docs

- [Quick start](https://shop.test/docs/start): install and run
- [API](/docs/api)

## Optional

- [Changelog](https://shop.test/changelog)
`
	if diff := cmp.Diff(want, buf.String()); diff != "" {
		t.Errorf("llms.txt diff (-want +got):\n%s", diff)
	}
}

func TestWriteFull(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	if err := llmstxt.WriteFull(t.Context(), &buf, doc()); err != nil {
		t.Fatal(err)
	}

	want := `# Shop

> A shop that sells games.

Prices are in BRL.
Stock updates hourly.

---

# Quick start

Source: https://shop.test/docs/start

# Start

Run it.

---

# Changelog

Source: https://shop.test/changelog

All changes.
`
	if diff := cmp.Diff(want, buf.String()); diff != "" {
		t.Errorf("llms-full.txt diff (-want +got):\n%s", diff)
	}

	t.Run("a failing page stops with its error", func(t *testing.T) {
		t.Parallel()

		boom := errors.New("boom")
		d := doc()
		d.Sections[0].Links[0].Content = func(context.Context) (string, error) { return "", boom }

		if err := llmstxt.WriteFull(t.Context(), &bytes.Buffer{}, d); !errors.Is(err, boom) {
			t.Errorf("WriteFull() = %v, want %v", err, boom)
		}
	})

	t.Run("MaxFullBytes truncates with a note", func(t *testing.T) {
		t.Parallel()

		d := doc()
		d.MaxFullBytes = 120

		var buf bytes.Buffer
		if err := llmstxt.WriteFull(t.Context(), &buf, d); err != nil {
			t.Fatal(err)
		}

		if !strings.HasSuffix(buf.String(), llmstxt.TruncatedNote) || strings.Contains(buf.String(), "All changes.") {
			t.Errorf("truncated output = %q", buf.String())
		}

		if buf.Len() > 120+len(llmstxt.TruncatedNote) {
			t.Errorf("len = %d, want <= %d", buf.Len(), 120+len(llmstxt.TruncatedNote))
		}
	})
}

func TestValidate(t *testing.T) {
	t.Parallel()

	for name, mut := range map[string]func(*llmstxt.Doc){
		"no title":         func(d *llmstxt.Doc) { d.Title = "" },
		"javascript url":   func(d *llmstxt.Doc) { d.Sections[0].Links[0].URL = "javascript:alert(1)" },
		"relative url":     func(d *llmstxt.Doc) { d.Sections[0].Links[0].URL = "docs/api" },
		"empty link title": func(d *llmstxt.Doc) { d.Optional[0].Title = "" },
		"untitled section": func(d *llmstxt.Doc) { d.Sections[0].Title = " " },
		"optional section": func(d *llmstxt.Doc) { d.Sections[0].Title = "Optional" },
	} {
		d := doc()
		mut(&d)

		if err := llmstxt.Write(&bytes.Buffer{}, d); err == nil {
			t.Errorf("%s: Write() = nil error", name)
		}

		if _, err := llmstxt.Handler(d); err == nil {
			t.Errorf("%s: Handler() = nil error", name)
		}
	}
}

// TestSecurity_A03_LLMsTxtMarkdownInjection checks that titles, notes and
// URLs cannot add headings, links or lines to the generated Markdown.
func TestSecurity_A03_LLMsTxtMarkdownInjection(t *testing.T) {
	t.Parallel()

	d := llmstxt.Doc{
		Title:   "Shop\n## Injected",
		Summary: "ok\n\n# Evil",
		Sections: []llmstxt.Section{{
			Title: "Docs\n- [x](https://evil.test)",
			Links: []llmstxt.Link{{
				Title: "a](https://evil.test) [b",
				URL:   "https://shop.test/a b)(c<d>",
				Notes: "n\r\n## Injected x",
			}},
		}},
	}

	var buf bytes.Buffer
	if err := llmstxt.Write(&buf, d); err != nil {
		t.Fatal(err)
	}

	got := buf.String()
	for line := range strings.Lines(got) {
		if strings.HasPrefix(line, "## Injected") || strings.HasPrefix(line, "# Evil") ||
			strings.HasPrefix(line, "- [x]") {
			t.Errorf("injected line %q in:\n%s", line, got)
		}
	}

	if !strings.Contains(
		got,
		`- [a\](https://evil.test) \[b](https://shop.test/a%20b%29%28c%3Cd%3E): n ## Injected x`,
	) {
		t.Errorf("link not escaped:\n%s", got)
	}
}

func TestHandlers(t *testing.T) {
	t.Parallel()

	h, err := llmstxt.Handler(doc())
	if err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/llms.txt", http.NoBody))

	etag := rec.Header().Get("ETag")
	if rec.Code != 200 || rec.Header().Get("Content-Type") != "text/plain; charset=utf-8" || etag == "" ||
		rec.Header().
			Get("Cache-Control") ==
			"" || rec.Header().Get("Last-Modified") != "Sun, 01 Nov 2026 12:00:00 GMT" ||
		!strings.HasPrefix(rec.Body.String(), "# Shop\n") {
		t.Fatalf("llms.txt = %d %v %q", rec.Code, rec.Header(), rec.Body)
	}

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/llms.txt", http.NoBody)
	req.Header.Set("If-None-Match", etag)

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotModified {
		t.Errorf("conditional GET = %d, want 304", rec.Code)
	}

	full, err := llmstxt.FullHandler(doc())
	if err != nil {
		t.Fatal(err)
	}

	rec = httptest.NewRecorder()
	full.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/llms-full.txt", http.NoBody))

	if rec.Code != 200 || rec.Header().Get("Content-Type") != "text/plain; charset=utf-8" ||
		!strings.Contains(rec.Body.String(), "Run it.") {
		t.Fatalf("llms-full.txt = %d %q", rec.Code, rec.Body)
	}

	// A page failing before anything was sent is a 500 with no detail.
	d := doc()
	d.Sections[0].Links[0].Content = func(context.Context) (string, error) { return "", errors.New("db password=x") }

	broken, err := llmstxt.FullHandler(d)
	if err != nil {
		t.Fatal(err)
	}

	rec = httptest.NewRecorder()
	broken.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/llms-full.txt", http.NoBody))

	if rec.Code != http.StatusInternalServerError || strings.Contains(rec.Body.String(), "password") {
		t.Errorf("failing page = %d %q", rec.Code, rec.Body)
	}
}

func TestRegister(t *testing.T) {
	t.Parallel()

	r := router.New(nil)
	if err := llmstxt.Register(r, doc()); err != nil {
		t.Fatal(err)
	}

	for path, want := range map[string]string{"/llms.txt": "- [API](/docs/api)", "/llms-full.txt": "Run it."} {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, http.NoBody))

		if rec.Code != 200 || !strings.Contains(rec.Body.String(), want) {
			t.Errorf("GET %s = %d %q", path, rec.Code, rec.Body)
		}
	}

	bad := doc()
	bad.Title = ""

	if err := llmstxt.Register(router.New(nil), bad); err == nil {
		t.Error("Register(invalid) = nil error")
	}
}
