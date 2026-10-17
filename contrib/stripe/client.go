// Package stripe is a Stripe API client and webhook receiver built on the
// standard library (no stripe-go): customers, Checkout Sessions,
// subscriptions, Billing Portal sessions and PaymentIntents with the API
// version pinned, idempotency keys on every POST, retries on 429/5xx and
// typed errors; webhooks verify Stripe-Signature, process each event once
// and dispatch it to an events.Bus.
//
// See https://github.com/AlexGalhardo/vukem/blob/main/docs/subsystems.md
// for the client and https://github.com/AlexGalhardo/vukem/blob/main/docs/billing.md
// for the subscriptions that vukem install:billing builds on it.
package stripe

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// APIVersion is the Stripe API version every request pins.
const APIVersion = "2026-08-26.dahlia"

// Client calls the Stripe API. Use a restricted key (rk_) with only the
// permissions the app needs.
type Client struct {
	key     string
	baseURL string
	http    *http.Client
	retries int
	sleep   func(context.Context, time.Duration) error
}

// Option configures a Client.
type Option func(*Client)

// WithBaseURL points the client at another API host (tests, proxies).
func WithBaseURL(u string) Option { return func(c *Client) { c.baseURL = strings.TrimSuffix(u, "/") } }

// WithHTTPClient sets the HTTP client (default: 30s timeout).
func WithHTTPClient(h *http.Client) Option { return func(c *Client) { c.http = h } }

// WithRetries sets how many times a 429, 5xx or network failure is retried
// (default 2).
func WithRetries(n int) Option { return func(c *Client) { c.retries = n } }

// New returns a client for key (sk_ or rk_).
func New(key string, opts ...Option) (*Client, error) {
	if !strings.HasPrefix(key, "sk_") && !strings.HasPrefix(key, "rk_") {
		return nil, errors.New("stripe: key must start with sk_ or rk_")
	}

	c := &Client{
		key: key, baseURL: "https://api.stripe.com", retries: 2,
		http: &http.Client{Timeout: 30 * time.Second}, sleep: sleepCtx,
	}

	for _, o := range opts {
		o(c)
	}

	return c, nil
}

// String never reveals the key, so a client can be logged safely.
func (c *Client) String() string { return "stripe.Client{key: " + redact(c.key) + "}" }

// GoString implements fmt.GoStringer for %#v.
func (c *Client) GoString() string { return c.String() }

