package cloudflare

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// APIURL is the Cloudflare API base URL.
const APIURL = "https://api.cloudflare.com/client/v4"

// purgeBatch is how many items one purge call carries.
// shortcut: 30 is the lowest per-call limit across plans; raise it per
// plan when purges hit the API rate limit.
const purgeBatch = 30

var zoneIDRe = regexp.MustCompile(`^[0-9a-f]{32}$`)

// Purger purges Cloudflare's cache for one zone
// (https://developers.cloudflare.com/api/resources/cache/methods/purge/,
// read 2026-10-29). Use an API token limited to the "Zone > Cache Purge"
// permission on that zone. Safe for concurrent use; call it from a job so
// a slow API does not hold a request.
type Purger struct {
	zoneID string
	token  string

	// BaseURL is the API base; tests point it at a fake server.
	BaseURL string
	// Client sends the requests; nil uses a client with a 10s timeout.
	Client *http.Client
}

// NewPurger returns a Purger for zoneID (32 hex characters) with an API
// token. The token is never part of an error.
func NewPurger(zoneID, token string) (*Purger, error) {
	if !zoneIDRe.MatchString(zoneID) {
		return nil, errors.New("cloudflare: zone id must be 32 lowercase hex characters")
	}

	if token == "" {
		return nil, errors.New("cloudflare: empty API token")
	}

	return &Purger{zoneID: zoneID, token: token, BaseURL: APIURL}, nil
}

// URLs purges these URLs.
func (p *Purger) URLs(ctx context.Context, urls ...string) error {
	return p.batched(ctx, "files", urls)
}

// Tags purges every response whose Cache-Tag holds one of tags.
func (p *Purger) Tags(ctx context.Context, tags ...string) error { return p.batched(ctx, "tags", tags) }

// Prefixes purges every URL starting with one of prefixes (host and path,
// without the scheme, e.g. "example.com/blog/").
func (p *Purger) Prefixes(ctx context.Context, prefixes ...string) error {
	return p.batched(ctx, "prefixes", prefixes)
}

// Hosts purges everything cached for these hostnames.
func (p *Purger) Hosts(ctx context.Context, hosts ...string) error {
	return p.batched(ctx, "hosts", hosts)
}

// Everything purges the whole zone.
func (p *Purger) Everything(ctx context.Context) error {
	return p.purge(ctx, map[string]any{"purge_everything": true})
}

func (p *Purger) batched(ctx context.Context, field string, items []string) error {
	for i := 0; i < len(items); i += purgeBatch {
		if err := p.purge(ctx, map[string]any{field: items[i:min(i+purgeBatch, len(items))]}); err != nil {
			return err
		}
	}

	return nil
}

func (p *Purger) purge(ctx context.Context, body map[string]any) error {
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}

	url := strings.TrimSuffix(p.BaseURL, "/") + "/zones/" + p.zoneID + "/purge_cache"

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(b))
	if err != nil {
		return fmt.Errorf("cloudflare: purge: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+p.token)

	client := p.Client
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("cloudflare: purge: %w", err)
	}
	defer resp.Body.Close()

	var out struct {
		Success bool `json:"success"`
		Errors  []struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"errors"`
	}

	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&out); err != nil {
		return fmt.Errorf("cloudflare: purge: status %d: %w", resp.StatusCode, err)
	}

	if resp.StatusCode != http.StatusOK || !out.Success {
		msgs := make([]string, len(out.Errors))
		for i, e := range out.Errors {
			msgs[i] = fmt.Sprintf("%d %s", e.Code, e.Message)
		}

		return fmt.Errorf("cloudflare: purge: status %d: %s", resp.StatusCode, strings.Join(msgs, "; "))
	}

	return nil
}
