package inertia

import (
	"bytes"
	"context"
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

	"github.com/AlexGalhardo/vukem/web"
)

// DefaultSSRURL is where `vukem ssr:start` serves the SSR bundle.
const DefaultSSRURL = "http://127.0.0.1:13714"

// SSRConfig enables server-side rendering through Inertia's Node SSR
// server. Any failure falls back to client-side rendering.
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
	// OnFailure observes failed renders (default: a warning log).
	OnFailure func(ctx context.Context, err SSRError)
}

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
func DisableSSR(ctx context.Context) {
	if s := stateFrom(ctx); s != nil {
		s.noSSR = true
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
// client.
func (in *Inertia) serverRender(c *web.Ctx, st *state, p *Page) (head, body string, ok bool) {
	ctx, r := c.Context(), c.Request()
	s := in.cfg.SSR
	if s == nil || st.noSSR || slices.ContainsFunc(s.Except, func(prefix string) bool {
		return strings.HasPrefix(r.URL.Path, prefix)
	}) {
		return "", "", false
	}

	url := s.URL + "/render"

	if hot := hotURL(s); hot != "" {
		url = hot + "/__inertia_ssr"
	} else if s.Bundle != "" {
		if _, err := os.Stat(s.Bundle); err != nil {
			return "", "", false
		}
	}

	res, ferr := in.postRender(ctx, url, p)
	if ferr != nil {
		ferr.Component, ferr.URL = p.Component, p.URL
		in.reportSSR(ctx, c.Log(), *ferr)

		return "", "", false
	}

	return strings.Join(res.Head, "\n"), res.Body, true
}

func hotURL(s *SSRConfig) string {
	if s.HotURL == nil {
		return ""
	}

	return strings.TrimSuffix(s.HotURL(), "/")
}

func (in *Inertia) postRender(ctx context.Context, url string, p *Page) (*ssrResult, *SSRError) {
	s := in.cfg.SSR

	payload, err := json.Marshal(p)
	if err != nil {
		return nil, &SSRError{Error: err.Error(), Type: "render"}
	}

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