func redact(key string) string {
	if i := strings.LastIndexByte(key, '_'); i > 0 && len(key) > i+4 {
		return key[:i+1] + "…" + key[len(key)-4:]
	}

	return "…"
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// Params are request parameters; nested maps and slices are form-encoded
// the Stripe way (a[b][0]=c).
type Params map[string]any

// call sends a request and decodes the JSON response into out. POSTs carry
// an Idempotency-Key (generated unless key is set) so retries are safe.
func (c *Client) call(ctx context.Context, method, path string, p Params, idemKey string, out any) error {
	body := encodeParams(p)

	if method == http.MethodPost && idemKey == "" {
		idemKey = rand.Text()
	}

	var lastErr error

	for attempt := 0; ; attempt++ {
		retry, wait, err := c.do(ctx, method, path, body, idemKey, out)
		if err == nil {
			return nil
		}

		lastErr = err

		if !retry || attempt >= c.retries {
			return lastErr
		}

		if wait == 0 {
			wait = time.Duration(math.Pow(2, float64(attempt))) * 500 * time.Millisecond
		}

		if err := c.sleep(ctx, min(wait, 10*time.Second)); err != nil {
			return lastErr
		}
	}
}

func (c *Client) do(
	ctx context.Context,
	method, path, body, idemKey string,
	out any,
) (retry bool, wait time.Duration, err error) {
	target := c.baseURL + path

	var rd io.Reader = http.NoBody

	if method == http.MethodGet && body != "" {
		target += "?" + body
	} else if body != "" {
		rd = strings.NewReader(body)
	}

	req, err := http.NewRequestWithContext(ctx, method, target, rd)
	if err != nil {
		return false, 0, err
	}

	req.Header.Set("Authorization", "Bearer "+c.key)
	req.Header.Set("Stripe-Version", APIVersion)
	req.Header.Set("User-Agent", "vukem-stripe/1")

	if method != http.MethodGet {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}

	if idemKey != "" {
		req.Header.Set("Idempotency-Key", idemKey)
	}

	res, err := c.http.Do(req)
	if err != nil {
		return ctx.Err() == nil, 0, fmt.Errorf("stripe: %s %s: %w", method, path, err)
	}
	defer res.Body.Close()

	b, err := io.ReadAll(io.LimitReader(res.Body, 16<<20))
	if err != nil {
		return true, 0, fmt.Errorf("stripe: read response: %w", err)
	}

	if res.StatusCode >= 200 && res.StatusCode < 300 {
		if out == nil {
			return false, 0, nil
		}

		if err := json.Unmarshal(b, out); err != nil {
			return false, 0, fmt.Errorf("stripe: decode %s: %w", path, err)
		}

		return false, 0, nil
	}

	apiErr := parseError(res, b)

	switch res.Header.Get("Stripe-Should-Retry") {
	case "false":
		retry = false
	case "true":
		retry = true
	default:
		retry = res.StatusCode == http.StatusTooManyRequests || res.StatusCode >= 500 ||
			res.StatusCode == http.StatusConflict
	}

	if s, err := strconv.Atoi(res.Header.Get("Retry-After")); err == nil && s > 0 {
		wait = time.Duration(s) * time.Second
	}

	return retry, wait, apiErr
}

// Error is a Stripe API error.
type Error struct {
	Status      int    `json:"-"`
	Type        string `json:"type"`
	Code        string `json:"code"`
	DeclineCode string `json:"decline_code"`
	Message     string `json:"message"`
	Param       string `json:"param"`
	RequestID   string `json:"-"`
}

func (e *Error) Error() string {
	s := fmt.Sprintf("stripe: %d %s", e.Status, e.Type)
	if e.Code != "" {
		s += " (" + e.Code + ")"
	}

	if e.Message != "" {
		s += ": " + e.Message
	}

	return s
}

// Is matches errors of the same Type and Code (ErrCardDeclined, ...).
func (e *Error) Is(target error) bool {
	t, ok := target.(*Error)
	return ok && t.Type == e.Type && (t.Code == "" || t.Code == e.Code)
}

// Sentinel errors to test with errors.Is.
var (
	ErrCardDeclined = &Error{Type: "card_error", Code: "card_declined"}
	ErrInvalid      = &Error{Type: "invalid_request_error"}
	ErrRateLimited  = &Error{Type: "rate_limit_error"}
	ErrAuth         = &Error{Type: "authentication_error"}
)

func parseError(res *http.Response, b []byte) *Error {
	var env struct {
		Error *Error `json:"error"`
	}

	e := &Error{Type: "api_error", Message: http.StatusText(res.StatusCode)}
	if json.Unmarshal(b, &env) == nil && env.Error != nil {
		e = env.Error
	}

	e.Status = res.StatusCode
	e.RequestID = res.Header.Get("Request-Id")

	if res.StatusCode == http.StatusTooManyRequests && e.Type == "" {
		e.Type = "rate_limit_error"
	}

	return e
}

// encodeParams form-encodes p with Stripe's bracket syntax, keys sorted.
func encodeParams(p Params) string {
	v := url.Values{}
	flatten(v, "", map[string]any(p))

	return v.Encode()
}

func flatten(v url.Values, prefix string, x any) {
	key := func(k string) string {
		if prefix == "" {
			return k
		}

		return prefix + "[" + k + "]"
	}

	switch t := x.(type) {
	case nil:
	case Params:
		flatten(v, prefix, map[string]any(t))
	case map[string]any:
		for k, val := range t {
			flatten(v, key(k), val)
		}
	case map[string]string:
		for k, val := range t {
			v.Set(key(k), val)
		}
	case []string:
		for i, val := range t {
			v.Set(prefix+"["+strconv.Itoa(i)+"]", val)
		}
	case []any:
		for i, val := range t {
			flatten(v, prefix+"["+strconv.Itoa(i)+"]", val)
		}
	case []Params:
		for i, val := range t {
			flatten(v, prefix+"["+strconv.Itoa(i)+"]", val)
		}
	case bool:
		v.Set(prefix, strconv.FormatBool(t))
	case string:
		v.Set(prefix, t)
	case fmt.Stringer:
		v.Set(prefix, t.String())
	default:
		v.Set(prefix, fmt.Sprint(t))
	}
}

// List is a page of a list endpoint.
type List[T any] struct {
	Data    []T  `json:"data"`
	HasMore bool `json:"has_more"`
}
