package vukemtest

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/AlexGalhardo/vukem/auth"
)

// Client sends in-process requests to the app and keeps cookies between
// them, like a browser.
type Client struct {
	app     *App
	t       testing.TB
	headers http.Header
	cookies map[string]*http.Cookie
}

// Client returns a new client with an empty cookie jar.
func (a *App) Client(tb testing.TB) *Client {
	tb.Helper()

	return &Client{app: a, t: tb, headers: http.Header{}, cookies: map[string]*http.Cookie{}}
}

// WithHeader sets a header on every following request.
func (c *Client) WithHeader(key, value string) *Client { c.headers.Set(key, value); return c }

// WithToken sends "Authorization: Bearer token".
func (c *Client) WithToken(token string) *Client {
	return c.WithHeader("Authorization", "Bearer "+token)
}

// WithCookie adds a cookie to the jar.
func (c *Client) WithCookie(name, value string) *Client {
	// G124: a request-side cookie; Secure/HttpOnly/SameSite only apply to Set-Cookie.
	c.cookies[name] = &http.Cookie{Name: name, Value: value} //nolint:gosec // see above

	return c
}

// ActingAs logs the client in as user on the session guard named guard
// ("web"), without going through the login form: the session holds what
// auth.SessionGuard.Login stores (auth.SessionValues), the user's session
// version included. Values already in the client's session are kept.
func (c *Client) ActingAs(guard string, user interface{ AuthID() string }) *Client {
	c.t.Helper()

	return c.WithSession(auth.SessionValues(guard, user))
}

// WithSession adds values to the client's session (a login stamp kept by
// the app, a feature flag...), keeping what it already holds. It starts a
// new session, as a login would.
func (c *Client) WithSession(values map[string]any) *Client {
	c.t.Helper()

	m, err := c.app.Sessions()
	if err != nil {
		c.t.Fatalf("vukemtest: %v", err)
	}

	data := m.Peek(c.t.Context(), slices.Collect(maps.Values(c.cookies)))
	maps.Copy(data, values)

	for _, ck := range m.Cookies(c.t.Context(), data) {
		c.cookies[ck.Name] = ck
	}

	return c
}

// CookieValue returns the value of a cookie in the jar ("" when absent).
func (c *Client) CookieValue(name string) string {
	if ck, ok := c.cookies[name]; ok {
		return ck.Value
	}

	return ""
}

// HTMX marks following requests as htmx partial requests.
func (c *Client) HTMX() *Client {
	return c.WithHeader("HX-Request", "true").WithHeader("HX-Request-Type", "partial")
}

// AcceptJSON asks for JSON responses.
func (c *Client) AcceptJSON() *Client { return c.WithHeader("Accept", "application/json") }

// Get sends GET.
func (c *Client) Get(target string) *Response { return c.Do(http.MethodGet, target, nil, "") }

// GetJSON sends one GET accepting JSON; later requests keep the client's
// own Accept header (use AcceptJSON to change it for good).
func (c *Client) GetJSON(target string) *Response {
	prev, had := c.headers["Accept"]
	c.headers.Set("Accept", "application/json")

	defer func() {
		if had {
			c.headers["Accept"] = prev
		} else {
			c.headers.Del("Accept")
		}
	}()

	return c.Get(target)
}

// Delete sends DELETE.
func (c *Client) Delete(target string) *Response { return c.Do(http.MethodDelete, target, nil, "") }

// PostForm sends an urlencoded form.
func (c *Client) PostForm(target string, form url.Values) *Response {
	return c.Do(http.MethodPost, target, strings.NewReader(form.Encode()), "application/x-www-form-urlencoded")
}

// PostJSON sends v as JSON.
func (c *Client) PostJSON(target string, v any) *Response {
	return c.sendJSON(http.MethodPost, target, v)
}

// PutJSON sends v as JSON with PUT.
func (c *Client) PutJSON(target string, v any) *Response {
	return c.sendJSON(http.MethodPut, target, v)
}

// PatchJSON sends v as JSON with PATCH.
func (c *Client) PatchJSON(target string, v any) *Response {
	return c.sendJSON(http.MethodPatch, target, v)
}

func (c *Client) sendJSON(method, target string, v any) *Response {
	c.t.Helper()

	b, err := json.Marshal(v)
	if err != nil {
		c.t.Fatalf("vukemtest: encode body: %v", err)
	}

	if c.headers.Get("Accept") == "" {
		c.headers.Set("Accept", "application/json")
	}

	return c.Do(method, target, bytes.NewReader(b), "application/json")
}

