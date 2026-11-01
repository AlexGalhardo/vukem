// Package bingwmt is a minimal client for the Bing Webmaster Tools API
// (https://learn.microsoft.com/en-us/bingwebmaster/getting-started): search
// traffic, backlinks, crawl and index state, and URL submission, over its
// JSON endpoint.
//
//	cfg, err := config.Load[bingwmt.Config](src) // BING_WEBMASTER_API_KEY
//	c, err := bingwmt.New(cfg.APIKey, bingwmt.Options{})
//	stats, err := c.QueryStats(ctx, "https://example.com/")
//
// The API key travels in the query string, as the API requires; it never
// appears in an error or log line produced by this package.
package bingwmt

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/AlexGalhardo/vukem/config"
	"github.com/AlexGalhardo/vukem/httpclient"
)

// DefaultEndpoint is the API's JSON endpoint; method names are appended.
const DefaultEndpoint = "https://ssl.bing.com/webmaster/api.svc/json/"

// Config loads the API key from the environment with config.Load.
type Config struct {
	// APIKey is generated in Bing Webmaster Tools, Settings > API access.
	APIKey config.Secret `env:"BING_WEBMASTER_API_KEY"`
}

// ErrNoAPIKey is returned by New when the key is empty: the integration is
// off.
var ErrNoAPIKey = errors.New("bingwmt: BING_WEBMASTER_API_KEY is not set")

// ErrRateLimited matches (errors.Is) an [*Error] for a 429 response.
var ErrRateLimited = errors.New("bingwmt: rate limited")

// Options tune the client.
type Options struct {
	// Endpoint replaces DefaultEndpoint (tests, proxies).
	Endpoint string
	// HTTPClient replaces the default: httpclient.Safe with a 30s timeout,
	// at most 4 requests in flight and 5 per second, and retries of GETs
	// on 429 and 5xx honouring Retry-After.
	HTTPClient *http.Client
}

// Client calls the API. It is safe for concurrent use.
type Client struct {
	key      config.Secret
	endpoint string
	http     *http.Client
}

// New returns a client, or ErrNoAPIKey when key is empty.
func New(key config.Secret, o Options) (*Client, error) {
	if key.IsZero() {
		return nil, ErrNoAPIKey
	}

	hc := o.HTTPClient
	if hc == nil {
		throttle := httpclient.Throttle(httpclient.Limit{PerSecond: 5, Burst: 5, InFlight: 4})
		retry := httpclient.Retry(httpclient.RetryPolicy{})
		hc = httpclient.Safe(httpclient.Options{
			Timeout:       30 * time.Second,
			WrapTransport: func(rt http.RoundTripper) http.RoundTripper { return retry(throttle(rt)) },
		})
	}

	ep := cmp.Or(o.Endpoint, DefaultEndpoint)
	if !strings.HasSuffix(ep, "/") {
		ep += "/"
	}

	return &Client{key: key, endpoint: ep, http: hc}, nil
}

// Error is a non-2xx answer. Code and Message come from the API's
// {"ErrorCode","Message"} body when present.
type Error struct {
	Method  string
	Status  int
	Code    int
	Message string
}

func (e *Error) Error() string {
	return fmt.Sprintf("bingwmt: %s: status %d: %s (code %d)", e.Method, e.Status, e.Message, e.Code)
}

// Is matches ErrRateLimited for a 429.
func (e *Error) Is(target error) bool {
	return target == ErrRateLimited && e.Status == http.StatusTooManyRequests
}

// QueryStats is a day of search traffic for a query (QueryStats) or a page
// (PageStats, where Query holds the page URL).
type QueryStats struct {
	Query                 string
	Date                  Date
	Clicks                int64
	Impressions           int64
	AvgClickPosition      int
	AvgImpressionPosition int
}

// LinkCounts is a page of pages with inbound links.
type LinkCounts struct {
	Links []struct {
		URL   string `json:"Url"`
		Count int64
	}
	TotalPages int
}

