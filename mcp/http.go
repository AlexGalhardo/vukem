package mcp

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// HTTPConfig configures the Streamable HTTP handler.
type HTTPConfig struct {
	// AllowedOrigins lists the browser origins ("https://app.example.com")
	// allowed to call the endpoint. A request with an Origin header that is
	// not listed is refused with 403 (DNS rebinding defence); requests
	// without one, which is what non-browser clients send, pass. Empty means
	// no browser origin is allowed.
	AllowedOrigins []string

	// Verifier validates bearer tokens. Without it the endpoint is open, so
	// HTTPHandler refuses to start unless InsecureNoAuth is set.
	Verifier TokenVerifier
	// InsecureNoAuth serves the endpoint without authorization. Tools that
	// declare Scopes are then hidden and refused. Use it for localhost only.
	InsecureNoAuth bool

	// Resource is the canonical URI of this MCP endpoint
	// ("https://api.example.com/mcp"), the audience of the access tokens.
	// Required with Verifier.
	Resource string
	// AuthorizationServers are the issuer URLs published in the protected
	// resource metadata (RFC 9728). Required with Verifier.
	AuthorizationServers []string
	// ScopesSupported is published in the metadata and in the 401 challenge.
	ScopesSupported []string
	// ResourceName is a human readable name for the metadata.
	ResourceName string
}

// HTTPHandler is the http.Handler of the MCP endpoint. Mount it on the
// Vukem router for POST; GET and DELETE are answered 405 because the
// revision has no GET stream and no sessions.
type HTTPHandler struct {
	s        *Server
	cfg      HTTPConfig
	origins  map[string]struct{}
	resource string
	metaPath string
	metaURL  string
}

// HTTPHandler returns the handler for the endpoint, or an error for a
// configuration that would weaken security: no Verifier and no
// InsecureNoAuth, or a Verifier without Resource and AuthorizationServers.
func (s *Server) HTTPHandler(cfg HTTPConfig) (*HTTPHandler, error) {
	h := &HTTPHandler{s: s, cfg: cfg, origins: map[string]struct{}{}}

	for _, o := range cfg.AllowedOrigins {
		n, ok := normalizeOrigin(o)
		if !ok || n == "null" {
			return nil, fmt.Errorf("mcp: invalid allowed origin %q", o)
		}

		h.origins[n] = struct{}{}
	}

	if cfg.Verifier == nil {
		if !cfg.InsecureNoAuth {
			return nil, errors.New(
				"mcp: HTTPConfig.Verifier is required; set InsecureNoAuth to serve without authorization",
			)
		}

		return h, nil
	}

	u, err := url.Parse(cfg.Resource)
	if err != nil || u.Scheme == "" || u.Host == "" || u.Fragment != "" || (u.Scheme != "https" && u.Scheme != "http") {
		return nil, fmt.Errorf(
			"mcp: HTTPConfig.Resource %q must be an absolute http(s) URL without fragment",
			cfg.Resource,
		)
	}

	if len(cfg.AuthorizationServers) == 0 {
		return nil, errors.New("mcp: HTTPConfig.AuthorizationServers is required with a Verifier")
	}

	h.resource = strings.TrimSuffix(cfg.Resource, "/")
	h.metaPath = "/.well-known/oauth-protected-resource" + strings.TrimSuffix(u.EscapedPath(), "/")
	h.metaURL = u.Scheme + "://" + u.Host + h.metaPath

	return h, nil
}

// MetadataPath is the path of the protected resource metadata (RFC 9728):
// "/.well-known/oauth-protected-resource" plus the path of Resource. Mount
// MetadataHandler there, on the same host as Resource.
func (h *HTTPHandler) MetadataPath() string { return h.metaPath }

// MetadataHandler serves the protected resource metadata document.
func (h *HTTPHandler) MetadataHandler() http.Handler {
	doc := map[string]any{
		"resource":                 h.resource,
		"authorization_servers":    h.cfg.AuthorizationServers,
		"bearer_methods_supported": []string{"header"},
	}

	if len(h.cfg.ScopesSupported) > 0 {
		doc["scopes_supported"] = h.cfg.ScopesSupported
	}

	if h.cfg.ResourceName != "" {
		doc["resource_name"] = h.cfg.ResourceName
	}

	body, merr := json.Marshal(doc)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)

			return
		}

		if merr != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)

			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "public, max-age=3600")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		_, _ = w.Write(body)
	})
}