// Do sends a request with an optional body and content type.
func (c *Client) Do(method, target string, body io.Reader, contentType string) *Response {
	c.t.Helper()

	if body == nil {
		body = http.NoBody
	}

	r := httptest.NewRequestWithContext(c.t.Context(), method, target, body)
	maps.Copy(r.Header, c.headers.Clone())

	if contentType != "" {
		r.Header.Set("Content-Type", contentType)
	}

	for _, ck := range c.cookies {
		r.AddCookie(ck)
	}

	rec := httptest.NewRecorder()
	c.app.handler.ServeHTTP(rec, r)

	for _, ck := range rec.Result().Cookies() {
		if ck.MaxAge < 0 {
			delete(c.cookies, ck.Name)
		} else {
			c.cookies[ck.Name] = ck
		}
	}

	return &Response{t: c.t, client: c, req: r, rec: rec}
}

// Response is a recorded response with chainable assertions.
type Response struct {
	t      testing.TB
	client *Client
	req    *http.Request
	rec    *httptest.ResponseRecorder
	json   any
}

// Recorder exposes the raw recorder.
func (r *Response) Recorder() *httptest.ResponseRecorder { return r.rec }

// Status returns the status code.
func (r *Response) Status() int { return r.rec.Code }

// Body returns the body.
func (r *Response) Body() string { return r.rec.Body.String() }

// Header returns a response header.
func (r *Response) Header(key string) string { return r.rec.Header().Get(key) }

func (r *Response) fail(format string, args ...any) {
	r.t.Helper()

	body := r.rec.Body.String()
	if len(body) > 2000 {
		body = body[:2000] + "…"
	}

	r.t.Fatalf("%s\n\n%s %s → %d\n%s\n%s", fmt.Sprintf(format, args...), r.req.Method, r.req.URL, r.rec.Code,
		headerDump(r.rec.Header()), body)
}

func headerDump(h http.Header) string {
	var b strings.Builder

	for _, k := range sortedHeaderKeys(h) {
		fmt.Fprintf(&b, "%s: %s\n", k, strings.Join(h[k], ", "))
	}

	return b.String()
}

func sortedHeaderKeys(h http.Header) []string {
	keys := make([]string, 0, len(h))
	for k := range h {
		keys = append(keys, k)
	}

	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j] < keys[j-1]; j-- {
			keys[j], keys[j-1] = keys[j-1], keys[j]
		}
	}

	return keys
}

// AssertStatus fails unless the status is code.
func (r *Response) AssertStatus(code int) *Response {
	r.t.Helper()

	if r.rec.Code != code {
		r.fail("status = %d, want %d", r.rec.Code, code)
	}

	return r
}

// AssertOK asserts 200.
func (r *Response) AssertOK() *Response { r.t.Helper(); return r.AssertStatus(http.StatusOK) }

// AssertCreated asserts 201.
func (r *Response) AssertCreated() *Response { r.t.Helper(); return r.AssertStatus(http.StatusCreated) }

// AssertNoContent asserts 204.
func (r *Response) AssertNoContent() *Response {
	r.t.Helper()
	return r.AssertStatus(http.StatusNoContent)
}

// AssertNotFound asserts 404.
func (r *Response) AssertNotFound() *Response {
	r.t.Helper()
	return r.AssertStatus(http.StatusNotFound)
}

// AssertForbidden asserts 403.
func (r *Response) AssertForbidden() *Response {
	r.t.Helper()
	return r.AssertStatus(http.StatusForbidden)
}

// AssertUnauthorized asserts 401.
func (r *Response) AssertUnauthorized() *Response {
	r.t.Helper()
	return r.AssertStatus(http.StatusUnauthorized)
}

// AssertUnprocessable asserts 422.
func (r *Response) AssertUnprocessable() *Response {
	r.t.Helper()
	return r.AssertStatus(http.StatusUnprocessableEntity)
}

// AssertRedirect asserts a 3xx (or htmx HX-Location) to location.
func (r *Response) AssertRedirect(location string) *Response {
	r.t.Helper()

	got := r.rec.Header().Get("Location")
	if hx := r.rec.Header().Get("HX-Location"); hx != "" {
		got = hx
	} else if r.rec.Code < 300 || r.rec.Code > 399 {
		r.fail("status = %d, want a redirect", r.rec.Code)
	}

	if got != location {
		r.fail("redirect to %q, want %q", got, location)
	}

	return r
}

// AssertRedirectRoute asserts a redirect to a named route.
func (r *Response) AssertRedirectRoute(name string, params ...any) *Response {
	r.t.Helper()

	u, err := r.client.app.Router.URL(name, params...)
	if err != nil {
		r.t.Fatalf("vukemtest: %v", err)
	}

	return r.AssertRedirect(u)
}

// AssertSee asserts the body contains text.
func (r *Response) AssertSee(text string) *Response {
	r.t.Helper()

	if !strings.Contains(r.rec.Body.String(), text) {
		r.fail("body does not contain %q", text)
	}

	return r
}

