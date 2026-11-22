package mcp

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// ErrNotFound is returned by a resource or prompt function for a target that
// does not exist. It is answered as -32602 (Invalid params), as the revision
// requires, without detail.
var ErrNotFound = errors.New("mcp: not found")

// Resource describes a fixed resource.
type Resource struct {
	URI         string
	Name        string
	Title       string
	Description string
	MIMEType    string
	Size        *int64
	Annotations *Annotations
	Icons       []Icon
	// TTL is the ttlMs hint on resources/read (default 0: do not cache).
	TTL time.Duration
}

// ResourceTemplate describes a family of resources by an RFC 6570 URI
// template. Only {var} (one path segment) and {+var} (reserved characters,
// slashes included) are understood.
type ResourceTemplate struct {
	URITemplate string
	Name        string
	Title       string
	Description string
	MIMEType    string
	Annotations *Annotations
	Icons       []Icon
	TTL         time.Duration
}

type resource struct {
	def  map[string]any
	ttl  time.Duration
	read func(ctx context.Context) ([]ResourceContents, error)
}

type template struct {
	def      map[string]any
	ttl      time.Duration
	re       *regexp.Regexp
	vars     []string
	read     func(ctx context.Context, uri string, vars map[string]string) ([]ResourceContents, error)
	complete CompleteFunc
}

