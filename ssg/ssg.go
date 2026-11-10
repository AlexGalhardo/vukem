// Package ssg prerenders an [http.Handler] to static files.
//
// [Generate] performs one in-process GET per page and writes only a 200
// response. It does not open a socket, follow redirects, crawl registered
// routes, or run the Inertia Node SSR server (that stays in inertia/ssr.go).
// The request carries no cookies. Its host defaults to [DefaultHost]. A
// handler can tell the call apart with [FromContext] and should not render
// per-user data: every visitor of the static file would see it.
//
//	report, err := ssg.Generate(ctx, handler, []ssg.Page{
//		{Path: "/"},
//		{Path: "/about"},
//	}, ssg.Options{OutDir: "dist"})
package ssg

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
)

const tmpSuffix = ".ssg-tmp"

const (
	// DefaultHost is the request host when [Options.Host] is empty.
	// .invalid is a reserved TLD, so the value is never a real origin.
	DefaultHost = "ssg.invalid"
	// MaxBody is the largest response body Generate will store.
	MaxBody = 8 << 20
)

var (
	// ErrInvalidPath reports a page path that is not a safe absolute URL path.
	ErrInvalidPath = errors.New("ssg: invalid path")
	// ErrStatus reports a response that is not 200. Nothing was written for that page.
	ErrStatus = errors.New("ssg: response status is not 200")
	// ErrTooLarge reports a body larger than [MaxBody]. Nothing was written for that page.
	ErrTooLarge = errors.New("ssg: body exceeds 8 MiB")
	// ErrEscape reports an output path that would leave [Options.OutDir].
	ErrEscape = errors.New("ssg: path escapes the output directory")
)

// Page is one URL path to prerender.
type Page struct {
	// Path is an absolute URL path, such as "/" or "/about".
	// A scheme, host, query, fragment, NUL, backslash or ".." segment is rejected.
	Path string
}

// Options selects where files are written and which host the request uses.
type Options struct {
	// OutDir is the directory to create and fill. Required.
	OutDir string
	// Host is the request host. Empty means [DefaultHost].
	Host string
}

// File is one written page.
type File struct {
	// Path is relative to OutDir and uses forward slashes.
	Path string
	// Bytes is the size of the file.
	Bytes int64
}

// Report lists the pages Generate wrote, in request order.
// On error it still lists pages written before the failure. Those files
// are left in place. The failing page is not written.
type Report struct {
	Files []File
}

type ctxKey struct{}

// NewContext marks ctx as a prerender. A nil parent uses [context.Background].
func NewContext(parent context.Context) context.Context {
	if parent == nil {
		parent = context.Background()
	}

	return context.WithValue(parent, ctxKey{}, true)
}

// FromContext reports whether ctx was created by [NewContext].
func FromContext(ctx context.Context) bool {
	if ctx == nil {
		return false
	}

	v, ok := ctx.Value(ctxKey{}).(bool)

	return ok && v
}

