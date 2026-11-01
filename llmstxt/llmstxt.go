// Package llmstxt renders /llms.txt and /llms-full.txt
// (https://llmstxt.org): a Markdown index of a site for language models
// and, in the full file, the Markdown of the linked pages one after the
// other. Nothing is served unless the app registers it:
//
//	err := llmstxt.Register(r, llmstxt.Doc{
//		Title:   "Shop",
//		Summary: "A shop that sells games.",
//		Sections: []llmstxt.Section{{Title: "Docs", Links: []llmstxt.Link{
//			{Title: "Quick start", URL: "https://shop.example/docs/start", Content: docs.Markdown("start")},
//		}}},
//	})
//
// Titles, notes and the summary are single-line text: line breaks become
// spaces and link text is escaped, so they cannot add headings or links.
// URLs must be absolute http(s) or root-relative. Details and page content
// are the app's own Markdown and are written as given.
package llmstxt

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode"

	"github.com/AlexGalhardo/vukem/router"
)

const (
	// DefaultMaxFullBytes caps llms-full.txt when Doc.MaxFullBytes is 0.
	DefaultMaxFullBytes = 16 << 20
	// TruncatedNote ends an llms-full.txt cut at the size cap.
	TruncatedNote = "\n\n[llms-full.txt truncated: size limit reached]\n"

	contentType  = "text/plain; charset=utf-8"
	cacheControl = "public, max-age=3600"
)

// Doc describes the site.
type Doc struct {
	// Title is the H1, usually the project or site name. Required.
	Title string
	// Summary is the blockquote under the title: what the site is.
	Summary string
	// Details is optional Markdown written after the summary (paragraphs,
	// lists; no headings). It is trusted app text, written as given.
	Details string
	// Sections are the H2 sections, each a list of links.
	Sections []Section
	// Optional links go under "## Optional": secondary pages a model may
	// skip when its context is short.
	Optional []Link
	// LastModified, when set, is sent as Last-Modified by both handlers.
	LastModified time.Time
	// MaxFullBytes caps llms-full.txt; past it the file ends with
	// TruncatedNote. 0 means DefaultMaxFullBytes.
	MaxFullBytes int64
}

// Section is one H2 section of links.
type Section struct {
	Title string
	Links []Link
}

// Link is one "- [Title](URL): Notes" entry.
type Link struct {
	Title string
	// URL is absolute http(s) or root-relative ("/docs/api").
	URL   string
	Notes string
	// Content returns the page as Markdown for llms-full.txt; nil leaves
	// the page out of it. It runs on every llms-full.txt request.
	Content func(ctx context.Context) (string, error)
}

// Validate reports the first problem with d: a missing title, a section
// without a title or named "Optional", a link without a title or with a
// URL that is not absolute http(s) or root-relative.
func (d *Doc) Validate() error {
	if text(d.Title) == "" {
		return errors.New("llmstxt: Doc.Title is required")
	}

	for _, s := range d.Sections {
		if t := text(s.Title); t == "" || strings.EqualFold(t, "Optional") {
			return fmt.Errorf("llmstxt: section title %q is empty or reserved", s.Title)
		}

		if err := checkLinks(s.Links); err != nil {
			return err
		}
	}

	return checkLinks(d.Optional)
}

func checkLinks(links []Link) error {
	for _, l := range links {
		if text(l.Title) == "" {
			return fmt.Errorf("llmstxt: link %q has no title", l.URL)
		}

		if err := checkURL(l.URL); err != nil {
			return err
		}
	}

	return nil
}

func checkURL(s string) error {
	u, err := url.Parse(s)

	switch {
	case err != nil:
		return fmt.Errorf("llmstxt: invalid URL %q: %w", s, err)
	case (u.Scheme == "http" || u.Scheme == "https") && u.Host != "":
		return nil
	case u.Scheme == "" && u.Host == "" && strings.HasPrefix(s, "/") && !strings.HasPrefix(s, "//"):
		return nil
	}

	return fmt.Errorf("llmstxt: URL %q must be absolute http(s) or root-relative", s)
}

// Write writes the llms.txt index of d to w.
func Write(w io.Writer, d Doc) error {
	if err := d.Validate(); err != nil {
		return err
	}

	var b strings.Builder
	writeHeader(&b, d)

	for _, s := range d.Sections {
		writeSection(&b, text(s.Title), s.Links)
	}

	if len(d.Optional) > 0 {
		writeSection(&b, "Optional", d.Optional)
	}

	_, err := io.WriteString(w, b.String())

	return err
}

func writeHeader(w *strings.Builder, d Doc) {
	w.WriteString("# " + text(d.Title) + "\n")

	if s := text(d.Summary); s != "" {
		w.WriteString("\n> " + s + "\n")
	}

	if s := strings.TrimSpace(strings.ReplaceAll(d.Details, "\r\n", "\n")); s != "" {
		w.WriteString("\n" + s + "\n")
	}
}

func writeSection(w *strings.Builder, title string, links []Link) {
	w.WriteString("\n## " + title + "\n\n")

	for _, l := range links {
		w.WriteString("- [" + linkText(l.Title) + "](" + escapeURL(l.URL) + ")")

		if n := text(l.Notes); n != "" {
			w.WriteString(": " + n)
		}

		w.WriteString("\n")
	}
}

