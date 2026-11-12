package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const maxBody = 128 << 20

// urls are the registries the tool talks to; tests point them at httptest.
type urls struct {
	npm, proxy, htmx    string // htmx has one %s: the version
	hub, registry, auth string // Docker Hub tags, registry and token endpoints
	github              string
}

var defaultURLs = urls{
	npm:   "https://registry.npmjs.org",
	proxy: "https://proxy.golang.org",
	htmx:  "https://cdn.jsdelivr.net/npm/htmx.org@%s/dist/htmx.min.js",
	hub:   "https://hub.docker.com", registry: "https://registry-1.docker.io", auth: "https://auth.docker.io/token",
	github: "https://api.github.com",
}

// info is what a registry knows about one package: the version the registry
// itself calls latest ("" when it has no such notion) and, per version, its
// peerDependencies.
type info struct {
	latest   string
	versions map[string]map[string]string
}

type client struct {
	http *http.Client
	urls urls
	// ghToken authenticates GitHub API calls (rate limit); empty is anonymous.
	ghToken string
}

func newClient(u urls) *client {
	return &client{http: &http.Client{Timeout: 60 * time.Second}, urls: u}
}

func (c *client) get(ctx context.Context, rawURL, accept string) ([]byte, error) {
	resp, err := c.do(ctx, http.MethodGet, rawURL, map[string]string{"Accept": accept})
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	return io.ReadAll(io.LimitReader(resp.Body, maxBody))
}

func (c *client) head(ctx context.Context, rawURL string, headers map[string]string) (http.Header, error) {
	resp, err := c.do(ctx, http.MethodHead, rawURL, headers)
	if err != nil {
		return nil, err
	}

	_ = resp.Body.Close()

	return resp.Header, nil
}

func (c *client) do(ctx context.Context, method, rawURL string, headers map[string]string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, rawURL, http.NoBody)
	if err != nil {
		return nil, err
	}

	for k, v := range headers {
		if v != "" {
			req.Header.Set(k, v)
		}
	}

	if c.ghToken != "" && strings.HasPrefix(rawURL, c.urls.github) {
		req.Header.Set("Authorization", "Bearer "+c.ghToken)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()

		return nil, fmt.Errorf("%s %s: %s", method, rawURL, resp.Status)
	}

	return resp, nil
}

// npm reads the abbreviated packument, which carries dist-tags and each
// version's peerDependencies without the full metadata.
func (c *client) npm(ctx context.Context, pkg string) (info, error) {
	b, err := c.get(ctx, c.urls.npm+"/"+url.PathEscape(pkg), "application/vnd.npm.install-v1+json")
	if err != nil {
		return info{}, err
	}

	var doc struct {
		DistTags map[string]string `json:"dist-tags"`
		Versions map[string]struct {
			Peers map[string]string `json:"peerDependencies"`
		} `json:"versions"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		return info{}, fmt.Errorf("npm %s: %w", pkg, err)
	}

	out := info{latest: doc.DistTags["latest"], versions: map[string]map[string]string{}}
	for v, d := range doc.Versions {
		out.versions[v] = d.Peers
	}

	return out, nil
}

// goModule lists the versions the module proxy serves.
func (c *client) goModule(ctx context.Context, module string) (info, error) {
	var esc strings.Builder

	for _, r := range module {
		if r >= 'A' && r <= 'Z' {
			esc.WriteString("!" + strings.ToLower(string(r)))
		} else {
			esc.WriteRune(r)
		}
	}

	b, err := c.get(ctx, c.urls.proxy+"/"+esc.String()+"/@v/list", "")
	if err != nil {
		return info{}, err
	}

	out := info{versions: map[string]map[string]string{}}
	for v := range strings.FieldsSeq(string(b)) {
		out.versions[v] = nil
	}

	return out, nil
}
