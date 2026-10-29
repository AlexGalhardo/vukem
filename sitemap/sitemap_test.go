package sitemap_test

import (
	"bytes"
	"encoding/xml"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/AlexGalhardo/vukem/sitemap"
)

func TestWriter(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	w, err := sitemap.NewWriter(&buf)
	if err != nil {
		t.Fatal(err)
	}

	when := time.Date(2026, 10, 29, 12, 0, 0, 0, time.FixedZone("BRT", -3*3600))
	if err := w.Add(sitemap.URL{
		Loc:     "https://hexascore.app/games/zelda?a=1&b=2",
		LastMod: when,
		Alternates: []sitemap.Alternate{
			{Lang: "pt-BR", Href: "https://hexascore.app/pt-BR/games/zelda"},
			{Lang: "x-default", Href: "https://hexascore.app/games/zelda"},
		},
	}); err != nil {
		t.Fatal(err)
	}

	if err := w.Add(sitemap.URL{Loc: "https://hexascore.app/"}); err != nil {
		t.Fatal(err)
	}

	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	out := buf.String()
	for _, want := range []string{
		`<loc>https://hexascore.app/games/zelda?a=1&amp;b=2</loc>`,
		`<lastmod>2026-10-29T15:00:00Z</lastmod>`,
		`<xhtml:link rel="alternate" hreflang="pt-BR" href="https://hexascore.app/pt-BR/games/zelda"/>`,
		`<url><loc>https://hexascore.app/</loc></url>`,
		"</urlset>\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("sitemap lacks %s:\n%s", want, out)
		}
	}

	if w.Len() != 2 {
		t.Errorf("Len() = %d, want 2", w.Len())
	}

	// Well-formed XML.
	d := xml.NewDecoder(strings.NewReader(out))
	for {
		if _, err := d.Token(); err != nil {
			if err.Error() != "EOF" {
				t.Fatalf("invalid XML: %v", err)
			}

			break
		}
	}
}

func TestWriterRejectsInvalidAndStopsWhenFull(t *testing.T) {
	t.Parallel()

	w, _ := sitemap.NewWriter(&bytes.Buffer{})

	for _, bad := range []sitemap.URL{
		{Loc: "/relative"},
		{Loc: "javascript:alert(1)"},
		{Loc: "https://x.example/" + strings.Repeat("a", 2048)},
		{Loc: "https://x.example/", Alternates: []sitemap.Alternate{{Lang: `en" onload="x`, Href: "https://x.example/"}}},
		{Loc: "https://x.example/", Alternates: []sitemap.Alternate{{Lang: "en", Href: "ftp://x.example/"}}},
	} {
		if err := w.Add(bad); err == nil || errors.Is(err, sitemap.ErrFull) {
			t.Errorf("Add(%+v) = %v, want a validation error", bad, err)
		}
	}

	for range sitemap.MaxURLs {
		if err := w.Add(sitemap.URL{Loc: "https://x.example/"}); err != nil {
			t.Fatal(err)
		}
	}

	if err := w.Add(sitemap.URL{Loc: "https://x.example/"}); !errors.Is(err, sitemap.ErrFull) {
		t.Fatalf("Add(50001st) = %v, want ErrFull", err)
	}
}

func TestWriteIndex(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	err := sitemap.WriteIndex(&buf, []sitemap.Sitemap{
		{Loc: "https://hexascore.app/sitemaps/games-1.xml.gz", LastMod: time.Date(2026, 10, 29, 0, 0, 0, 0, time.UTC)},
	})
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(buf.String(), "<sitemap><loc>https://hexascore.app/sitemaps/games-1.xml.gz</loc>"+
		"<lastmod>2026-10-29T00:00:00Z</lastmod></sitemap>") {
		t.Errorf("index = %s", buf.String())
	}

	if err := sitemap.WriteIndex(&buf, []sitemap.Sitemap{{Loc: "nope"}}); err == nil {
		t.Error("WriteIndex(relative) = nil error")
	}
}