// WriteFull writes llms-full.txt: the header of llms.txt, then the
// Content of every link that has one (sections first, then Optional),
// each after a "---" rule, its title as H1 and a Source line. Output past
// MaxFullBytes is cut and ends with TruncatedNote. A Content error stops
// the writing and is returned.
func WriteFull(ctx context.Context, w io.Writer, d Doc) error {
	if err := d.Validate(); err != nil {
		return err
	}

	limit := d.MaxFullBytes
	if limit <= 0 {
		limit = DefaultMaxFullBytes
	}

	cw := &capWriter{w: w, left: limit}
	bw := bufio.NewWriter(cw)

	var head strings.Builder
	writeHeader(&head, d)

	err := writeFull(ctx, bw, head.String(), allLinks(d))
	if err == nil {
		err = bw.Flush()
	}

	if !errors.Is(err, errFull) {
		return err
	}

	_, err = io.WriteString(w, TruncatedNote)

	return err
}

func writeFull(ctx context.Context, bw *bufio.Writer, head string, links []Link) error {
	if _, err := bw.WriteString(head); err != nil {
		return err
	}

	for _, l := range links {
		if l.Content == nil {
			continue
		}

		if err := ctx.Err(); err != nil {
			return err
		}

		body, err := l.Content(ctx)
		if err != nil {
			return fmt.Errorf("llmstxt: content of %s: %w", l.URL, err)
		}

		page := "\n---\n\n# " + text(l.Title) + "\n\nSource: " + escapeURL(l.URL) + "\n\n" +
			strings.TrimSpace(strings.ReplaceAll(body, "\r\n", "\n")) + "\n"
		if _, err := bw.WriteString(page); err != nil {
			return err
		}
	}

	return nil
}

func allLinks(d Doc) []Link {
	var links []Link
	for _, s := range d.Sections {
		links = append(links, s.Links...)
	}

	return append(links, d.Optional...)
}

var errFull = errors.New("llmstxt: size limit reached")

// capWriter passes at most left bytes to w, then reports errFull.
type capWriter struct {
	w    io.Writer
	left int64
	full bool
}

func (c *capWriter) Write(p []byte) (int, error) {
	if c.full {
		return 0, errFull
	}

	if int64(len(p)) > c.left {
		n, err := c.w.Write(p[:c.left])
		c.left, c.full = 0, true

		if err != nil {
			return n, err
		}

		return n, errFull
	}

	n, err := c.w.Write(p)
	c.left -= int64(n)

	return n, err
}

// Handler serves llms.txt, rendered once, with an ETag, Cache-Control and
// Last-Modified (when set), answering conditional requests with 304.
func Handler(d Doc) (http.Handler, error) {
	var buf bytes.Buffer
	if err := Write(&buf, d); err != nil {
		return nil, err
	}

	body := buf.Bytes()
	sum := sha256.Sum256(body)
	etag := `"` + hex.EncodeToString(sum[:12]) + `"`

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Type", contentType)
		h.Set("Cache-Control", cacheControl)
		h.Set("ETag", etag)
		http.ServeContent(w, r, "", d.LastModified, bytes.NewReader(body))
	}), nil
}

// FullHandler streams llms-full.txt, calling every Content per request.
// A Content error before the first byte reached the client is a bare 500;
// after that the connection is aborted, so a cut file never looks whole.
func FullHandler(d Doc) (http.Handler, error) {
	if err := d.Validate(); err != nil {
		return nil, err
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !d.LastModified.IsZero() {
			w.Header().Set("Last-Modified", d.LastModified.UTC().Format(http.TimeFormat))
		}

		sw := &startWriter{w: w}
		if err := WriteFull(r.Context(), sw, d); err != nil {
			if sw.started {
				panic(http.ErrAbortHandler)
			}

			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		}
	}), nil
}

// startWriter sends the headers with the first body byte.
type startWriter struct {
	w       http.ResponseWriter
	started bool
}

func (s *startWriter) Write(p []byte) (int, error) {
	if !s.started {
		s.started = true
		h := s.w.Header()
		h.Set("Content-Type", contentType)
		h.Set("Cache-Control", cacheControl)
		h.Set("X-Content-Type-Options", "nosniff")
	}

	return s.w.Write(p)
}

// Register serves d at GET /llms.txt and GET /llms-full.txt.
func Register(r *router.Router, d Doc) error {
	index, err := Handler(d)
	if err != nil {
		return err
	}

	full, err := FullHandler(d)
	if err != nil {
		return err
	}

	r.Handle("GET /llms.txt", index)
	r.Handle("GET /llms-full.txt", full)

	return nil
}

// text makes s one line: whitespace runs (line breaks included) become one
// space and other control or format characters are dropped.
func text(s string) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return ' '
		}

		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return -1
		}

		return r
	}, s)

	return strings.Join(strings.Fields(s), " ")
}

var linkEscaper = strings.NewReplacer(`\`, `\\`, `[`, `\[`, `]`, `\]`)

func linkText(s string) string { return linkEscaper.Replace(text(s)) }

var urlEscaper = strings.NewReplacer(" ", "%20", "(", "%28", ")", "%29", "<", "%3C", ">", "%3E")

func escapeURL(s string) string { return urlEscaper.Replace(s) }
