// Package indexnow tells search engines (Bing, Yandex, Seznam, Naver and
// the others behind api.indexnow.org) that pages were added, changed or
// removed, following https://www.indexnow.org/documentation.
//
// Set INDEXNOW_KEY and vukem serves the key file at /{key}.txt; then
//
//	n, err := indexnow.New(app.Config.IndexNow, app.Config.URL, nil)
//	err = n.Notify(ctx, "https://example.com/games/zelda")
//
// or dispatch [Job] on a queue so requests never wait on the search engine.
// A removed page is notified the same way: its URL now answers 404 or 410.
package indexnow

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
	"regexp"
	"strings"
	"time"

	"github.com/AlexGalhardo/vukem/config"
	"github.com/AlexGalhardo/vukem/httpclient"
	"github.com/AlexGalhardo/vukem/queue"
)

// DefaultEndpoint shares every notification with all IndexNow engines.
const DefaultEndpoint = "https://api.indexnow.org/indexnow"

// MaxURLs is the protocol's limit per request; Notify splits larger lists.
const MaxURLs = 10_000

// Config is loaded from the environment with config.Load (vukem.Config
// embeds it). An empty Key turns IndexNow off.
type Config struct {
	// Key is 8 to 128 characters of a-z, A-Z, 0-9 and "-". It is public
	// once served, but kept out of logs like any credential.
	Key config.Secret `env:"INDEXNOW_KEY"`
	// KeyLocation is the absolute URL of the key file, on the site's host.
	// Empty means {APP_URL}/{key}.txt. vukem serves the file at either.
	KeyLocation string `env:"INDEXNOW_KEY_LOCATION"`
	// Endpoint replaces DefaultEndpoint, e.g. https://www.bing.com/indexnow.
	Endpoint string `env:"INDEXNOW_ENDPOINT"`
}

var keyPattern = regexp.MustCompile(`^[A-Za-z0-9-]{8,128}$`)

// Sentinel errors matched (errors.Is) by [*Error] and Notify.
var (
	// ErrDisabled is returned by New when INDEXNOW_KEY is empty.
	ErrDisabled = errors.New("indexnow: INDEXNOW_KEY is not set")
	// ErrForeignURL is returned by Notify, before sending anything, for a
	// URL that is not an http(s) URL on the site's host.
	ErrForeignURL = errors.New("indexnow: URL is not on the site's host")
	// ErrBadRequest is a 400: invalid format.
	ErrBadRequest = errors.New("indexnow: bad request")
	// ErrForbidden is a 403: the key file is missing or does not hold the key.
	ErrForbidden = errors.New("indexnow: key not valid")
	// ErrUnprocessable is a 422: URLs off the host, or a malformed key.
	ErrUnprocessable = errors.New("indexnow: unprocessable entity")
	// ErrTooManyRequests is a 429: slow down.
	ErrTooManyRequests = errors.New("indexnow: too many requests")
)

// Error is a non-success answer from the endpoint.
type Error struct{ Status int }

func (e *Error) Error() string { return fmt.Sprintf("indexnow: status %d", e.Status) }

// Unwrap returns the sentinel for the status (nil for other codes).
func (e *Error) Unwrap() error {
	switch e.Status {
	case http.StatusBadRequest:
		return ErrBadRequest
	case http.StatusForbidden:
		return ErrForbidden
	case http.StatusUnprocessableEntity:
		return ErrUnprocessable
	case http.StatusTooManyRequests:
		return ErrTooManyRequests
	default:
		return nil
	}
}

// Enabled reports whether a key is set.
func (c Config) Enabled() bool { return !c.Key.IsZero() }

// Validate checks the key, and that KeyLocation is on siteURL's host and
// Endpoint is https. vukem.New calls it with APP_URL.
func (c Config) Validate(siteURL string) error {
	if !c.Enabled() {
		return nil
	}

	_, err := c.resolve(siteURL)

	return err
}

// KeyPath is the path the key file is served at.
func (c Config) KeyPath() string {
	if u, err := url.Parse(c.KeyLocation); err == nil && c.KeyLocation != "" {
		return u.EscapedPath()
	}

	return "/" + c.Key.Reveal() + ".txt"
}

// KeyFileHandler serves the key as UTF-8 text, as search engines check it.
func (c Config) KeyFileHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = io.WriteString(w, c.Key.Reveal())
	})
}

type site struct {
	scheme, host, keyLocation, endpoint string
}

