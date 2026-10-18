package web

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net"
	"net/http"
	"net/url"
	"path"
	"strings"

	"github.com/AlexGalhardo/vukem/internal/disposition"
)

var errNoURLs = errors.New("web: no URL generator configured")

// ResponseWriter wraps http.ResponseWriter to record the status and whether
// the response was written. It supports http.ResponseController.
type ResponseWriter struct {
	http.ResponseWriter

	status  int
	pending int // set by Ctx.Status, sent by the next WriteHeader
	written bool
	size    int64
}

// WriteHeader records and writes the status. A status set earlier with
// Ctx.Status replaces a 200 (the implicit status of Write, and the default
// of helpers such as Render, View or inertia.Render); any other code (a
// redirect, 204, 304, an explicit 202) is sent as given.
func (w *ResponseWriter) WriteHeader(code int) {
	if w.written {
		return
	}

	if w.pending != 0 && code == http.StatusOK {
		code = w.pending
	}

	w.pending = 0

	w.status = code
	w.written = true
	w.ResponseWriter.WriteHeader(code)
}

// Write writes the body, sending a 200 status first when needed.
func (w *ResponseWriter) Write(b []byte) (int, error) {
	if !w.written {
		w.WriteHeader(http.StatusOK)
	}

	n, err := w.ResponseWriter.Write(b)
	w.size += int64(n)

	return n, err
}

// WriteString writes s like Write, without copying it into a []byte when
// the underlying writer is an io.StringWriter.
func (w *ResponseWriter) WriteString(s string) (int, error) {
	if !w.written {
		w.WriteHeader(http.StatusOK)
	}

	n, err := io.WriteString(w.ResponseWriter, s)
	w.size += int64(n)

	return n, err
}

// Status returns the written status (0 when nothing was written).
func (w *ResponseWriter) Status() int { return w.status }

// Written reports whether the status line was sent.
func (w *ResponseWriter) Written() bool { return w.written }

// Size returns the number of body bytes written.
func (w *ResponseWriter) Size() int64 { return w.size }

// Unwrap supports http.ResponseController.
func (w *ResponseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// Flush sends the status (a pending Ctx.Status, else 200) and flushes the
// underlying writer. Without it, http.ResponseController would flush the
// unwrapped writer and send an implicit 200 behind this one's back.
func (w *ResponseWriter) Flush() { _ = w.FlushError() }

// FlushError is Flush reporting http.ErrNotSupported when the underlying
// writer cannot flush (http.ResponseController prefers it).
func (w *ResponseWriter) FlushError() error {
	if !w.written {
		w.WriteHeader(http.StatusOK)
	}

	return http.NewResponseController(w.ResponseWriter).Flush()
}

// Hijack hands the connection to the caller (WebSocket upgrades). No status
// is sent: the hijacker writes its own response, and the writer counts as
// written so nothing is sent on the connection afterwards.
func (w *ResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	conn, rw, err := http.NewResponseController(w.ResponseWriter).Hijack()
	if err == nil {
		w.written = true
		w.pending = 0
	}

	return conn, rw, err
}

// Status sets the response status without sending it: the next helper
// that writes the response with the default 200 (JSON(200, v), Render,
// View, inertia.Render, a plain Write) sends it instead, with its headers.
// c.Status(201).JSON(200, v) answers 201 with Content-Type
// application/json. Any other status wins over it: a redirect, NoContent,
// a 304, JSON(202, v). A handler that returns nil without writing sends
// the status alone; a returned error or a panic answers with its own
// status.
func (c *Ctx) Status(code int) *Ctx {
	c.w.pending = code
	return c
}

// String writes a text/plain response.
func (c *Ctx) String(status int, s string) error {
	c.w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	c.w.WriteHeader(status)
	_, err := c.w.WriteString(s)

	return err
}

// HTML writes a text/html response from a trusted string. Never pass user
// input: use views (html/template) for escaping.
func (c *Ctx) HTML(status int, html string) error {
	c.w.Header().Set("Content-Type", "text/html; charset=utf-8")
	c.w.WriteHeader(status)
	_, err := c.w.WriteString(html)

	return err
}

// JSON writes v as JSON with status.
func (c *Ctx) JSON(status int, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("web: encode json: %w", err)
	}

	c.w.Header().Set("Content-Type", "application/json")
	c.w.WriteHeader(status)
	_, err = c.w.Write(b)

	return err
}

// Created writes v as JSON with 201 and a Location header.
func (c *Ctx) Created(location string, v any) error {
	if location != "" {
		c.w.Header().Set("Location", location)
	}

	return c.JSON(http.StatusCreated, v)
}

// Accepted writes v as JSON with 202.
func (c *Ctx) Accepted(v any) error { return c.JSON(http.StatusAccepted, v) }