// Generate writes one index.html per page. "/" becomes index.html,
// "/about" becomes about/index.html and "/docs/cli" becomes
// docs/cli/index.html. OutDir is created when every path is valid.
// A non-200 status, including a redirect, or a body over [MaxBody],
// writes nothing for that page and stops the run.
func Generate(ctx context.Context, h http.Handler, pages []Page, opt Options) (Report, error) {
	if ctx == nil {
		return Report{}, errors.New("ssg: nil context")
	}

	if err := ctx.Err(); err != nil {
		return Report{}, err
	}

	if h == nil {
		return Report{}, errors.New("ssg: nil handler")
	}

	if opt.OutDir == "" {
		return Report{}, errors.New("ssg: output directory is required")
	}

	host, err := cleanHost(opt.Host)
	if err != nil {
		return Report{}, err
	}

	cleaned := make([]string, len(pages))
	rels := make([]string, len(pages))
	// Lower-cased: Windows and macOS file systems treat names that differ by case as one file.
	seen := make(map[string]int, len(pages))

	for i, page := range pages {
		c, err := cleanPath(page.Path)
		if err != nil {
			return Report{}, err
		}

		rel := relFile(c)
		key := strings.ToLower(rel)

		if j, dup := seen[key]; dup {
			return Report{}, fmt.Errorf("%w: %q and %q write the same file", ErrInvalidPath, pages[j].Path, page.Path)
		}

		seen[key] = i
		cleaned[i] = c
		rels[i] = rel
	}

	root, err := prepareOut(opt.OutDir)
	if err != nil {
		return Report{}, err
	}

	defer root.Close()

	var report Report

	for i := range pages {
		if err := ctx.Err(); err != nil {
			return report, err
		}

		body, err := render(ctx, h, host, cleaned[i])
		if err != nil {
			return report, fmt.Errorf("ssg: %s: %w", pages[i].Path, err)
		}

		if err := writePage(root, rels[i], body); err != nil {
			return report, err
		}

		report.Files = append(report.Files, File{Path: rels[i], Bytes: int64(len(body))})
	}

	return report, nil
}

func cleanHost(host string) (string, error) {
	if host == "" {
		return DefaultHost, nil
	}

	if strings.ContainsAny(host, "/\\?#@ ") || strings.Contains(host, "://") {
		return "", fmt.Errorf("%w: host %q", ErrInvalidPath, host)
	}

	return host, nil
}

func cleanPath(raw string) (string, error) {
	if raw == "" || raw[0] != '/' {
		return "", fmt.Errorf("%w: %q", ErrInvalidPath, raw)
	}

	if strings.ContainsRune(raw, 0) || strings.ContainsAny(raw, "\\?#") || strings.Contains(raw, "://") {
		return "", fmt.Errorf("%w: %q", ErrInvalidPath, raw)
	}

	u, err := url.Parse(raw)
	if err != nil || u.Host != "" || u.Scheme != "" || u.RawQuery != "" || u.Fragment != "" ||
		u.Opaque != "" || u.User != nil || u.ForceQuery {
		return "", fmt.Errorf("%w: %q", ErrInvalidPath, raw)
	}

	// url.Parse already decoded the path once; a remaining "%" would be decoded again by the handler's request.
	decoded := u.Path
	if decoded == "" || decoded[0] != '/' || strings.Contains(decoded, "%") {
		return "", fmt.Errorf("%w: %q", ErrInvalidPath, raw)
	}

	if strings.ContainsRune(decoded, 0) || strings.ContainsAny(decoded, "\\?#") {
		return "", fmt.Errorf("%w: %q", ErrInvalidPath, raw)
	}

	for seg := range strings.SplitSeq(strings.Trim(decoded, "/"), "/") {
		if seg == ".." {
			return "", fmt.Errorf("%w: %q", ErrInvalidPath, raw)
		}
	}

	cleaned := path.Clean(decoded)
	if !strings.HasPrefix(cleaned, "/") || strings.Contains(cleaned, "..") {
		return "", fmt.Errorf("%w: %q", ErrInvalidPath, raw)
	}

	for seg := range strings.SplitSeq(strings.Trim(cleaned, "/"), "/") {
		if unsafeSegment(seg) {
			return "", fmt.Errorf("%w: %q", ErrInvalidPath, raw)
		}
	}

	return cleaned, nil
}

// unsafeSegment reports names Windows cannot store as written: a trailing dot or
// space is dropped, and device names (with or without an extension) are reserved.
func unsafeSegment(seg string) bool {
	// ".ssg-tmp" is the temporary name writePage uses beside each page.
	if strings.HasSuffix(seg, ".") || strings.HasSuffix(seg, " ") || strings.HasSuffix(seg, tmpSuffix) {
		return true
	}

	base, _, _ := strings.Cut(seg, ".")
	base = strings.ToLower(strings.TrimRight(base, " "))

	switch base {
	case "con", "prn", "aux", "nul", "conin$", "conout$":
		return true
	}

	digit := strings.TrimPrefix(strings.TrimPrefix(base, "com"), "lpt")
	if digit == base || len([]rune(digit)) != 1 {
		return false
	}

	// The superscript digits are reserved too.
	return strings.ContainsAny(digit, "0123456789\u00b9\u00b2\u00b3")
}

