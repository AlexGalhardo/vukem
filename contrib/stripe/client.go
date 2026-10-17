// Package stripe is a Stripe API client and webhook receiver built on the
// standard library (no stripe-go): customers, Checkout Sessions,
// subscriptions, Billing Portal sessions, PaymentIntents, refunds and Connect
// (Accounts v2 and v1 accounts, onboarding and login links, transfers and
// reversals, the Stripe-Account header) with the API version pinned, idempotency keys on
// every POST, retries on 429/5xx and typed errors; webhooks verify
// Stripe-Signature (platform and Connect secrets), process each event once
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
	// account is the connected account ForAccount acts as; asAccount tells
	// ForAccount("") (a caller bug, rejected) from the platform client.
	account   string
	asAccount bool
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

// ForAccount returns a copy of the client that sends every request as the
// connected account id (Stripe-Account: acct_...), for direct charges and
// reads on that account. An invalid id fails each call with
// ErrInvalidRequestOption before anything is sent.
func (c *Client) ForAccount(id string) *Client {
	cp := *c
	cp.account, cp.asAccount = id, true

	return &cp
}

// ErrInvalidRequestOption is returned, before any request is sent, for a
// Stripe-Account that is not an acct_ id, an idempotency key that is
// longer than 255 bytes or not printable ASCII, or a platform-only Connect
// method (accounts, account and login links, transfers) called through
// ForAccount or OnAccount.
var ErrInvalidRequestOption = errors.New("stripe: invalid request option")

// RequestOption tunes one request (Call and the Connect methods).
type RequestOption func(*requestOptions)

type requestOptions struct {
	idemKey      string
	account      string
	accountSet   bool
	platformOnly bool
}

// platformOnly marks endpoints the platform calls on its own account
// (creating accounts, links, transfers): sending them as a connected
// account would act on the wrong account, so an account scope is refused.
func platformOnly(o *requestOptions) { o.platformOnly = true }

// IdempotencyKey sets the Idempotency-Key of a POST, so retries of the same
// business operation (e.g. "order-42-transfer") never run twice, even
// across processes. Without it (or with "") every POST gets a random key
// that only protects the client's own retries. GETs never send one.
func IdempotencyKey(key string) RequestOption {
	return func(o *requestOptions) { o.idemKey = key }
}

// OnAccount sends the request as connected account id
// (Stripe-Account header); it overrides ForAccount.
func OnAccount(id string) RequestOption {
	return func(o *requestOptions) { o.account, o.accountSet = id, true }
}

func (o requestOptions) validate() error {
	if o.platformOnly && o.accountSet {
		return fmt.Errorf("%w: platform-only endpoint called with a connected account scope", ErrInvalidRequestOption)
	}

	if o.accountSet && !validAccountID(o.account) {
		return fmt.Errorf("%w: Stripe-Account must be an acct_ id", ErrInvalidRequestOption)
	}

	if len(o.idemKey) > 255 {
		return fmt.Errorf("%w: idempotency key longer than 255 bytes", ErrInvalidRequestOption)
	}

	for i := range len(o.idemKey) {
		if b := o.idemKey[i]; b < 0x21 || b > 0x7e {
			return fmt.Errorf("%w: idempotency key must be printable ASCII", ErrInvalidRequestOption)
		}
	}

	return nil
}

func validAccountID(id string) bool {
	rest, ok := strings.CutPrefix(id, "acct_")
	if !ok || rest == "" || len(rest) > 255 {
		return false
	}

	for _, r := range rest {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') {
			return false
		}
	}

	return true
}

// Call sends any request (Connect endpoints, refunds, ...) and decodes the
// JSON response into out (a struct, *json.RawMessage or nil to discard).
// POSTs (and API v2 DELETEs) carry an Idempotency-Key: IdempotencyKey's, or
// a generated one. A path under /v2/ is API v2: writes send p as JSON
// (a nil value is JSON null, which v2 uses to clear a metadata key).
func (c *Client) Call(ctx context.Context, method, path string, p Params, out any, opts ...RequestOption) error {
	if out == nil {
		out = &json.RawMessage{}
	}

	return c.call(ctx, method, path, p, out, opts...)
}

// Params are request parameters; nested maps and slices are form-encoded
// the Stripe way (a[b][0]=c).
type Params map[string]any

// call sends a request and decodes the JSON response into out. POSTs carry
// an Idempotency-Key (generated unless one is set) so retries are safe.
func (c *Client) call(ctx context.Context, method, path string, p Params, out any, opts ...RequestOption) error {
	o := requestOptions{account: c.account, accountSet: c.asAccount}
	for _, opt := range opts {
		opt(&o)
	}

	if err := o.validate(); err != nil {
		return err
	}

	v2 := strings.HasPrefix(path, "/v2/")

	body, contentType, err := encodeBody(method, v2, p)
	if err != nil {
		return fmt.Errorf("stripe: encode %s: %w", path, err)
	}

	idemKey := ""
	// API v2 replays DELETEs idempotently too (docs.stripe.com/api-v2-overview#idempotency).
	if method == http.MethodPost || (v2 && method == http.MethodDelete) {
		idemKey = o.idemKey
		if idemKey == "" {
			idemKey = rand.Text()
		}
	}

	var lastErr error

	for attempt := 0; ; attempt++ {
		retry, wait, err := c.do(ctx, method, path, body, contentType, idemKey, o.account, out)
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
	method, path, body, contentType, idemKey, account string,
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

	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}

	if idemKey != "" {
		req.Header.Set("Idempotency-Key", idemKey)
	}

	if account != "" {
		req.Header.Set("Stripe-Account", account)
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

// encodeBody encodes p for the request: a query string on GET (v2 reads
// arrays as include[0]=a, the same bracket syntax as v1), JSON for API v2
// writes (v2 rejects form bodies) and a form for v1 writes. It returns the
// Content-Type ("" on GET).
func encodeBody(method string, v2 bool, p Params) (body, contentType string, err error) {
	switch {
	case method == http.MethodGet:
		return encodeParams(p), "", nil
	case v2:
		if p == nil {
			p = Params{}
		}

		b, err := json.Marshal(p)

		return string(b), "application/json", err
	default:
		return encodeParams(p), "application/x-www-form-urlencoded", nil
	}
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