// NoContent writes 204.
func (c *Ctx) NoContent() error {
	c.w.WriteHeader(http.StatusNoContent)
	return nil
}

// Blob writes raw bytes with a content type.
func (c *Ctx) Blob(status int, contentType string, b []byte) error {
	c.w.Header().Set("Content-Type", contentType)
	c.w.WriteHeader(status)
	_, err := c.w.Write(b)

	return err
}

// Stream copies r to the response.
func (c *Ctx) Stream(status int, contentType string, r io.Reader) error {
	c.w.Header().Set("Content-Type", contentType)
	c.w.WriteHeader(status)
	_, err := io.Copy(c.w, r)

	return err
}

// Redirect redirects to target. Unsafe methods get 303 See Other so the
// browser follows with GET; GET/HEAD get 302 Found. Use RedirectStatus for
// other codes. htmx requests receive HX-Location instead (navigation without
// a full reload).
func (c *Ctx) Redirect(target string) error {
	status := http.StatusFound
	if c.r.Method != http.MethodGet && c.r.Method != http.MethodHead {
		status = http.StatusSeeOther
	}

	return c.RedirectStatus(status, target)
}

// RedirectStatus redirects with an explicit 3xx status.
func (c *Ctx) RedirectStatus(status int, target string) error {
	if c.IsHTMX() && c.r.Header.Get("HX-History-Restore-Request") != "true" {
		c.w.Header().Set("HX-Location", target)
		c.w.WriteHeader(http.StatusOK)

		return nil
	}

	http.Redirect(c.w, c.r, target, status)

	return nil
}

// RedirectRoute redirects to a named route.
func (c *Ctx) RedirectRoute(name string, params ...any) error {
	u, err := c.URL(name, params...)
	if err != nil {
		return err
	}

	return c.Redirect(u)
}

// Back redirects to the Referer when it is same-origin, else to fallback.
// Off-site referers are ignored (open-redirect protection, A01).
func (c *Ctx) Back(fallback string) error {
	return c.Redirect(SafeRedirect(c.r, c.r.Referer(), fallback))
}

// SafeRedirect returns target when it is a relative path or has the same
// host as r, and fallback otherwise.
func SafeRedirect(r *http.Request, target, fallback string) string {
	if target == "" {
		return fallback
	}

	u, err := url.Parse(target)
	if err != nil {
		return fallback
	}

	switch {
	case u.Scheme == "" && u.Host == "":
		if isLocalPath(target) && isLocalPath(path.Clean(u.Path)) {
			return target
		}
	case (u.Scheme == "http" || u.Scheme == "https") && u.Host == r.Host:
		// "https://host//evil.com" has the path "//evil.com", which a
		// browser reads as scheme-relative: check the result, not the input.
		if p := u.RequestURI(); isLocalPath(p) && isLocalPath(path.Clean(u.Path)) {
			return p
		}
	}

	return fallback
}

// isLocalPath reports whether p is an absolute path on the current origin:
// browsers treat a leading "//" or "/\" as another host. Callers also
// check the cleaned path, because http.Redirect cleans relative targets
// ("/./\evil.com" becomes "/\evil.com").
func isLocalPath(p string) bool {
	return strings.HasPrefix(p, "/") && !strings.HasPrefix(p, "//") && !strings.HasPrefix(p, `/\`)
}

// File serves name from fsys (with Range and If-Modified-Since support).
func (c *Ctx) File(fsys fs.FS, name string) error { return c.file(fsys, name, nil) }

// file serves name; ready runs once the file is known to be servable,
// before any byte is written.
func (c *Ctx) file(fsys fs.FS, name string, ready func()) error {
	f, err := fsys.Open(name)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return errNotFound()
		}

		return err
	}
	defer f.Close()

	st, err := f.Stat()
	if err != nil {
		return err
	}

	if st.IsDir() {
		return errNotFound()
	}

	if ready != nil {
		ready()
	}

	rs, ok := f.(io.ReadSeeker)
	if !ok {
		b, err := io.ReadAll(f)
		if err != nil {
			return err
		}

		ct := mime.TypeByExtension(path.Ext(name))
		if ct == "" {
			ct = http.DetectContentType(b)
		}

		return c.Blob(http.StatusOK, ct, b)
	}

	http.ServeContent(c.w, c.r, st.Name(), st.ModTime(), rs)

	return nil
}

// Download serves name from fsys as an attachment called filename (the
// base of name when empty). The name is sanitized like
// export.ContentDisposition: last path element only, no control or bidi
// characters, an RFC 5987 filename* when it is not ASCII. The header is
// set only once the file opened, so an error page is not an attachment.
func (c *Ctx) Download(fsys fs.FS, name, filename string) error {
	if filename == "" {
		filename = path.Base(name)
	}

	return c.file(fsys, name, func() {
		c.w.Header().Set("Content-Disposition", disposition.Attachment(filename))
	})
}