func relFile(cleaned string) string {
	if cleaned == "/" {
		return "index.html"
	}

	return strings.TrimPrefix(cleaned, "/") + "/index.html"
}

func prepareOut(dir string) (*os.Root, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("ssg: output directory: %w", err)
	}

	// Published pages must stay readable by a static server running as another user.
	if err := os.MkdirAll(abs, 0o755); err != nil { //nolint:gosec // G301: world-traversable output dir
		return nil, fmt.Errorf("ssg: output directory: %w", err)
	}

	root, err := os.OpenRoot(abs)
	if err != nil {
		return nil, fmt.Errorf("ssg: output directory: %w", err)
	}

	return root, nil
}

func render(ctx context.Context, h http.Handler, host, pagePath string) ([]byte, error) {
	target := url.URL{Scheme: "http", Host: host, Path: pagePath}

	req, err := http.NewRequestWithContext(NewContext(ctx), http.MethodGet, target.String(), http.NoBody)
	if err != nil {
		return nil, err
	}

	rec := &capture{}
	h.ServeHTTP(rec, req)

	if err := ctx.Err(); err != nil {
		return nil, err
	}

	status := rec.status
	if status == 0 {
		status = http.StatusOK
	}

	if status != http.StatusOK {
		return nil, fmt.Errorf("%w: status %d", ErrStatus, status)
	}

	if rec.over {
		return nil, ErrTooLarge
	}

	return rec.body.Bytes(), nil
}

func writePage(root *os.Root, rel string, body []byte) error {
	if !filepath.IsLocal(rel) {
		return fmt.Errorf("%w: %s", ErrEscape, rel)
	}

	if err := root.MkdirAll(path.Dir(rel), 0o755); err != nil {
		return rootErr(rel, err)
	}

	// Write beside the target and rename, so an existing hard link is replaced, not truncated.
	tmp := rel + tmpSuffix
	if err := root.Remove(tmp); err != nil && !errors.Is(err, os.ErrNotExist) {
		return rootErr(rel, err)
	}

	// Public pages, not secrets: a static server running as another user must be able to read them.
	f, err := root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return rootErr(rel, err)
	}

	_, err = f.Write(body)
	if cerr := f.Close(); err == nil {
		err = cerr
	}

	if err == nil {
		err = root.Rename(tmp, rel)
	}

	if err != nil {
		_ = root.Remove(tmp)

		return rootErr(rel, err)
	}

	return nil
}

// rootErr maps the os.Root escape error, which has no exported sentinel, to ErrEscape.
func rootErr(rel string, err error) error {
	if strings.Contains(err.Error(), "escapes from parent") {
		return fmt.Errorf("%w: %s", ErrEscape, rel)
	}

	return err
}

// capture buffers a response up to MaxBody without a socket.
type capture struct {
	header http.Header
	status int
	body   bytes.Buffer
	over   bool
}

func (c *capture) Header() http.Header {
	if c.header == nil {
		c.header = make(http.Header)
	}

	return c.header
}

func (c *capture) WriteHeader(status int) {
	if c.status != 0 {
		return
	}

	c.status = status
}

func (c *capture) Write(p []byte) (int, error) {
	if c.status == 0 {
		c.status = http.StatusOK
	}

	if c.over || c.body.Len() >= MaxBody || len(p) > MaxBody-c.body.Len() {
		c.over = true

		return 0, ErrTooLarge
	}

	return c.body.Write(p)
}
