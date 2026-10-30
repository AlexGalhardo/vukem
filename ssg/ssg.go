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

	for i, page := range pages {
		c, err := cleanPath(page.Path)
		if err != nil {
			return Report{}, err
		}

		cleaned[i] = c
		rels[i] = relFile(c)
	}

	root, err := prepareOut(opt.OutDir)
	if err != nil {
		return Report{}, err
	}

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

	decoded, err := url.PathUnescape(u.Path)
	if err != nil || decoded == "" || decoded[0] != '/' {
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

	return cleaned, nil
}

func relFile(cleaned string) string {
	if cleaned == "/" {
		return "index.html"
	}

	return strings.TrimPrefix(cleaned, "/") + "/index.html"
}

func prepareOut(dir string) (string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", fmt.Errorf("ssg: output directory: %w", err)
	}

	// Published pages must stay readable by a static server running as another user.
	if err := os.MkdirAll(abs, 0o755); err != nil { //nolint:gosec // G301: world-traversable output dir
		return "", fmt.Errorf("ssg: output directory: %w", err)
	}

	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", fmt.Errorf("ssg: output directory: %w", err)
	}

	return resolved, nil
}

func render(ctx context.Context, h http.Handler, host, pagePath string) ([]byte, error) {
	req, err := http.NewRequestWithContext(NewContext(ctx), http.MethodGet, "http://"+host+pagePath, http.NoBody)
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

func writePage(root, rel string, body []byte) error {
	if !filepath.IsLocal(rel) {
		return fmt.Errorf("%w: %s", ErrEscape, rel)
	}

	dest := filepath.Join(root, filepath.FromSlash(rel))
	if err := inside(root, dest); err != nil {
		return err
	}

	parent := filepath.Dir(dest)
	if err := os.MkdirAll(parent, 0o755); err != nil { //nolint:gosec // G301: world-traversable output dir
		return err
	}

	resolvedParent, err := filepath.EvalSymlinks(parent)
	if err != nil {
		return err
	}

	resolved := filepath.Join(resolvedParent, filepath.Base(dest))
	if err := inside(root, resolvedParent); err != nil {
		return err
	}

	if err := inside(root, resolved); err != nil {
		return err
	}

	info, err := os.Lstat(dest)
	if err == nil && info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%w: %s", ErrEscape, rel)
	}

	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}

	// Public pages, not secrets: a static server running as another user must be able to read them.
	if err := os.WriteFile(dest, body, 0o644); err != nil { //nolint:gosec // G306: prerendered HTML is public
		return err
	}

	return nil
}

func inside(root, target string) error {
	rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(target))
	if err != nil || (rel != "." && !filepath.IsLocal(rel)) {
		return fmt.Errorf("%w: %s", ErrEscape, target)
	}

	return nil
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