func (c Config) resolve(siteURL string) (site, error) {
	var s site

	if !keyPattern.MatchString(c.Key.Reveal()) {
		return s, errors.New("indexnow: INDEXNOW_KEY must be 8 to 128 characters of a-z, A-Z, 0-9 and -")
	}

	u, err := url.Parse(siteURL)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return s, fmt.Errorf("indexnow: site URL %q must be an absolute http(s) URL", siteURL)
	}

	s.scheme, s.host = u.Scheme, strings.ToLower(u.Host)
	s.keyLocation = cmp.Or(c.KeyLocation, s.scheme+"://"+u.Host+"/"+c.Key.Reveal()+".txt")

	if !s.onHost(s.keyLocation) {
		return s, errors.New("indexnow: INDEXNOW_KEY_LOCATION must be an http(s) URL on the site's host")
	}

	s.endpoint = cmp.Or(c.Endpoint, DefaultEndpoint)

	if e, err := url.Parse(s.endpoint); err != nil || e.Scheme != "https" || e.Host == "" || e.User != nil {
		return s, fmt.Errorf("indexnow: INDEXNOW_ENDPOINT %q must be an https URL", c.Endpoint)
	}

	return s, nil
}

func (s site) onHost(raw string) bool {
	u, err := url.Parse(raw)

	return err == nil && (u.Scheme == "https" || u.Scheme == "http") && u.User == nil &&
		strings.EqualFold(u.Host, s.host)
}

// Client sends notifications for one site.
type Client struct {
	key  string
	site site
	http *http.Client
}

// New returns a client for siteURL (APP_URL), ErrDisabled without a key,
// or the Validate error. A nil hc uses httpclient.Safe with a 30s timeout.
func New(cfg Config, siteURL string, hc *http.Client) (*Client, error) {
	if !cfg.Enabled() {
		return nil, ErrDisabled
	}

	s, err := cfg.resolve(siteURL)
	if err != nil {
		return nil, err
	}

	if hc == nil {
		hc = httpclient.Safe(httpclient.Options{Timeout: 30 * time.Second})
	}

	return &Client{key: cfg.Key.Reveal(), site: s, http: hc}, nil
}

// Notify submits urls, de-duplicated, in requests of at most MaxURLs. Every
// URL must be on the site's host (ErrForeignURL, nothing sent otherwise).
// 200 and 202 are success; other answers are an [*Error].
func (c *Client) Notify(ctx context.Context, urls ...string) error {
	seen := make(map[string]bool, len(urls))
	list := make([]string, 0, len(urls))

	for _, u := range urls {
		if !c.site.onHost(u) {
			return fmt.Errorf("%w: %q", ErrForeignURL, u)
		}

		if !seen[u] {
			seen[u] = true
			list = append(list, u)
		}
	}

	for start := 0; start < len(list); start += MaxURLs {
		if err := c.send(ctx, list[start:min(start+MaxURLs, len(list))]); err != nil {
			return err
		}
	}

	return nil
}

func (c *Client) send(ctx context.Context, urls []string) error {
	body, err := json.Marshal(map[string]any{
		"host": c.site.host, "key": c.key, "keyLocation": c.site.keyLocation, "urlList": urls,
	})
	if err != nil {
		return fmt.Errorf("indexnow: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.site.endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("indexnow: %w", err)
	}

	req.Header.Set("Content-Type", "application/json; charset=utf-8")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("indexnow: %w", err)
	}

	defer resp.Body.Close()

	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
		return &Error{Status: resp.StatusCode}
	}

	return nil
}

// Job notifies URLs from a queue worker. Register [Client.Handler], then
// dispatch it from a create, update or delete:
//
//	app.Queue.Dispatch(ctx, indexnow.Job{URLs: []string{gameURL}})
type Job struct {
	URLs []string `json:"urls"`
}

// Kind implements queue.Job.
func (Job) Kind() string { return "vukem.indexnow" }

// Handler runs Job with up to 5 retries, backing off from one minute. 400,
// 403, 422 and foreign URLs are not retried (queue.ErrPermanent): a retry
// cannot fix them.
func (c *Client) Handler() queue.Registration {
	return queue.Handle(func(ctx context.Context, j Job) error {
		err := c.Notify(ctx, j.URLs...)
		if errors.Is(err, ErrForeignURL) || errors.Is(err, ErrBadRequest) || errors.Is(err, ErrForbidden) ||
			errors.Is(err, ErrUnprocessable) {
			return fmt.Errorf("%w: %w", err, queue.ErrPermanent)
		}

		return err
	}, queue.Retries(5), queue.Backoff(queue.Exponential(time.Minute)))
}