// AssertDontSee asserts the body does not contain text.
func (r *Response) AssertDontSee(text string) *Response {
	r.t.Helper()

	if strings.Contains(r.rec.Body.String(), text) {
		r.fail("body contains %q", text)
	}

	return r
}

// AssertHeader asserts a response header value.
func (r *Response) AssertHeader(key, value string) *Response {
	r.t.Helper()

	if got := r.rec.Header().Get(key); got != value {
		r.fail("header %s = %q, want %q", key, got, value)
	}

	return r
}

// AssertHeaderContains checks that header key contains substr.
func (r *Response) AssertHeaderContains(key, substr string) *Response {
	r.t.Helper()

	if got := r.rec.Header().Get(key); !strings.Contains(got, substr) {
		r.fail("header %s = %q, want it to contain %q", key, got, substr)
	}

	return r
}

// AssertHeaderMissing checks that the response has no header key (for
// example no Set-Cookie on a health probe, no Server banner).
func (r *Response) AssertHeaderMissing(key string) *Response {
	r.t.Helper()

	if v, ok := r.rec.Header()[http.CanonicalHeaderKey(key)]; ok {
		r.fail("header %s present (%q), want it missing", key, v)
	}

	return r
}

func (r *Response) decoded() any {
	r.t.Helper()

	if r.json == nil {
		dec := json.NewDecoder(bytes.NewReader(r.rec.Body.Bytes()))
		dec.UseNumber()

		if err := dec.Decode(&r.json); err != nil {
			r.fail("body is not JSON: %v", err)
		}
	}

	return r.json
}

// JSONPath returns the value at a dotted path ("data.0.title").
func (r *Response) JSONPath(path string) (any, bool) {
	r.t.Helper()

	cur := r.decoded()

	if path == "" {
		return cur, true
	}

	for part := range strings.SplitSeq(path, ".") {
		switch v := cur.(type) {
		case map[string]any:
			next, ok := v[part]
			if !ok {
				return nil, false
			}

			cur = next
		case []any:
			i, err := strconv.Atoi(part)
			if err != nil || i < 0 || i >= len(v) {
				return nil, false
			}

			cur = v[i]
		default:
			return nil, false
		}
	}

	return cur, true
}

// AssertMatchesOpenAPI checks the response against the app's OpenAPI
// document (App.OpenAPI): the operation for the request, the schema of the
// response status (or the default problem response).
func (r *Response) AssertMatchesOpenAPI() *Response {
	r.t.Helper()

	doc, err := r.client.app.OpenAPI()
	if err != nil {
		r.fail("OpenAPI document: %v", err)
	}

	if err := doc.ValidateResponse(r.req.Method, r.req.URL.Path, r.rec.Code, r.rec.Body.Bytes()); err != nil {
		r.fail("response does not match the OpenAPI document: %v", err)
	}

	return r
}

// AssertJSONPath asserts the value at path equals want (compared by its
// JSON encoding, so 3 matches json.Number("3")).
func (r *Response) AssertJSONPath(path string, want any) *Response {
	r.t.Helper()

	got, ok := r.JSONPath(path)
	if !ok {
		r.fail("JSON path %q not found", path)
	}

	if !sameJSON(got, want) {
		r.fail("JSON path %q = %v, want %v", path, got, want)
	}

	return r
}

// AssertJSONCount asserts the array (or object) at path has n entries.
func (r *Response) AssertJSONCount(path string, n int) *Response {
	r.t.Helper()

	got, ok := r.JSONPath(path)
	if !ok {
		r.fail("JSON path %q not found", path)
	}

	size := -1

	switch v := got.(type) {
	case []any:
		size = len(v)
	case map[string]any:
		size = len(v)
	}

	if size != n {
		r.fail("JSON path %q has %d entries, want %d", path, size, n)
	}

	return r
}

// AssertJSON asserts the whole body equals want (by JSON encoding).
func (r *Response) AssertJSON(want any) *Response {
	r.t.Helper()

	if !sameJSON(r.decoded(), want) {
		r.fail("JSON body differs from %v", want)
	}

	return r
}

func sameJSON(a, b any) bool {
	x, err1 := canonical(a)
	y, err2 := canonical(b)

	return err1 == nil && err2 == nil && x == y
}

func canonical(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}

	dec := json.NewDecoder(bytes.NewReader(b))

	var out any
	if err := dec.Decode(&out); err != nil {
		return "", err
	}

	c, err := json.Marshal(out)

	return string(c), err
}

// AssertProblem asserts an RFC 9457 problem+json response with status.
func (r *Response) AssertProblem(status int) *Response {
	r.t.Helper()
	r.AssertStatus(status)

	if ct := r.rec.Header().Get("Content-Type"); ct != "application/problem+json" {
		r.fail("Content-Type = %q, want application/problem+json", ct)
	}

	return r.AssertJSONPath("status", status)
}

