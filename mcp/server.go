package mcp

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"time"
)

// Config configures a Server. Name and Version are required.
type Config struct {
	// Name, Title and Version identify the server in server/discover and in
	// the _meta of every result.
	Name, Title, Version string
	// Instructions is optional guidance for the model on using the server.
	Instructions string
	// Logger receives internal errors and panics, which are never shown to
	// clients (default: discarded).
	Logger *slog.Logger
	// MaxMessageBytes caps one inbound message (default 1 MiB).
	MaxMessageBytes int64
	// PageSize is the number of items per list page (default 50).
	PageSize int
	// CacheTTL is the ttlMs hint on list results (default 0: do not cache).
	// Caches are always private because lists may vary by the caller's token.
	CacheTTL time.Duration
	// StateKey signs the requestState of input_required results. It must be
	// the same on every instance behind a load balancer; when empty a random
	// per-process key is used, so a retry that lands on another instance is
	// refused.
	StateKey []byte
	// StateTTL is how long a requestState stays valid (default 10 minutes).
	StateTTL time.Duration
	// MaxInFlight caps concurrent requests per stdio connection (default 32).
	MaxInFlight int
	// MaxSubscriptions caps open subscriptions/listen streams (default 1000).
	MaxSubscriptions int
	// KeepAlive is the interval of SSE comment lines on idle streams
	// (default 25 seconds).
	KeepAlive time.Duration
	// ShutdownGrace is how long ServeStdio lets running requests finish after
	// its input closes before cancelling them (default 5 seconds).
	ShutdownGrace time.Duration
	// Versions lists the protocol revisions served (default: 2026-07-28 and
	// the four earlier ones).
	Versions []string
}

// Server is an MCP server. Register tools, resources and prompts, then serve
// it with ServeStdio or HTTPHandler. It is safe for concurrent use.
type Server struct {
	cfg Config

	mu        sync.RWMutex
	tools     map[string]*tool
	resources map[string]*resource
	templates map[string]*template
	prompts   map[string]*prompt

	subMu sync.Mutex
	subs  map[*subscription]struct{}

	done      chan struct{}
	closeOnce sync.Once
}

// NewServer returns a Server. It panics when Name or Version is empty or a
// listed version is unknown, mistakes that surface at boot.
func NewServer(cfg Config) *Server {
	if cfg.Name == "" || cfg.Version == "" {
		panic("mcp: Config.Name and Config.Version are required")
	}

	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.DiscardHandler)
	}

	if cfg.MaxMessageBytes <= 0 {
		cfg.MaxMessageBytes = 1 << 20
	}

	if cfg.PageSize <= 0 {
		cfg.PageSize = 50
	}

	if cfg.StateTTL <= 0 {
		cfg.StateTTL = 10 * time.Minute
	}

	if cfg.MaxInFlight <= 0 {
		cfg.MaxInFlight = 32
	}

	if cfg.MaxSubscriptions <= 0 {
		cfg.MaxSubscriptions = 1000
	}

	if cfg.KeepAlive <= 0 {
		cfg.KeepAlive = 25 * time.Second
	}

	if cfg.ShutdownGrace <= 0 {
		cfg.ShutdownGrace = 5 * time.Second
	}

	if len(cfg.Versions) == 0 {
		cfg.Versions = append([]string{Version}, legacyVersions...)
	}

	for _, v := range cfg.Versions {
		if v != Version && !slices.Contains(legacyVersions, v) {
			panic(fmt.Sprintf("mcp: unknown protocol version %q", v))
		}
	}

	if len(cfg.StateKey) == 0 {
		cfg.StateKey = make([]byte, 32)
		if _, err := rand.Read(cfg.StateKey); err != nil {
			panic("mcp: random state key: " + err.Error())
		}
	}

	return &Server{
		cfg:       cfg,
		tools:     map[string]*tool{},
		resources: map[string]*resource{},
		templates: map[string]*template{},
		prompts:   map[string]*prompt{},
		subs:      map[*subscription]struct{}{},
		done:      make(chan struct{}),
	}
}

// Close ends every subscriptions/listen stream gracefully. The server keeps
// answering ordinary requests.
func (s *Server) Close() { s.closeOnce.Do(func() { close(s.done) }) }

// call is one request being served.
type call struct {
	msg     message
	version string
	modern  bool
	meta    requestMeta
	token   *TokenInfo
	// scopeStrict makes a missing token count as holding no scope (HTTP).
	scopeStrict bool
	// notify sends a notification on the request's own response stream; nil
	// when the transport has none.
	notify func(method string, params any)
	// keepAlive writes an idle marker on the stream (HTTP only).
	keepAlive func()
	// scopeMissing is set when a tool needs scopes the token lacks; HTTP
	// answers 403.
	scopeMissing []string
	// capMissing is set when a request needs a client capability that the
	// request did not declare; HTTP answers 400.
	capMissing bool
	// stateKey and stateArgs identify the target of an input_required retry.
	stateKey  string
	stateArgs any
}

