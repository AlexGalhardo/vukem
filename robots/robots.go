// Package robots renders and serves /robots.txt (RFC 9309): groups of
// user agents with their allow and disallow rules, plus Sitemap lines.
// Nothing is served unless the app registers it:
//
//	err := robots.Register(r, robots.File{
//		Groups: []robots.Group{
//			{UserAgents: []string{"*"}, Disallow: []string{"/admin/", "/trap/"}},
//		},
//		Sitemaps: []string{"https://shop.example/sitemap.xml"},
//		NoIndex:  app.Config.NoIndex, // staging: disallow everything
//	})
//
// Values are single-line: a line break or control character in a user
// agent or rule is an error, so input cannot add rules.
package robots

import (
	"bytes"
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
	contentType  = "text/plain; charset=utf-8"
	cacheControl = "public, max-age=3600"
)

// Group is one record of the file: rules for the crawlers it names.
type Group struct {
	// UserAgents are the product tokens the group applies to ("*",
	// "Googlebot"). At least one is required.
	UserAgents []string
	// Allow and Disallow are path rules: "/" followed by the path, with
	// "*" and a final "$" as RFC 9309 wildcards; "#" (a comment) is
	// refused. An empty Disallow entry
	// writes "Disallow:", which allows everything.
	Allow    []string
	Disallow []string
}

// File describes robots.txt.
type File struct {
	Groups []Group
	// Sitemaps are absolute http(s) URLs written as Sitemap lines.
	Sitemaps []string
	// NoIndex replaces the groups with one that disallows everything for
	// every crawler (a staging site); the sitemaps are left out too.
	NoIndex bool
}

// DisallowAll is the body served when File.NoIndex is set.
const DisallowAll = "User-agent: *\nDisallow: /\n"

// Validate reports a group without a user agent, a rule that is not a
// path and any value that would span lines.
func (f File) Validate() error {
	for i, g := range f.Groups {
		if len(g.UserAgents) == 0 {
			return fmt.Errorf("robots: group %d has no user agent", i)
		}

		for _, ua := range g.UserAgents {
			if ua == "" || !oneLine(ua) || strings.ContainsAny(ua, " #") {
				return fmt.Errorf("robots: invalid user agent %q", ua)
			}
		}

		for _, rule := range append(append([]string{}, g.Allow...), g.Disallow...) {
			if rule != "" && (!oneLine(rule) || strings.Contains(rule, "#") ||
				(!strings.HasPrefix(rule, "/") && !strings.HasPrefix(rule, "*"))) {
				return fmt.Errorf("robots: rule %q must be a path starting with / or *", rule)
			}
		}
	}

	for _, s := range f.Sitemaps {
		u, err := url.Parse(s)
		if err != nil || !oneLine(s) || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return fmt.Errorf("robots: sitemap %q must be an absolute http(s) URL", s)
		}
	}

	return nil
}

func oneLine(s string) bool {
	return !strings.ContainsFunc(s, func(r rune) bool { return unicode.IsControl(r) || r == ' ' || r == ' ' })
}

// Write writes f to w, after Validate.
func Write(w io.Writer, f File) error {
	if f.NoIndex {
		_, err := io.WriteString(w, DisallowAll)
		return err
	}

	if err := f.Validate(); err != nil {
		return err
	}

	var b strings.Builder

	for i, g := range f.Groups {
		if i > 0 {
			b.WriteString("\n")
		}

		for _, ua := range g.UserAgents {
			b.WriteString("User-agent: " + ua + "\n")
		}

		for _, a := range g.Allow {
			b.WriteString("Allow: " + a + "\n")
		}

		for _, d := range g.Disallow {
			b.WriteString(strings.TrimSpace("Disallow: "+d) + "\n")
		}
	}

	if len(f.Sitemaps) > 0 && len(f.Groups) > 0 {
		b.WriteString("\n")
	}

	for _, s := range f.Sitemaps {
		b.WriteString("Sitemap: " + s + "\n")
	}

	_, err := io.WriteString(w, b.String())

	return err
}

// Handler serves f, rendered once, with an ETag and Cache-Control,
// answering conditional requests with 304.
func Handler(f File) (http.Handler, error) {
	var buf bytes.Buffer
	if err := Write(&buf, f); err != nil {
		return nil, err
	}

	if buf.Len() == 0 {
		return nil, errors.New("robots: empty file (no groups, sitemaps or NoIndex)")
	}

	body := buf.Bytes()
	sum := sha256.Sum256(body)
	etag := `"` + hex.EncodeToString(sum[:12]) + `"`

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Type", contentType)
		h.Set("Cache-Control", cacheControl)
		h.Set("ETag", etag)
		http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(body))
	}), nil
}

// Register serves f at GET /robots.txt.
func Register(r *router.Router, f File) error {
	h, err := Handler(f)
	if err != nil {
		return err
	}

	r.Handle("GET /robots.txt", h)

	return nil
}