// AssertJSONValidationErrors asserts a 422 problem with errors for fields.
func (r *Response) AssertJSONValidationErrors(fields ...string) *Response {
	r.t.Helper()
	r.AssertProblem(http.StatusUnprocessableEntity)

	errs, _ := r.JSONPath("errors")
	m, _ := errs.(map[string]any)

	for _, f := range fields {
		if _, ok := m[f]; !ok {
			r.fail("no validation error for %q (got %v)", f, m)
		}
	}

	return r
}

// AssertHXTrigger asserts the response triggers a client event.
func (r *Response) AssertHXTrigger(event string) *Response {
	r.t.Helper()

	var triggers map[string]any

	h := r.rec.Header().Get("HX-Trigger")
	if err := json.Unmarshal([]byte(h), &triggers); err != nil {
		if h != event {
			r.fail("HX-Trigger %q does not include %q", h, event)
		}

		return r
	}

	if _, ok := triggers[event]; !ok {
		r.fail("HX-Trigger %q does not include %q", h, event)
	}

	return r
}

// AssertFragment asserts an HTML fragment (no <html> document).
func (r *Response) AssertFragment() *Response {
	r.t.Helper()

	if strings.Contains(strings.ToLower(r.rec.Body.String()), "<html") {
		r.fail("got a full page, want a fragment")
	}

	return r
}

// Session returns the session data the next request would see.
func (r *Response) Session() map[string]any {
	r.t.Helper()

	m, err := r.client.app.Sessions()
	if err != nil {
		r.t.Fatalf("vukemtest: %v", err)
	}

	cookies := make([]*http.Cookie, 0, len(r.client.cookies))
	for _, c := range r.client.cookies {
		cookies = append(cookies, c)
	}

	return m.Peek(r.t.Context(), cookies)
}

// AssertSessionHas asserts a session (or flash) key is set.
func (r *Response) AssertSessionHas(key string) *Response {
	r.t.Helper()

	if r.Session()[key] == nil {
		r.fail("session has no %q", key)
	}

	return r
}

// AssertSessionMissing asserts a session key is absent.
func (r *Response) AssertSessionMissing(key string) *Response {
	r.t.Helper()

	if r.Session()[key] != nil {
		r.fail("session has %q", key)
	}

	return r
}

// AssertSessionHasErrors asserts validation errors were flashed for fields.
func (r *Response) AssertSessionHasErrors(fields ...string) *Response {
	r.t.Helper()

	errs, _ := r.Session()["_errors"].(map[string]any)

	for _, f := range fields {
		if _, ok := errs[f]; !ok {
			r.fail("no flashed validation error for %q (got %v)", f, errs)
		}
	}

	return r
}

// inertiaFlashKey is the session key under which inertia.Flash keeps data
// until a page renders it.
const inertiaFlashKey = "_inertia_flash"

// AssertFlash asserts that key was flashed with a value equal to want
// (compared by JSON encoding). It finds web flash data (c.Flash) and
// inertia.Flash data, either still in the session after a redirect or
// rendered in the "flash" object of an Inertia page. The key must be
// present: want nil matches a flashed nil, not a missing value.
// InertiaPage.FlashEquals reads only the page's flash object.
func (r *Response) AssertFlash(key string, want any) *Response {
	r.t.Helper()

	got, ok := r.flash(key)
	if !ok {
		r.fail("no flash %q", key)
	}

	if !sameJSON(got, want) {
		r.fail("flash %q = %s, want %s", key, show(got), show(want))
	}

	return r
}

// AssertNoFlash asserts that key is not flashed, neither in the session
// nor in an Inertia page's flash object.
func (r *Response) AssertNoFlash(key string) *Response {
	r.t.Helper()

	if got, ok := r.flash(key); ok {
		r.fail("flash %q present (%s), want none", key, show(got))
	}

	return r
}

func (r *Response) flash(key string) (any, bool) {
	r.t.Helper()

	if page, err := r.inertiaPage(); err == nil {
		if f, ok := page["flash"].(map[string]any); ok {
			if v, ok := f[key]; ok {
				return v, true
			}
		}
	}

	s := r.Session()

	if f, ok := s[inertiaFlashKey].(map[string]any); ok {
		if v, ok := f[key]; ok {
			return v, true
		}
	}

	// A plain session value is not a flash: the key must be listed as
	// flashed (new on this request, or carried to the next one).
	for _, list := range []string{"_flash.old", "_flash.new"} {
		var listed bool

		switch keys := s[list].(type) {
		case []any:
			listed = slices.Contains(keys, any(key))
		case []string:
			listed = slices.Contains(keys, key)
		}

		// A listed key whose value was pulled is not a flash anymore.
		if v, ok := s[key]; listed && ok {
			return v, true
		}
	}

	return nil, false
}
