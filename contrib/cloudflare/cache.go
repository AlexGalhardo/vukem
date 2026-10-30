package cloudflare

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// CachePolicy describes how Cloudflare and browsers cache a response
// (https://developers.cloudflare.com/cache/concepts/cdn-cache-control/,
// read 2026-10-29). Edge goes to Cloudflare-CDN-Cache-Control, which only
// Cloudflare reads and never forwards; Browser goes to Cache-Control.
type CachePolicy struct {
	// Edge is how long Cloudflare keeps the response; 0 means it does not.
	Edge time.Duration
	// Browser is how long browsers keep it; 0 means they revalidate.
	Browser time.Duration
	// StaleWhileRevalidate lets Cloudflare serve a stale copy for this long
	// while it fetches a fresh one.
	StaleWhileRevalidate time.Duration
	// Tags go to Cache-Tag, so Purger.Tags can purge the responses later.
	// Cloudflare strips the header before the visitor sees it.
	Tags []string
}

// maxTagLen is Cloudflare's limit for one cache tag.
const maxTagLen = 1024

// Middleware returns middleware that applies the policy to successful
// GET and HEAD responses. A response is never marked cacheable when the
// request carries Authorization or Cookie, when the response sets a cookie
// or already sets Cache-Control: such a response may belong to one user,
// and caching it at the edge would serve it to everyone.
func (p CachePolicy) Middleware() (func(http.Handler) http.Handler, error) {
	if p.Edge < 0 || p.Browser < 0 || p.StaleWhileRevalidate < 0 {
		return nil, errors.New("cloudflare: negative cache duration")
	}

	for _, t := range p.Tags {
		if t == "" || len(t) > maxTagLen || strings.ContainsAny(t, ", ") || !printable(t) {
			return nil, fmt.Errorf("cloudflare: invalid cache tag %q", t)
		}
	}

	browser := "public, max-age=" + seconds(p.Browser)
	if p.Browser == 0 {
		browser = "public, max-age=0, must-revalidate"
	}

	edge := "max-age=" + seconds(p.Edge)
	if p.StaleWhileRevalidate > 0 {
		edge += ", stale-while-revalidate=" + seconds(p.StaleWhileRevalidate)
	}

	if p.Edge == 0 {
		edge = "no-store"
	}

	tags := strings.Join(p.Tags, ",")

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if (r.Method != http.MethodGet && r.Method != http.MethodHead) ||
				r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
				next.ServeHTTP(w, r)
				return
			}

			next.ServeHTTP(&cacheWriter{ResponseWriter: w, browser: browser, edge: edge, tags: tags}, r)
		})
	}, nil
}

// cacheWriter adds the cache headers just before the status is written,
// once the handler's own headers are known.
type cacheWriter struct {
	http.ResponseWriter
	browser, edge, tags string
	wrote               bool
}

func (w *cacheWriter) WriteHeader(code int) {
	if !w.wrote {
		w.wrote = true
		h := w.Header()

		if code == http.StatusOK && h.Get("Set-Cookie") == "" && h.Get("Cache-Control") == "" {
			h.Set("Cache-Control", w.browser)
			h.Set("Cloudflare-CDN-Cache-Control", w.edge)

			if w.tags != "" {
				h.Set("Cache-Tag", w.tags)
			}
		}
	}

	w.ResponseWriter.WriteHeader(code)
}

func (w *cacheWriter) Write(b []byte) (int, error) {
	if !w.wrote {
		w.WriteHeader(http.StatusOK)
	}

	return w.ResponseWriter.Write(b)
}

// Unwrap lets http.ResponseController reach the underlying writer.
func (w *cacheWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func seconds(d time.Duration) string { return strconv.FormatInt(int64(d/time.Second), 10) }

func printable(s string) bool {
	for _, c := range []byte(s) {
		if c < 0x21 || c > 0x7e {
			return false
		}
	}

	return true
}
