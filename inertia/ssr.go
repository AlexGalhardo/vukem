package inertia

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/AlexGalhardo/vukem/errs"
	"github.com/AlexGalhardo/vukem/web"
)

// DefaultSSRURL is where `vukem ssr:start` serves the SSR bundle.
const DefaultSSRURL = "http://127.0.0.1:13714"

// SSRConfig enables server-side rendering through Inertia's Node SSR
// server. A failure falls back to client-side rendering, unless the page is
// strict (Strict or RequireSSR): it then answers 503 Service Unavailable
// with Retry-After.
type SSRConfig struct {
	// URL of the production SSR server (default DefaultSSRURL).
	URL string
	// HotURL returns the Vite dev server URL while it runs (vite.HotURL);
	// renders then go to its /__inertia_ssr endpoint and Bundle is not
	// checked.
	HotURL func() string
	// Bundle, when set, is the SSR bundle path; SSR is skipped while it is
	// missing (e.g. before the first build).
	Bundle string
	// Timeout bounds each render and health check (default 3s).
	Timeout time.Duration
	// Except lists path prefixes rendered on the client only.
	Except []string
	// Client sends the requests (default: a client with Timeout).
	Client *http.Client
	// OnFailure observes failed renders (default: a warning log), strict
	// ones included.
	OnFailure func(ctx context.Context, err SSRError)
	// Strict makes every server-rendered page answer 503 with Retry-After
	// instead of falling back to client-side rendering, a missing Bundle
	// included; Except and DisableSSR still opt pages out. With Strict,
	// vukem.WithInertia registers SSRHealthy as the "inertia-ssr" readiness
	// check. RequireSSR makes a single request strict.
	Strict bool
	// Cache, when set, keeps successful renders for CacheTTL, keyed by a
	// SHA-256 of the exact page object sent to the SSR server (component,
	// props, URL, asset version), so only an identical page reuses a
	// render and personalised props never leak across users. A
	// cache.Store (e.g. cache.NewMemory(n), bounded) satisfies it. The
	// CSP nonce is never part of the render (the root view adds it), so a
	// cached render is nonce-safe. Not used for the Vite dev server.
	// Renders must be deterministic for their page object.
	Cache SSRCache
	// CacheTTL is how long a render stays cached (default 1m).
	CacheTTL time.Duration
	// CacheWhen, when set, limits caching to the pages it accepts (for
	// example anonymous visits); nil caches every server-rendered page.
	CacheWhen func(c *web.Ctx, p *Page) bool
}

// SSRCache stores rendered pages; cache.Store implements it.
type SSRCache interface {
	Get(ctx context.Context, key string) ([]byte, bool, error)
	Set(ctx context.Context, key string, value []byte, ttl time.Duration) error
}

// ssrRetryAfter is the Retry-After (seconds) of a strict SSR failure: long
// enough for `vukem ssr:start` to restart a crashed server.
const ssrRetryAfter = "5"

// SSRError describes a failed render, as reported by the SSR server
// ("browser-api", "component-resolution", "render") or "connection" when it
// could not be reached.
type SSRError struct {
	Error          string `json:"error"`
	Type           string `json:"type"`
	Hint           string `json:"hint,omitempty"`
	BrowserAPI     string `json:"browserApi,omitempty"`
	Stack          string `json:"stack,omitempty"`
	SourceLocation string `json:"sourceLocation,omitempty"`
	Component      string `json:"component,omitempty"`
	URL            string `json:"url,omitempty"`
}

// DisableSSR renders the page for the request carrying ctx on the client.
// It wins over Strict and RequireSSR.
func DisableSSR(ctx context.Context) {
	if s := stateFrom(ctx); s != nil {
		s.noSSR = true
	}
}

// RequireSSR makes the page for the request carrying ctx strict: when SSR is
// configured and the render fails, the response is 503 with Retry-After
// instead of a client-rendered page (for pages that must be indexable).
// Without an SSRConfig it does nothing.
func RequireSSR(ctx context.Context) {
	if s := stateFrom(ctx); s != nil {
		s.needSSR = true
	}
}

func (c *SSRConfig) withDefaults() *SSRConfig {
	out := *c
	if out.URL == "" {
		out.URL = DefaultSSRURL
	}

	out.URL = strings.TrimSuffix(out.URL, "/")

	if out.Timeout <= 0 {
		out.Timeout = 3 * time.Second
	}

	if out.Client == nil {
		out.Client = &http.Client{Timeout: out.Timeout}
	}

	if out.CacheTTL <= 0 {
		out.CacheTTL = time.Minute
	}

	return &out
}

// SSRHealthy checks the production SSR server's /health endpoint.
func (in *Inertia) SSRHealthy(ctx context.Context) error {
	s := in.cfg.SSR
	if s == nil {
		return errors.New("inertia: SSR is not configured")
	}

	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.URL+"/health", http.NoBody)
	if err != nil {
		return err
	}

	res, err := s.Client.Do(req)
	if err != nil {
		return fmt.Errorf("inertia: SSR health: %w", err)
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("inertia: SSR health: %s", res.Status)
	}

	return nil
}