// LinkDetails is a page of the links pointing at one URL.
type LinkDetails struct {
	Details []struct {
		URL        string `json:"Url"`
		AnchorText string
	}
	TotalPages int
}

// CrawlStats is a day of crawling.
type CrawlStats struct {
	Date               Date
	CrawledPages       int64
	CrawlErrors        int64
	InIndex            int64
	InLinks            int64
	Code2xx            int64
	Code301            int64
	Code302            int64
	Code4xx            int64
	Code5xx            int64
	AllOtherCodes      int64
	BlockedByRobotsTxt int64
	ConnectionTimeout  int64
	ContainsMalware    int64
	DNSFailures        int64 `json:"DnsFailures"`
}

// URLInfo is the index state of one URL.
type URLInfo struct {
	URL                string `json:"Url"`
	HTTPStatus         int    `json:"HttpStatus"`
	DocumentSize       int64
	AnchorCount        int64
	TotalChildURLCount int64 `json:"TotalChildUrlCount"`
	IsPage             bool
	DiscoveryDate      Date
	LastCrawledDate    Date
}

// CrawlIssue is a URL Bing could not crawl cleanly; Issues is the API's
// bit set of issue types.
type CrawlIssue struct {
	URL      string `json:"Url"`
	HTTPCode int    `json:"HttpCode"`
	Issues   int
	InLinks  int64
}

// SubmissionQuota is how many URLs SubmitURL and SubmitURLBatch may still
// send for a site.
type SubmissionQuota struct {
	DailyQuota   int
	MonthlyQuota int
}

// QueryStats returns the top search queries of siteURL.
func (c *Client) QueryStats(ctx context.Context, siteURL string) ([]QueryStats, error) {
	return get[[]QueryStats](ctx, c, "GetQueryStats", url.Values{"siteUrl": {siteURL}})
}

// PageStats returns the top pages of siteURL in search.
func (c *Client) PageStats(ctx context.Context, siteURL string) ([]QueryStats, error) {
	return get[[]QueryStats](ctx, c, "GetPageStats", url.Values{"siteUrl": {siteURL}})
}

// LinkCounts returns page (from 0) of the site's pages with inbound links.
func (c *Client) LinkCounts(ctx context.Context, siteURL string, page int) (LinkCounts, error) {
	return get[LinkCounts](ctx, c, "GetLinkCounts", url.Values{"siteUrl": {siteURL}, "page": {strconv.Itoa(page)}})
}

// URLLinks returns page (from 0) of the inbound links to link.
func (c *Client) URLLinks(ctx context.Context, siteURL, link string, page int) (LinkDetails, error) {
	return get[LinkDetails](ctx, c, "GetUrlLinks",
		url.Values{"siteUrl": {siteURL}, "link": {link}, "page": {strconv.Itoa(page)}})
}

// CrawlStats returns the site's daily crawl statistics.
func (c *Client) CrawlStats(ctx context.Context, siteURL string) ([]CrawlStats, error) {
	return get[[]CrawlStats](ctx, c, "GetCrawlStats", url.Values{"siteUrl": {siteURL}})
}

// URLInfo returns the index state of pageURL.
func (c *Client) URLInfo(ctx context.Context, siteURL, pageURL string) (URLInfo, error) {
	return get[URLInfo](ctx, c, "GetUrlInfo", url.Values{"siteUrl": {siteURL}, "url": {pageURL}})
}

// CrawlIssues returns the URLs with crawl issues.
func (c *Client) CrawlIssues(ctx context.Context, siteURL string) ([]CrawlIssue, error) {
	return get[[]CrawlIssue](ctx, c, "GetCrawlIssues", url.Values{"siteUrl": {siteURL}})
}

// SubmissionQuota returns the remaining URL submission quota.
func (c *Client) SubmissionQuota(ctx context.Context, siteURL string) (SubmissionQuota, error) {
	return get[SubmissionQuota](ctx, c, "GetUrlSubmissionQuota", url.Values{"siteUrl": {siteURL}})
}