// normalizeOrigin lowercases scheme and host of an origin.
func normalizeOrigin(o string) (string, bool) {
	if o == "null" {
		return o, true
	}

	u, err := url.Parse(o)
	if err != nil || u.Scheme == "" || u.Host == "" || (u.Path != "" && u.Path != "/") || u.RawQuery != "" ||
		u.User != nil {
		return "", false
	}

	return strings.ToLower(u.Scheme) + "://" + strings.ToLower(u.Host), true
}

func (h *HTTPHandler) originAllowed(r *http.Request) bool {
	vals := r.Header.Values("Origin")
	if len(vals) == 0 {
		return true
	}

	if len(vals) > 1 {
		return false
	}

	n, ok := normalizeOrigin(vals[0])
	if !ok {
		return false
	}

	_, allowed := h.origins[n]

	return allowed
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		status, b = http.StatusInternalServerError, []byte(
			`{"jsonrpc":"2.0","id":null,"error":{"code":-32603,"message":"Internal error"}}`,
		)
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_, _ = w.Write(b)
}

func writeError(w http.ResponseWriter, status int, id json.RawMessage, e *Error) {
	if len(id) == 0 {
		id = json.RawMessage("null")
	}

	writeJSON(w, status, response{JSONRPC: "2.0", ID: id, Error: e})
}

func quoteParam(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}

func (h *HTTPHandler) challenge(status int, errCode, scope string) string {
	parts := []string{}
	if errCode != "" {
		parts = append(parts, "error="+quoteParam(errCode))
	}

	if scope == "" && status == http.StatusUnauthorized {
		scope = strings.Join(h.cfg.ScopesSupported, " ")
	}

	if scope != "" {
		parts = append(parts, "scope="+quoteParam(scope))
	}

	parts = append(parts, "resource_metadata="+quoteParam(h.metaURL))

	return "Bearer " + strings.Join(parts, ", ")
}

// authorize verifies the bearer token. It writes the response and returns
// false when the request must stop.
func (h *HTTPHandler) authorize(w http.ResponseWriter, r *http.Request) (*TokenInfo, bool) {
	if h.cfg.Verifier == nil {
		return nil, true
	}

	deny := func(errCode string) (*TokenInfo, bool) {
		w.Header().Set("WWW-Authenticate", h.challenge(http.StatusUnauthorized, errCode, ""))
		writeError(w, http.StatusUnauthorized, nil, &Error{Code: CodeInvalidRequest, Message: "Unauthorized"})

		return nil, false
	}

	scheme, tok, ok := strings.Cut(r.Header.Get("Authorization"), " ")
	if r.Header.Get("Authorization") == "" {
		return deny("")
	}

	tok = strings.TrimSpace(tok)
	if !ok || !strings.EqualFold(scheme, "Bearer") || tok == "" {
		return deny("invalid_token")
	}

	info, err := h.cfg.Verifier.VerifyAccessToken(r.Context(), tok, h.resource)

	switch {
	case errors.Is(err, ErrInvalidToken):
		return deny("invalid_token")
	case err != nil:
		h.s.cfg.Logger.ErrorContext(r.Context(), "mcp: token verification failed", "err", err)
		writeError(w, http.StatusServiceUnavailable, nil, &Error{Code: CodeInternal, Message: "Internal error"})

		return nil, false
	case info == nil,
		strings.TrimSuffix(info.Resource, "/") != h.resource,
		!info.ExpiresAt.IsZero() && !info.ExpiresAt.After(time.Now()):
		return deny("invalid_token")
	}

	return info, true
}

func decodeHeaderValue(v string) (string, bool) {
	const prefix, suffix = "=?base64?", "?="

	if strings.HasPrefix(v, prefix) && strings.HasSuffix(v, suffix) && len(v) >= len(prefix)+len(suffix) {
		b, err := base64.StdEncoding.DecodeString(v[len(prefix) : len(v)-len(suffix)])

		return string(b), err == nil
	}

	for _, r := range v {
		if r < 0x20 && r != '\t' || r > 0x7e {
			return "", false
		}
	}

	return v, true
}