type ssrResult struct {
	Head []string `json:"head"`
	Body string   `json:"body"`
}

// serverRender returns the SSR head and body, or ok=false to render on the
// client. A failed strict render returns a 503 error instead.
func (in *Inertia) serverRender(c *web.Ctx, st *state, p *Page) (head, body string, ok bool, err error) {
	ctx, r := c.Context(), c.Request()
	s := in.cfg.SSR
	if s == nil || st.noSSR || slices.ContainsFunc(s.Except, func(prefix string) bool {
		return strings.HasPrefix(r.URL.Path, prefix)
	}) {
		return "", "", false, nil
	}

	strict := s.Strict || st.needSSR
	url := s.URL + "/render"

	var (
		res  *ssrResult
		ferr *SSRError
	)

	if hot := hotURL(s); hot != "" {
		url = hot + "/__inertia_ssr"
	} else if s.Bundle != "" {
		if _, serr := os.Stat(s.Bundle); serr != nil {
			if !strict {
				return "", "", false, nil
			}

			ferr = &SSRError{Error: "SSR bundle not found", Type: "bundle"}
		}
	}

	if ferr == nil {
		res, ferr = in.cachedRender(c, url, p)
	}

	if ferr != nil {
		ferr.Component, ferr.URL = p.Component, p.URL
		in.reportSSR(ctx, c.Log(), *ferr)

		if strict {
			return "", "", false, errs.Wrap(http.StatusServiceUnavailable,
				fmt.Errorf("inertia: strict SSR failed (%s): %s", ferr.Type, ferr.Error)).
				WithHeader("Retry-After", ssrRetryAfter)
		}

		return "", "", false, nil
	}

	return strings.Join(res.Head, "\n"), res.Body, true, nil
}

func hotURL(s *SSRConfig) string {
	if s.HotURL == nil {
		return ""
	}

	return strings.TrimSuffix(s.HotURL(), "/")
}

// cachedRender serves a render from SSRConfig.Cache or stores a fresh
// successful one. Cache errors are logged and never fail the page.
func (in *Inertia) cachedRender(c *web.Ctx, url string, p *Page) (*ssrResult, *SSRError) {
	payload, err := json.Marshal(p)
	if err != nil {
		return nil, &SSRError{Error: err.Error(), Type: "render"}
	}

	s := in.cfg.SSR
	if s.Cache == nil || hotURL(s) != "" || (s.CacheWhen != nil && !s.CacheWhen(c, p)) {
		return in.postRender(c.Context(), url, payload)
	}

	ctx := c.Context()
	sum := sha256.Sum256(payload)
	key := "inertia:ssr:" + hex.EncodeToString(sum[:])

	if b, ok, err := s.Cache.Get(ctx, key); err != nil {
		c.Log().WarnContext(ctx, "inertia: SSR cache read failed", "err", err)
	} else if ok {
		var res ssrResult
		if json.Unmarshal(b, &res) == nil && res.Body != "" {
			return &res, nil
		}
	}

	res, ferr := in.postRender(ctx, url, payload)
	if ferr != nil {
		return nil, ferr
	}

	if b, err := json.Marshal(res); err == nil {
		if err := s.Cache.Set(ctx, key, b, s.CacheTTL); err != nil {
			c.Log().WarnContext(ctx, "inertia: SSR cache write failed", "err", err)
		}
	}

	return res, nil
}

func (in *Inertia) postRender(ctx context.Context, url string, payload []byte) (*ssrResult, *SSRError) {
	s := in.cfg.SSR

	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return nil, &SSRError{Error: err.Error(), Type: "connection"}
	}

	req.Header.Set("Content-Type", "application/json")

	res, err := s.Client.Do(req)
	if err != nil {
		return nil, &SSRError{Error: err.Error(), Type: "connection"}
	}
	defer res.Body.Close()

	b, err := io.ReadAll(io.LimitReader(res.Body, 8<<20))
	if err != nil {
		return nil, &SSRError{Error: err.Error(), Type: "connection"}
	}

	if res.StatusCode != http.StatusOK {
		e := SSRError{Error: res.Status, Type: "render"}
		_ = json.Unmarshal(b, &e)

		return nil, &e
	}

	var out ssrResult
	if err := json.Unmarshal(b, &out); err != nil || out.Body == "" {
		return nil, &SSRError{Error: "invalid SSR response", Type: "render"}
	}

	return &out, nil
}

func (in *Inertia) reportSSR(ctx context.Context, log *slog.Logger, e SSRError) {
	if in.cfg.SSR.OnFailure != nil {
		in.cfg.SSR.OnFailure(ctx, e)
		return
	}

	log.WarnContext(ctx, "inertia: SSR failed, rendering on the client",
		"type", e.Type, "error", e.Error, "component", e.Component, "hint", e.Hint)
}