// SubmitURL asks Bing to crawl pageURL. It counts against SubmissionQuota
// and is never retried (the POST is not idempotent for the quota).
func (c *Client) SubmitURL(ctx context.Context, siteURL, pageURL string) error {
	return c.post(ctx, "SubmitUrl", map[string]any{"siteUrl": siteURL, "url": pageURL})
}

// SubmitURLBatch asks Bing to crawl pageURLs, like SubmitURL.
func (c *Client) SubmitURLBatch(ctx context.Context, siteURL string, pageURLs []string) error {
	return c.post(ctx, "SubmitUrlBatch", map[string]any{"siteUrl": siteURL, "urlList": pageURLs})
}

func get[T any](ctx context.Context, c *Client, method string, q url.Values) (T, error) {
	var out struct{ D T }

	err := c.do(ctx, http.MethodGet, method, q, nil, &out)

	return out.D, err
}

func (c *Client) post(ctx context.Context, method string, body any) error {
	b, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("bingwmt: %s: %w", method, err)
	}

	return c.do(ctx, http.MethodPost, method, nil, b, nil)
}

func (c *Client) do(ctx context.Context, verb, method string, q url.Values, body []byte, out any) error {
	if q == nil {
		q = url.Values{}
	}

	q.Set("apikey", c.key.Reveal())

	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}

	req, err := http.NewRequestWithContext(ctx, verb, c.endpoint+method+"?"+q.Encode(), rd)
	if err != nil {
		return fmt.Errorf("bingwmt: %s: invalid request", method)
	}

	req.Header.Set("Accept", "application/json")

	if body != nil {
		req.Header.Set("Content-Type", "application/json; charset=utf-8")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("bingwmt: %s: %w", method, redact(err))
	}

	defer resp.Body.Close()

	data, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	if err != nil {
		return fmt.Errorf("bingwmt: %s: read: %w", method, redact(err))
	}

	if resp.StatusCode/100 != 2 {
		e := &Error{Method: method, Status: resp.StatusCode, Message: http.StatusText(resp.StatusCode)}

		var apiErr struct {
			ErrorCode int    `json:"ErrorCode"`
			Message   string `json:"Message"`
		}
		if json.Unmarshal(data, &apiErr) == nil && apiErr.Message != "" {
			e.Code, e.Message = apiErr.ErrorCode, apiErr.Message
		}

		e.Message = strings.ReplaceAll(e.Message, c.key.Reveal(), "[redacted]")

		return e
	}

	if out == nil {
		return nil
	}

	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("bingwmt: %s: decode: %w", method, err)
	}

	return nil
}

// redact drops the request URL, which carries the API key, from transport
// errors.
func redact(err error) error {
	if ue, ok := errors.AsType[*url.Error](err); ok {
		return ue.Err
	}

	return err
}

// Date is a WCF JSON date ("/Date(1700000000000-0800)/").
type Date struct{ time.Time }

// UnmarshalJSON parses the WCF form; null and "" leave the zero time.
func (d *Date) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return fmt.Errorf("bingwmt: invalid date: %w", err)
	}

	if s == "" {
		return nil
	}

	inner, ok := strings.CutPrefix(s, "/Date(")
	if inner, ok2 := strings.CutSuffix(inner, ")/"); ok && ok2 {
		// The offset after the milliseconds is informational: the
		// milliseconds are already UTC.
		end := len(inner)
		if i := strings.IndexAny(inner[1:], "+-"); i >= 0 {
			end = i + 1
		}

		ms, err := strconv.ParseInt(inner[:end], 10, 64)
		if err != nil {
			return fmt.Errorf("bingwmt: invalid date %q", s)
		}

		d.Time = time.UnixMilli(ms).UTC()

		return nil
	}

	return fmt.Errorf("bingwmt: invalid date %q", s)
}