func headerMismatch(msg string) *Error {
	return &Error{Code: CodeHeaderMismatch, Message: "Header mismatch: " + msg}
}

// checkHeaders validates the standard request headers against the body.
func checkHeaders(r *http.Request, c *call) *Error {
	if c.modern {
		hv := r.Header.Get("MCP-Protocol-Version")
		if hv == "" {
			return headerMismatch("MCP-Protocol-Version header is missing")
		}

		if hv != c.meta.Version {
			return headerMismatch("MCP-Protocol-Version does not match the request metadata")
		}

		if r.Header.Get("Mcp-Method") == "" {
			return headerMismatch("Mcp-Method header is missing")
		}
	}

	if m := r.Header.Get("Mcp-Method"); m != "" && m != c.msg.Method {
		return headerMismatch("Mcp-Method does not match the method")
	}

	if !c.modern {
		return nil
	}

	var field string

	switch c.msg.Method {
	case "tools/call", "prompts/get":
		field = "name"
	case "resources/read":
		field = "uri"
	default:
		return nil
	}

	var p map[string]json.RawMessage
	_ = json.Unmarshal(c.msg.Params, &p)

	var want string
	if err := json.Unmarshal(p[field], &want); err != nil {
		return invalidParams("missing " + field)
	}

	raw := r.Header.Get("Mcp-Name")
	if raw == "" {
		return headerMismatch("Mcp-Name header is missing")
	}

	got, ok := decodeHeaderValue(raw)
	if !ok {
		return headerMismatch("Mcp-Name has invalid characters")
	}

	if got != want {
		return headerMismatch("Mcp-Name does not match the request body")
	}

	return nil
}

func acceptsJSON(accept string) bool {
	if accept == "" {
		return true
	}

	for part := range strings.SplitSeq(accept, ",") {
		mt, _, _ := mime.ParseMediaType(strings.TrimSpace(part))
		if mt == "application/json" || mt == "*/*" || mt == "application/*" {
			return true
		}
	}

	return false
}

// ServeHTTP implements http.Handler.
func (h *HTTPHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !h.originAllowed(r) {
		writeError(w, http.StatusForbidden, nil, &Error{Code: CodeInvalidRequest, Message: "Forbidden origin"})

		return
	}

	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeError(w, http.StatusMethodNotAllowed, nil, &Error{Code: CodeInvalidRequest, Message: "Method not allowed"})

		return
	}

	// RFC 6750 section 2.3: a token in the query string leaks into logs and
	// referrers, so the request is refused and the value never read.
	if _, ok := r.URL.Query()["access_token"]; ok {
		writeError(
			w,
			http.StatusBadRequest,
			nil,
			&Error{Code: CodeInvalidRequest, Message: "Tokens must not be sent in the query string"},
		)

		return
	}

	token, ok := h.authorize(w, r)
	if !ok {
		return
	}

	if mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type")); err != nil || mt != "application/json" {
		writeError(
			w,
			http.StatusUnsupportedMediaType,
			nil,
			&Error{Code: CodeInvalidRequest, Message: "Content-Type must be application/json"},
		)

		return
	}

	if !acceptsJSON(r.Header.Get("Accept")) {
		writeError(
			w,
			http.StatusNotAcceptable,
			nil,
			&Error{Code: CodeInvalidRequest, Message: "Accept must allow application/json"},
		)

		return
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, h.s.cfg.MaxMessageBytes))
	if err != nil {
		if _, big := errors.AsType[*http.MaxBytesError](err); big {
			writeError(
				w,
				http.StatusRequestEntityTooLarge,
				nil,
				&Error{Code: CodeInvalidRequest, Message: "Message too large"},
			)

			return
		}

		writeError(w, http.StatusBadRequest, nil, &Error{Code: CodeParse, Message: "Parse error"})

		return
	}

	h.serveMessage(w, r, body, token)
}

