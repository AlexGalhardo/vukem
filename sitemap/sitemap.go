// Package sitemap streams XML sitemaps (https://www.sitemaps.org/protocol.html)
// with hreflang alternates
// (https://developers.google.com/search/docs/specialty/international/localized-versions#sitemap),
// for catalogues too large to build in memory: a [Writer] writes one
// <urlset> of at most [MaxURLs] URLs and [MaxBytes] bytes, and
// [WriteIndex] writes the sitemap index that lists the chunks.
//
//	w, err := sitemap.NewWriter(gz) // gz := gzip.NewWriter(file)
//	for item := range items {
//		if err := w.Add(sitemap.URL{Loc: item.URL, LastMod: item.UpdatedAt}); errors.Is(err, sitemap.ErrFull) {
//			// close this chunk and start the next one
//		}
//	}
//	err = w.Close()
package sitemap

import (
	"bufio"
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/url"
	"time"
)

// Protocol limits per sitemap file (uncompressed).
const (
	MaxURLs  = 50_000
	MaxBytes = 50 << 20
)

// ErrFull means the URL would push the file past MaxURLs or MaxBytes;
// it was not written. Close the file and start the next chunk.
var ErrFull = errors.New("sitemap: file is full")

// URL is one <url> entry.
type URL struct {
	// Loc is the absolute http(s) URL of the page.
	Loc string
	// LastMod is when the page last changed; zero leaves it out.
	LastMod time.Time
	// Alternates are the page's language versions (hreflang), the page
	// itself included; Lang is a BCP 47 tag or "x-default".
	Alternates []Alternate
}

// Alternate is a language version of a page.
type Alternate struct {
	Lang string
	Href string
}

// Sitemap is one entry of a sitemap index.
type Sitemap struct {
	Loc     string
	LastMod time.Time
}

const (
	urlsetOpen = xml.Header + `<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9" ` +
		`xmlns:xhtml="http://www.w3.org/1999/xhtml">` + "\n"
	urlsetClose = "</urlset>\n"
)

// Writer streams one <urlset>. It is not safe for concurrent use.
type Writer struct {
	w      *bufio.Writer
	count  int
	size   int
	closed bool
}

// NewWriter writes the urlset header to w and returns a Writer for its
// URLs.
func NewWriter(w io.Writer) (*Writer, error) {
	sw := &Writer{w: bufio.NewWriter(w), size: len(urlsetOpen) + len(urlsetClose)}
	if _, err := sw.w.WriteString(urlsetOpen); err != nil {
		return nil, err
	}

	return sw, nil
}

// Add writes u. An invalid URL is an error and writes nothing; ErrFull
// means the file is at its limit.
func (sw *Writer) Add(u URL) error {
	if sw.closed {
		return errors.New("sitemap: Add after Close")
	}

	entry, err := encodeURL(u)
	if err != nil {
		return err
	}

	if sw.count == MaxURLs || sw.size+len(entry) > MaxBytes {
		return ErrFull
	}

	if _, err := sw.w.Write(entry); err != nil {
		return err
	}

	sw.count++
	sw.size += len(entry)

	return nil
}

// Len reports how many URLs were written.
func (sw *Writer) Len() int { return sw.count }

// Close writes the closing tag and flushes; it does not close the
// underlying writer (close the gzip writer and the file yourself).
func (sw *Writer) Close() error {
	if sw.closed {
		return nil
	}

	sw.closed = true

	if _, err := sw.w.WriteString(urlsetClose); err != nil {
		return err
	}

	return sw.w.Flush()
}

// WriteIndex writes a sitemap index listing maps (at most MaxURLs).
func WriteIndex(w io.Writer, maps []Sitemap) error {
	if len(maps) > MaxURLs {
		return fmt.Errorf("sitemap: index of %d sitemaps, the limit is %d", len(maps), MaxURLs)
	}

	bw := bufio.NewWriter(w)
	_, _ = bw.WriteString(xml.Header + `<sitemapindex xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">` + "\n")

	for _, m := range maps {
		if err := checkURL(m.Loc); err != nil {
			return err
		}

		_, _ = bw.WriteString("<sitemap><loc>")
		_ = xml.EscapeText(bw, []byte(m.Loc))
		_, _ = bw.WriteString("</loc>")
		writeLastMod(bw, m.LastMod)
		_, _ = bw.WriteString("</sitemap>\n")
	}

	_, _ = bw.WriteString("</sitemapindex>\n")

	return bw.Flush()
}

func encodeURL(u URL) ([]byte, error) {
	if err := checkURL(u.Loc); err != nil {
		return nil, err
	}

	var b bytes.Buffer

	b.WriteString("<url><loc>")
	_ = xml.EscapeText(&b, []byte(u.Loc))
	b.WriteString("</loc>")
	writeLastMod(&b, u.LastMod)

	for _, a := range u.Alternates {
		if !validLang(a.Lang) {
			return nil, fmt.Errorf("sitemap: invalid hreflang %q", a.Lang)
		}

		if err := checkURL(a.Href); err != nil {
			return nil, err
		}

		b.WriteString(`<xhtml:link rel="alternate" hreflang="` + a.Lang + `" href="`)
		_ = xml.EscapeText(&b, []byte(a.Href))
		b.WriteString(`"/>`)
	}

	b.WriteString("</url>\n")

	return b.Bytes(), nil
}

func writeLastMod(w io.StringWriter, t time.Time) {
	if !t.IsZero() {
		_, _ = w.WriteString("<lastmod>" + t.UTC().Format(time.RFC3339) + "</lastmod>")
	}
}

// checkURL accepts absolute http(s) URLs only: crawlers ignore anything
// else, and it keeps javascript: or relative values out of the file.
func checkURL(s string) error {
	u, err := url.Parse(s)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || len(s) > 2048 {
		return fmt.Errorf("sitemap: %q is not an absolute http(s) URL of at most 2048 bytes", s)
	}

	return nil
}

func validLang(s string) bool {
	if s == "" || len(s) > 35 {
		return false
	}

	for _, c := range []byte(s) {
		if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (c < '0' || c > '9') && c != '-' {
			return false
		}
	}

	return true
}
