package analytics

import (
	"bytes"
	"cmp"
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

var (
	umamiID     = regexp.MustCompile(`^[A-Za-z0-9-]{1,64}$`)
	umamiDomain = regexp.MustCompile(`^[A-Za-z0-9.-]{1,253}$`)
	umamiTag    = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,50}$`)
)

func (c Config) umamiScript() string { return cmp.Or(c.UmamiScriptURL, DefaultUmamiScriptURL) }

// umamiCloudGateway receives the events of Umami Cloud's tracker
// (https://docs.umami.is/docs/api/sending-stats).
const umamiCloudGateway = "https://gateway.umami.is"

// umamiEndpoint is where the tracker posts events: data-host-url, Umami
// Cloud's gateway, or a self-hosted script's directory, plus /api/send (as
// Umami's tracker computes it).
func (c Config) umamiEndpoint() string {
	base := strings.TrimSuffix(c.UmamiHostURL, "/")
	if base == "" {
		s := c.umamiScript()
		base = s[:max(strings.LastIndex(s, "/"), 0)]

		if origin(s) == origin(DefaultUmamiScriptURL) {
			base = umamiCloudGateway
		}
	}

	return base + "/api/send"
}

func (c Config) validateUmami() error {
	if c.UmamiWebsiteID == "" {
		if len(c.UmamiDomains) > 0 || c.UmamiHostURL != "" || c.UmamiTag != "" {
			return errors.New("analytics: UMAMI_DOMAINS, UMAMI_HOST_URL and UMAMI_TAG need UMAMI_WEBSITE_ID")
		}

		return nil
	}

	var errs []error

	if !umamiID.MatchString(c.UmamiWebsiteID) {
		errs = append(errs, errors.New("analytics: invalid UMAMI_WEBSITE_ID"))
	}

	if !checkURL(c.umamiScript()) {
		errs = append(errs, fmt.Errorf("analytics: UMAMI_SCRIPT_URL %q must be an https URL", c.UmamiScriptURL))
	}

	if c.UmamiHostURL != "" && !checkURL(c.UmamiHostURL) {
		errs = append(errs, fmt.Errorf("analytics: UMAMI_HOST_URL %q must be an https URL", c.UmamiHostURL))
	}

	for _, d := range c.UmamiDomains {
		if !umamiDomain.MatchString(d) {
			errs = append(errs, fmt.Errorf("analytics: invalid UMAMI_DOMAINS entry %q", d))
		}
	}

	if c.UmamiTag != "" && !umamiTag.MatchString(c.UmamiTag) {
		errs = append(errs, fmt.Errorf("analytics: invalid UMAMI_TAG %q", c.UmamiTag))
	}

	return errors.Join(errs...)
}

// umamiTag renders the tracker. It needs no inline code: Umami's
// auto-track follows history.pushState, so Inertia visits are page views.
func (c Config) umamiTag(nonce string) string {
	if c.UmamiWebsiteID == "" {
		return ""
	}

	var b strings.Builder

	b.WriteString(`<script defer src="` + esc(c.umamiScript()) + `" data-website-id="` + esc(c.UmamiWebsiteID) + `"`)

	if !c.UmamiIgnoreDoNotTrack {
		b.WriteString(` data-do-not-track="true"`)
	}

	if len(c.UmamiDomains) > 0 {
		b.WriteString(` data-domains="` + esc(strings.Join(c.UmamiDomains, ",")) + `"`)
	}

	if c.UmamiHostURL != "" {
		b.WriteString(` data-host-url="` + esc(c.UmamiHostURL) + `"`)
	}

	if c.UmamiTag != "" {
		b.WriteString(` data-tag="` + esc(c.UmamiTag) + `"`)
	}

	b.WriteString(nonceAttr(nonce) + `></script>`)

	return b.String()
}

func (c Config) umamiCSP() map[string][]string {
	if c.UmamiWebsiteID == "" {
		return nil
	}

	return map[string][]string{
		"script-src":  {origin(c.umamiScript())},
		"connect-src": {origin(c.umamiEndpoint())},
	}
}

// UmamiEvent is a custom event sent from the server with
// [Config.SendUmamiEvent].
type UmamiEvent struct {
	// Name is the event name (required, at most 50 characters).
	Name string
	// URL is the page path the event belongs to, e.g. "/checkout".
	URL string
	// Hostname is the site's host, e.g. "example.com".
	Hostname string
	// Title, Referrer and Language describe the page and visitor
	// (optional).
	Title, Referrer, Language string
	// Data holds the event properties (optional).
	Data map[string]any
}

// SendUmamiEvent posts e to Umami's /api/send, for events the browser
// never sees (a webhook, a job). It is a no-op when Umami is off. Umami
// drops requests whose User-Agent looks like a bot: pass the visitor's.
// A nil client uses one with a 10s timeout.
func (c Config) SendUmamiEvent(ctx context.Context, client *http.Client, userAgent string, e UmamiEvent) error {
	if c.UmamiWebsiteID == "" {
		return nil
	}

	if e.Name == "" || len(e.Name) > 50 {
		return errors.New("analytics: umami event name must have 1 to 50 characters")
	}

	payload := map[string]any{"website": c.UmamiWebsiteID, "name": e.Name}
	for k, v := range map[string]string{
		"url": e.URL, "hostname": e.Hostname, "title": e.Title, "referrer": e.Referrer, "language": e.Language,
	} {
		if v != "" {
			payload[k] = v
		}
	}

	if len(e.Data) > 0 {
		payload["data"] = e.Data
	}

	body, err := json.Marshal(map[string]any{"type": "event", "payload": payload})
	if err != nil {
		return fmt.Errorf("analytics: umami event: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.umamiEndpoint(), bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("analytics: umami event: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", userAgent)

	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("analytics: umami event: %w", err)
	}

	defer resp.Body.Close()

	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))

	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("analytics: umami event: status %d", resp.StatusCode)
	}

	return nil
}