func (h *HTTPHandler) serveMessage(w http.ResponseWriter, r *http.Request, body []byte, token *TokenInfo) {
	if t := bytes.TrimSpace(body); len(t) > 0 && t[0] == '[' {
		writeError(
			w,
			http.StatusBadRequest,
			nil,
			&Error{Code: CodeInvalidRequest, Message: "Batches are not supported"},
		)

		return
	}

	var msg message
	if err := json.Unmarshal(body, &msg); err != nil {
		writeError(w, http.StatusBadRequest, nil, &Error{Code: CodeParse, Message: "Parse error"})

		return
	}

	if msg.JSONRPC != "2.0" || msg.Method == "" {
		writeError(
			w,
			http.StatusBadRequest,
			safeID(msg.ID),
			&Error{Code: CodeInvalidRequest, Message: "Invalid request"},
		)

		return
	}

	if len(msg.ID) == 0 {
		w.WriteHeader(http.StatusAccepted)

		return
	}

	if !validID(msg.ID) {
		writeError(w, http.StatusBadRequest, nil, &Error{Code: CodeInvalidRequest, Message: "Invalid request id"})

		return
	}

	c := &call{msg: msg, token: token, scopeStrict: true}

	fallback := ""
	if h.s.supported("2025-03-26") {
		fallback = "2025-03-26" // clients older than 2025-06-18 sent no version header
	}

	if hv := r.Header.Get("MCP-Protocol-Version"); hv != "" {
		fallback = hv
	}

	rerr := h.s.resolve(c, r.Header.Get("MCP-Protocol-Version"), fallback)
	if rerr == nil {
		rerr = checkHeaders(r, c)
	}

	if rerr != nil {
		writeError(w, http.StatusBadRequest, msg.ID, rerr)

		return
	}

	ctx := r.Context()
	if token != nil {
		ctx = WithTokenInfo(ctx, token)
	}

	st := &sseStream{w: w, rc: http.NewResponseController(w)}
	c.notify = st.notify
	c.keepAlive = st.keepAlive

	res, rerr := h.s.handle(ctx, c)

	if ctx.Err() != nil {
		return // client gone: cancelled, nothing more to send
	}

	resp := response{JSONRPC: "2.0", ID: msg.ID, Result: res, Error: rerr}

	if st.finish(resp) {
		return
	}

	switch {
	case rerr == nil:
		writeJSON(w, http.StatusOK, resp)
	case len(c.scopeMissing) > 0:
		w.Header().
			Set("WWW-Authenticate", h.challenge(http.StatusForbidden, "insufficient_scope", strings.Join(c.scopeMissing, " ")))
		writeJSON(w, http.StatusForbidden, resp)
	case c.capMissing:
		writeJSON(w, http.StatusBadRequest, resp)
	case rerr.Code == CodeMethodNotFound && c.modern:
		writeJSON(w, http.StatusNotFound, resp)
	default:
		writeJSON(w, http.StatusOK, resp)
	}
}

// sseStream switches the response to Server-Sent Events the first time the
// request has something to stream.
type sseStream struct {
	w  http.ResponseWriter
	rc *http.ResponseController

	mu      sync.Mutex
	started bool
	closed  bool
}

func (s *sseStream) start() {
	if s.started {
		return
	}

	h := s.w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache, no-store")
	h.Set("X-Accel-Buffering", "no")
	h.Set("X-Content-Type-Options", "nosniff")
	s.w.WriteHeader(http.StatusOK)

	s.started = true
}

func (s *sseStream) event(v any) {
	b, err := json.Marshal(v)
	if err != nil {
		return
	}

	_, _ = s.w.Write(append(append([]byte("event: message\ndata: "), b...), '\n', '\n'))
	_ = s.rc.Flush()
}

func (s *sseStream) notify(method string, params any) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return
	}

	s.start()
	s.event(notification{JSONRPC: "2.0", Method: method, Params: params})
}

func (s *sseStream) keepAlive() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.started && !s.closed {
		_, _ = s.w.Write([]byte(":\n\n"))
		_ = s.rc.Flush()
	}
}

// finish sends the final response on the stream when one was opened and
// reports whether it did.
func (s *sseStream) finish(resp response) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.closed = true

	if !s.started {
		return false
	}

	s.event(resp)

	return true
}