type ctxKey struct{}

// RequestInfo describes the request being served, for tool, resource and
// prompt functions.
type RequestInfo struct {
	// ProtocolVersion is the revision the client used for this request.
	ProtocolVersion string
	// ClientInfo is the self-reported client identity (not verified).
	ClientInfo *Implementation

	call   *call
	inputs map[string]json.RawMessage
}

// RequestFrom returns the request a tool, resource or prompt function is
// serving.
func RequestFrom(ctx context.Context) (*RequestInfo, bool) {
	ri, ok := ctx.Value(ctxKey{}).(*RequestInfo)

	return ri, ok
}

// Progress sends a notifications/progress for the request when the client
// asked for progress. progress must increase with every call; total is zero
// when unknown. It does nothing otherwise.
func Progress(ctx context.Context, progress, total float64, msg string) {
	ri, ok := RequestFrom(ctx)
	if !ok || len(ri.call.meta.ProgressToken) == 0 || ri.call.notify == nil {
		return
	}

	p := map[string]any{"progressToken": ri.call.meta.ProgressToken, "progress": progress}
	if total > 0 {
		p["total"] = total
	}

	if msg != "" {
		p["message"] = msg
	}

	ri.call.notify("notifications/progress", p)
}

// handle answers one request. It never panics.
func (s *Server) handle(ctx context.Context, c *call) (res json.RawMessage, rerr *Error) {
	defer func() {
		if r := recover(); r != nil {
			s.cfg.Logger.ErrorContext(ctx, "mcp: panic", "method", c.msg.Method, "panic", fmt.Sprint(r))

			res, rerr = nil, &Error{Code: CodeInternal, Message: "Internal error"}
		}
	}()

	if c.token != nil {
		ctx = WithTokenInfo(ctx, c.token)
	}

	ctx = context.WithValue(ctx, ctxKey{}, &RequestInfo{
		ProtocolVersion: c.version, ClientInfo: c.meta.ClientInfo, call: c,
	})

	out, err := s.route(ctx, c)
	if err != nil {
		return nil, err
	}

	b, merr := s.finish(c, out)
	if merr != nil {
		s.cfg.Logger.ErrorContext(ctx, "mcp: encode result", "method", c.msg.Method, "err", merr)

		return nil, &Error{Code: CodeInternal, Message: "Internal error"}
	}

	return b, nil
}

// finish adds resultType and serverInfo to a result of the modern revision.
func (s *Server) finish(c *call, out any) (json.RawMessage, error) {
	raw, err := json.Marshal(out)
	if err != nil {
		return nil, err
	}

	if !c.modern {
		return raw, nil
	}

	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, err
	}

	if _, ok := obj["resultType"]; !ok {
		obj["resultType"] = json.RawMessage(`"complete"`)
	}

	meta := map[string]json.RawMessage{}
	if m, ok := obj["_meta"]; ok {
		if err := json.Unmarshal(m, &meta); err != nil {
			return nil, err
		}
	}

	info, err := json.Marshal(Implementation{Name: s.cfg.Name, Title: s.cfg.Title, Version: s.cfg.Version})
	if err != nil {
		return nil, err
	}

	meta[metaServerInfo] = info

	if obj["_meta"], err = json.Marshal(meta); err != nil {
		return nil, err
	}

	return json.Marshal(obj)
}

func (s *Server) route(ctx context.Context, c *call) (any, *Error) {
	modernOnly := func(fn func() (any, *Error)) (any, *Error) {
		if !c.modern {
			return nil, methodNotFound(c.msg.Method)
		}

		return fn()
	}

	switch c.msg.Method {
	case "initialize":
		return s.initialize(c)
	case "server/discover":
		return modernOnly(func() (any, *Error) { return s.discover(), nil })
	case "ping":
		if c.modern {
			return nil, methodNotFound(c.msg.Method) // removed in 2026-07-28
		}

		return map[string]any{}, nil
	case "tools/list":
		return s.listTools(c)
	case "tools/call":
		return s.callTool(ctx, c)
	case "resources/list":
		return s.listResources(c)
	case "resources/templates/list":
		return s.listTemplates(c)
	case "resources/read":
		return s.readResource(ctx, c)
	case "prompts/list":
		return s.listPrompts(c)
	case "prompts/get":
		return s.getPrompt(ctx, c)
	case "completion/complete":
		return s.complete(ctx, c)
	case "subscriptions/listen":
		return modernOnly(func() (any, *Error) { return s.listen(ctx, c) })
	default:
		return nil, methodNotFound(c.msg.Method)
	}
}