// AddResource registers a fixed resource. It panics on an empty or repeated
// URI.
func AddResource(s *Server, r Resource, read func(context.Context) ([]ResourceContents, error)) {
	if r.URI == "" || r.Name == "" {
		panic("mcp: a resource needs a URI and a name")
	}

	def := map[string]any{"uri": r.URI, "name": r.Name}
	setIf(def, "title", r.Title)
	setIf(def, "description", r.Description)
	setIf(def, "mimeType", r.MIMEType)

	if r.Size != nil {
		def["size"] = *r.Size
	}

	if r.Annotations != nil {
		def["annotations"] = r.Annotations
	}

	if len(r.Icons) > 0 {
		def["icons"] = r.Icons
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, dup := s.resources[r.URI]; dup {
		panic(fmt.Sprintf("mcp: resource %q registered twice", r.URI))
	}

	s.resources[r.URI] = &resource{def: def, ttl: r.TTL, read: read}
}

var varRe = regexp.MustCompile(`\{(\+?)([A-Za-z0-9_]+)\}`)

// AddResourceTemplate registers a resource template; read receives the
// matched URI and its variables, percent-decoded. A variable that contains a
// ".." segment or a NUL never matches, so a template over files cannot
// escape its root. complete may be nil. It panics on a bad or repeated
// template.
func AddResourceTemplate(
	s *Server, t ResourceTemplate,
	read func(ctx context.Context, uri string, vars map[string]string) ([]ResourceContents, error),
	complete CompleteFunc,
) {
	if t.URITemplate == "" || t.Name == "" {
		panic("mcp: a resource template needs a URI template and a name")
	}

	var (
		vars []string
		expr strings.Builder
		last int
	)

	expr.WriteString("^")

	for _, m := range varRe.FindAllStringSubmatchIndex(t.URITemplate, -1) {
		expr.WriteString(regexp.QuoteMeta(t.URITemplate[last:m[0]]))

		if t.URITemplate[m[2]:m[3]] == "+" {
			expr.WriteString("(.+)")
		} else {
			expr.WriteString("([^/?#]+)")
		}

		vars = append(vars, t.URITemplate[m[4]:m[5]])
		last = m[1]
	}

	expr.WriteString(regexp.QuoteMeta(t.URITemplate[last:]) + "$")

	if len(vars) == 0 || strings.ContainsAny(varRe.ReplaceAllString(t.URITemplate, ""), "{}") {
		panic(fmt.Sprintf("mcp: invalid URI template %q", t.URITemplate))
	}

	def := map[string]any{"uriTemplate": t.URITemplate, "name": t.Name}
	setIf(def, "title", t.Title)
	setIf(def, "description", t.Description)
	setIf(def, "mimeType", t.MIMEType)

	if t.Annotations != nil {
		def["annotations"] = t.Annotations
	}

	if len(t.Icons) > 0 {
		def["icons"] = t.Icons
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, dup := s.templates[t.URITemplate]; dup {
		panic(fmt.Sprintf("mcp: resource template %q registered twice", t.URITemplate))
	}

	s.templates[t.URITemplate] = &template{
		def: def, ttl: t.TTL, re: regexp.MustCompile(expr.String()), vars: vars, read: read, complete: complete,
	}
}

func (t *template) match(uri string) (map[string]string, bool) {
	m := t.re.FindStringSubmatch(uri)
	if m == nil {
		return nil, false
	}

	out := make(map[string]string, len(t.vars))

	for i, name := range t.vars {
		v, err := url.PathUnescape(m[i+1])
		if err != nil || strings.ContainsRune(v, 0) {
			return nil, false
		}

		for seg := range strings.SplitSeq(strings.ReplaceAll(v, "\\", "/"), "/") {
			if seg == ".." {
				return nil, false
			}
		}

		out[name] = v
	}

	return out, true
}

func (s *Server) listResources(c *call) (any, *Error) {
	s.mu.RLock()
	empty := len(s.resources) == 0 && len(s.templates) == 0
	keys := make([]string, 0, len(s.resources))
	defs := map[string]any{}

	for k, r := range s.resources {
		keys = append(keys, k)
		defs[k] = r.def
	}

	s.mu.RUnlock()

	if empty {
		return nil, methodNotFound(c.msg.Method)
	}

	return pageResult(s, c, "resources", keys, func(k string) any { return defs[k] })
}

func (s *Server) listTemplates(c *call) (any, *Error) {
	s.mu.RLock()
	empty := len(s.resources) == 0 && len(s.templates) == 0
	keys := make([]string, 0, len(s.templates))
	defs := map[string]any{}

	for k, t := range s.templates {
		keys = append(keys, k)
		defs[k] = t.def
	}

	s.mu.RUnlock()

	if empty {
		return nil, methodNotFound(c.msg.Method)
	}

	return pageResult(s, c, "resourceTemplates", keys, func(k string) any { return defs[k] })
}

func (s *Server) readResource(ctx context.Context, c *call) (any, *Error) {
	var p struct {
		URI string `json:"uri"`
		inputParams
	}

	if perr := decodeParams(c.msg.Params, &p); perr != nil {
		return nil, perr
	}

	if p.URI == "" {
		return nil, invalidParams("missing uri")
	}

	s.mu.RLock()
	r := s.resources[p.URI]

	var (
		t    *template
		vars map[string]string
	)

	if r == nil {
		for _, cand := range s.templates {
			if v, ok := cand.match(p.URI); ok {
				t, vars = cand, v

				break
			}
		}
	}

	s.mu.RUnlock()

	if r == nil && t == nil {
		return nil, &Error{Code: CodeInvalidParams, Message: "Resource not found", Data: map[string]any{"uri": p.URI}}
	}

	ctx, perr := s.withInputs(ctx, c, p.inputParams, p.URI, nil)
	if perr != nil {
		return nil, perr
	}

	var (
		contents []ResourceContents
		err      error
		ttl      time.Duration
	)

	if r != nil {
		ttl = r.ttl
		contents, err = safeRun(func() ([]ResourceContents, error) { return r.read(ctx) })
	} else {
		ttl = t.ttl
		contents, err = safeRun(func() ([]ResourceContents, error) { return t.read(ctx, p.URI, vars) })
	}

	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, &Error{
				Code:    CodeInvalidParams,
				Message: "Resource not found",
				Data:    map[string]any{"uri": p.URI},
			}
		}

		if e, ok := errors.AsType[*Error](err); ok {
			return nil, e
		}

		return s.failure(ctx, c, "resource "+p.URI, err)
	}

	if len(contents) == 0 {
		// An empty list would be ambiguous with a missing resource.
		contents = []ResourceContents{{URI: p.URI}}
	}

	for i := range contents {
		if contents[i].URI == "" {
			contents[i].URI = p.URI
		}
	}

	return map[string]any{"contents": contents, "ttlMs": ttl.Milliseconds(), "cacheScope": "private"}, nil
}