func methodNotFound(m string) *Error {
	return &Error{Code: CodeMethodNotFound, Message: "Method not found: " + m}
}

func invalidParams(msg string) *Error {
	return &Error{Code: CodeInvalidParams, Message: "Invalid params: " + msg}
}

// capabilities lists what the server currently offers.
func (s *Server) capabilities(modern bool) map[string]any {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// The older revisions have no subscriptions/listen, so list changes and
	// resource updates cannot be announced to them.
	flag := func(m map[string]any) map[string]any {
		if modern {
			m["listChanged"] = true
		}

		return m
	}

	caps := map[string]any{}
	if len(s.tools) > 0 {
		caps["tools"] = flag(map[string]any{})
	}

	if len(s.resources) > 0 || len(s.templates) > 0 {
		r := flag(map[string]any{})
		if modern {
			r["subscribe"] = true
		}

		caps["resources"] = r
	}

	if len(s.prompts) > 0 {
		caps["prompts"] = flag(map[string]any{})
	}

	if s.hasCompletions() {
		caps["completions"] = map[string]any{}
	}

	return caps
}

func (s *Server) discover() any {
	return map[string]any{
		"supportedVersions": s.cfg.Versions,
		"capabilities":      s.capabilities(true),
		"instructions":      s.cfg.Instructions,
		"ttlMs":             s.cfg.CacheTTL.Milliseconds(),
		"cacheScope":        "private",
	}
}

// initialize answers the handshake of the older revisions. It keeps no
// state: the client sends its version again on every later HTTP request.
func (s *Server) initialize(c *call) (any, *Error) {
	var p struct {
		ProtocolVersion string `json:"protocolVersion"`
	}

	if perr := decodeParams(c.msg.Params, &p); perr != nil {
		return nil, perr
	}

	var legacy []string

	for _, v := range s.cfg.Versions {
		if v != Version {
			legacy = append(legacy, v)
		}
	}

	if len(legacy) == 0 {
		return nil, &Error{
			Code:    CodeInvalidRequest,
			Message: "initialize is not supported; send the protocol version in _meta",
			Data:    map[string]any{"supported": s.cfg.Versions},
		}
	}

	v := legacy[0]
	if slices.Contains(legacy, p.ProtocolVersion) {
		v = p.ProtocolVersion
	}

	c.version = v

	return map[string]any{
		"protocolVersion": v,
		"capabilities":    s.capabilities(false),
		"serverInfo":      Implementation{Name: s.cfg.Name, Title: s.cfg.Title, Version: s.cfg.Version},
		"instructions":    s.cfg.Instructions,
	}, nil
}

// decodeParams decodes params into v; absent params leave v zero.
func decodeParams(raw json.RawMessage, v any) *Error {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}

	if raw[0] != '{' {
		return invalidParams("params must be an object")
	}

	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()

	if err := dec.Decode(v); err != nil {
		return invalidParams("malformed params")
	}

	return nil
}

// supported reports whether the server serves revision v.
func (s *Server) supported(v string) bool { return slices.Contains(s.cfg.Versions, v) }

// resolve works out the revision of a request and validates its metadata.
// header is the MCP-Protocol-Version header (HTTP) and fallback the revision
// to assume when neither the body nor the header names one.
func (s *Server) resolve(c *call, header, fallback string) *Error {
	if c.msg.Method == "initialize" {
		return nil
	}

	var p struct {
		Meta requestMeta `json:"_meta"`
	}

	if perr := decodeParams(c.msg.Params, &p); perr != nil {
		return perr
	}

	c.meta = p.Meta

	v := c.meta.Version

	if v == "" {
		v = header
	}

	if v == "" {
		v = fallback
	}

	if v == "" {
		return invalidParams("missing _meta " + metaVersion)
	}

	if !s.supported(v) {
		return &Error{
			Code: CodeUnsupportedProtocolVersion, Message: "Unsupported protocol version",
			Data: map[string]any{"supported": s.cfg.Versions, "requested": v},
		}
	}

	c.version = v
	c.modern = v >= Version

	if c.modern {
		if c.meta.Version == "" {
			return invalidParams("missing _meta " + metaVersion)
		}

		if len(c.meta.Capabilities) == 0 || c.meta.Capabilities[0] != '{' {
			return invalidParams("missing _meta " + metaCapabilities)
		}
	}

	return nil
}
