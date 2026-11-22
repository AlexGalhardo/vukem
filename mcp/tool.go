package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/AlexGalhardo/vukem/errs"
	"github.com/AlexGalhardo/vukem/openapi"
	"github.com/AlexGalhardo/vukem/z"
)

var nameRe = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,128}$`)

// ToolAnnotations are behaviour hints for clients. They are not enforced and
// clients treat them as untrusted.
type ToolAnnotations struct {
	Title           string `json:"title,omitempty"`
	ReadOnlyHint    *bool  `json:"readOnlyHint,omitempty"`
	DestructiveHint *bool  `json:"destructiveHint,omitempty"`
	IdempotentHint  *bool  `json:"idempotentHint,omitempty"`
	OpenWorldHint   *bool  `json:"openWorldHint,omitempty"`
}

// Tool describes a tool. Name is 1 to 128 characters of A-Z, a-z, 0-9, _, -
// and .; Description is what the model reads to decide when to call it.
type Tool struct {
	Name        string
	Title       string
	Description string
	Annotations *ToolAnnotations
	Icons       []Icon
	// Scopes are the OAuth scopes a token must hold to see and call the tool.
	// They need HTTPConfig.Verifier: the HTTP handler refuses to start
	// otherwise. stdio takes its credentials from the environment and does
	// not check scopes.
	Scopes []string
}

// Result is what a tool function may return to control the whole result:
// content blocks, structured content and the error flag.
type Result struct {
	Content    []Content
	Structured any
	IsError    bool
}

// TextResult is a tool result with its own text next to the structured value:
// Data becomes structuredContent and is checked against its own `validate`
// tags and described by outputSchema, as a plain struct result is, while Text
// replaces the JSON copy as the content block the model reads (a short
// summary, a warning about untrusted data). An empty Text falls back to the
// JSON of Data. Use it as the R of AddTool; it is not a Result, so
// outputSchema is kept.
type TextResult[T any] struct {
	Text string
	Data T
}

// textual lets AddTool read a TextResult without knowing its T.
type textual interface {
	text() string
	data() any
	dataType() reflect.Type
	check() error
}

func (r TextResult[T]) text() string         { return r.Text }
func (r TextResult[T]) data() any            { return r.Data }
func (TextResult[T]) dataType() reflect.Type { return reflect.TypeFor[T]() }

func (r TextResult[T]) check() error {
	if reflect.TypeFor[T]().Kind() != reflect.Struct {
		return nil
	}

	return z.For[T]().Check(r.Data)
}

type tool struct {
	def    map[string]any
	scopes []string
	// run parses args and calls the function.
	run func(ctx context.Context, args map[string]any) (*callToolResult, error)
}

type callToolResult struct {
	Content           []Content `json:"content"`
	StructuredContent any       `json:"structuredContent,omitempty"`
	IsError           bool      `json:"isError"`
}

// ToolError is an error the model should see and may act on: bad input,
// a business rule, a failed call to an API. Anything else a tool returns is
// logged and answered as a generic internal error.
type ToolError struct{ Message string }

// Error implements error.
func (e *ToolError) Error() string { return e.Message }

// ToolErrorf returns a *ToolError.
func ToolErrorf(format string, args ...any) error {
	return &ToolError{Message: fmt.Sprintf(format, args...)}
}

// schemaOf builds a self-contained JSON Schema 2020-12 for t from the same
// tags as OpenAPI: nested types go under $defs.
func schemaOf(t reflect.Type, input bool) map[string]any {
	sc := openapi.NewSchemas()

	var out map[string]any
	if t.Kind() == reflect.Struct {
		out = sc.Object(t, nil)
		out["type"] = "object"
	} else {
		out = sc.For(t)
	}

	if input {
		if _, ok := out["additionalProperties"]; !ok {
			out["additionalProperties"] = false
		}
	}

	if defs := sc.Components(); len(defs) > 0 {
		d := map[string]any{}
		for k, v := range defs {
			d[k] = v
		}

		out["$defs"] = d
	}

	rewriteRefs(out)

	return out
}

func rewriteRefs(v any) {
	switch x := v.(type) {
	case map[string]any:
		for k, e := range x {
			if s, ok := e.(string); ok && k == "$ref" {
				x[k] = strings.Replace(s, "#/components/schemas/", "#/$defs/", 1)

				continue
			}

			rewriteRefs(e)
		}
	case []any:
		for _, e := range x {
			rewriteRefs(e)
		}
	}
}

// AddTool registers fn as a tool. P must be a struct: its fields become the
// input schema and the arguments are parsed with a strict z schema built
// from its `validate` tags before fn runs. R is the structured result: it is
// returned as structuredContent with its JSON as the text fallback, and
// checked against its own `validate` tags. R may also be a Result for full
// control, or a TextResult to send its own text next to the structured value.
// AddTool panics on a bad name, a duplicate or a non-struct P, like
// http.ServeMux.
func AddTool[P, R any](s *Server, t Tool, fn func(context.Context, P) (R, error)) {
	if !nameRe.MatchString(t.Name) {
		panic(fmt.Sprintf("mcp: invalid tool name %q", t.Name))
	}

	if reflect.TypeFor[P]().Kind() != reflect.Struct {
		panic(fmt.Sprintf("mcp: tool %q: input type %s is not a struct", t.Name, reflect.TypeFor[P]()))
	}

	args := z.For[P]().Strict()
	rt := reflect.TypeFor[R]()
	_, custom := any(*new(R)).(Result)

	if tx, ok := any(*new(R)).(textual); ok {
		rt = tx.dataType()
	}

	def := map[string]any{"name": t.Name, "inputSchema": schemaOf(reflect.TypeFor[P](), true)}
	setIf(def, "title", t.Title)
	setIf(def, "description", t.Description)

	if t.Annotations != nil {
		def["annotations"] = t.Annotations
	}

	if len(t.Icons) > 0 {
		def["icons"] = t.Icons
	}

	var outCheck func(R) error

	if !custom && (rt.Kind() == reflect.Struct || rt.Kind() == reflect.Map || rt.Kind() == reflect.Slice) {
		def["outputSchema"] = schemaOf(rt, false)

		if _, isText := any(*new(R)).(textual); !isText && rt.Kind() == reflect.Struct {
			outCheck = z.For[R]().Check
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, dup := s.tools[t.Name]; dup {
		panic(fmt.Sprintf("mcp: tool %q registered twice", t.Name))
	}

	s.tools[t.Name] = &tool{
		def:    def,
		scopes: slices.Clone(t.Scopes),
		run: func(ctx context.Context, in map[string]any) (*callToolResult, error) {
			p, err := args.Parse(in)
			if err != nil {
				if is, ok := z.AsIssues(err); ok {
					return &callToolResult{
						Content: []Content{Text("Invalid arguments: " + is.Error())},
						IsError: true,
					}, nil
				}

				return nil, err
			}

			r, err := fn(ctx, p)
			if err != nil {
				return nil, err
			}

			if res, ok := any(r).(Result); ok {
				return &callToolResult{
					Content:           res.Content,
					StructuredContent: res.Structured,
					IsError:           res.IsError,
				}, nil
			}

			return toolOutput(t.Name, r, outCheck, def["outputSchema"] != nil)
		},
	}
}

// toolOutput builds the result of a tool whose R is not a Result: the value
// (or a TextResult's data) as structuredContent when there is an outputSchema,
// and its text, or the JSON of the value, as the content block.
func toolOutput[R any](name string, r R, check func(R) error, structured bool) (*callToolResult, error) {
	var (
		value   any = r
		summary string
	)

	if tx, ok := any(r).(textual); ok {
		value, summary = tx.data(), tx.text()

		if err := tx.check(); err != nil {
			return nil, fmt.Errorf("tool %s output violates its schema: %w", name, err)
		}
	}

	if check != nil {
		if err := check(r); err != nil {
			return nil, fmt.Errorf("tool %s output violates its schema: %w", name, err)
		}
	}

	if summary == "" {
		text, err := json.Marshal(value)
		if err != nil {
			return nil, err
		}

		summary = string(text)
	}

	out := &callToolResult{Content: []Content{Text(summary)}}
	if structured {
		out.StructuredContent = value
	}

	return out, nil
}

func setIf(m map[string]any, k, v string) {
	if v != "" {
		m[k] = v
	}
}

// missingScopes returns the scopes of need that the caller lacks. Over HTTP a
// request without a verified token holds none, so a scoped tool stays shut;
// stdio takes its credentials from the environment and checks nothing.
func (c *call) missingScopes(need []string) []string {
	if len(need) == 0 || (c.token == nil && !c.scopeStrict) {
		return nil
	}

	var missing []string

	for _, sc := range need {
		if c.token == nil || !slices.Contains(c.token.Scopes, sc) {
			missing = append(missing, sc)
		}
	}

	return missing
}

func (s *Server) listTools(c *call) (any, *Error) {
	s.mu.RLock()

	names := make([]string, 0, len(s.tools))

	for n, t := range s.tools {
		if len(c.missingScopes(t.scopes)) == 0 {
			names = append(names, n)
		}
	}

	if len(s.tools) == 0 {
		s.mu.RUnlock()

		return nil, methodNotFound(c.msg.Method)
	}

	defs := make(map[string]map[string]any, len(names))
	for _, n := range names {
		defs[n] = s.tools[n].def
	}

	s.mu.RUnlock()

	return pageResult(s, c, "tools", names, func(n string) any { return defs[n] })
}

func (s *Server) callTool(ctx context.Context, c *call) (any, *Error) {
	var p struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
		inputParams
	}

	if perr := decodeParams(c.msg.Params, &p); perr != nil {
		return nil, perr
	}

	s.mu.RLock()
	t := s.tools[p.Name]
	s.mu.RUnlock()

	if t == nil {
		return nil, invalidParams("unknown tool " + quote(p.Name))
	}

	if missing := c.missingScopes(t.scopes); len(missing) > 0 {
		c.scopeMissing = missing

		return nil, &Error{Code: CodeInvalidParams, Message: "Insufficient scope"}
	}

	if p.Arguments == nil {
		p.Arguments = map[string]any{}
	}

	ctx, perr := s.withInputs(ctx, c, p.inputParams, p.Name, p.Arguments)
	if perr != nil {
		return nil, perr
	}

	res, err := safeRun(func() (*callToolResult, error) { return t.run(ctx, p.Arguments) })
	if err != nil {
		return s.failure(ctx, c, "tool "+p.Name, err)
	}

	return res, nil
}

// safeRun turns a panic in user code into an error.
func safeRun[T any](fn func() (T, error)) (out T, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = &panicError{value: r}
		}
	}()

	return fn()
}

type panicError struct{ value any }

func (e *panicError) Error() string { return fmt.Sprint("panic: ", e.value) }

func quote(s string) string {
	return strconv.Quote(s)
}

// rpcFailure is failure for everything that is not a tool call (resources/read,
// prompts/get, completion/complete). Those methods have no tool-shaped result,
// so a *ToolError or a 4xx *errs.HTTPError is a JSON-RPC error here: an invalid
// params error for a bad request or a missing thing, -32030 for a refusal,
// -32023 for a usage limit. The message is the one the developer wrote for the
// caller; a 5xx and any other error stay a bare internal error, logged.
func (s *Server) rpcFailure(ctx context.Context, c *call, what string, err error) (any, *Error) {
	if te, ok := errors.AsType[*ToolError](err); ok {
		return nil, &Error{Code: CodeInvalidParams, Message: te.Message}
	}

	if he, ok := errors.AsType[*errs.HTTPError](
		err,
	); ok && he.Status >= 400 &&
		he.Status < http.StatusInternalServerError {
		return nil, httpStatusError(he)
	}

	return s.failure(ctx, c, what, err)
}

func httpStatusError(he *errs.HTTPError) *Error {
	switch he.Status {
	case http.StatusUnauthorized, http.StatusForbidden:
		return &Error{Code: CodeForbidden, Message: he.Text()}
	case http.StatusNotFound, http.StatusGone:
		return &Error{Code: CodeInvalidParams, Message: "Not found"}
	case http.StatusTooManyRequests:
		e := &Error{Code: CodeRateLimited, Message: he.Text()}
		if retry := he.Headers.Get("Retry-After"); retry != "" {
			e.Data = map[string]any{"retryAfter": retry}
		}

		return e
	default:
		return &Error{Code: CodeInvalidParams, Message: he.Text()}
	}
}

// failure maps an error returned by user code to what the client may see.
func (s *Server) failure(ctx context.Context, c *call, what string, err error) (any, *Error) {
	if ir, ok := errors.AsType[*inputRequiredError](err); ok {
		return s.inputRequired(c, ir)
	}

	if _, ok := errors.AsType[*capabilityError](err); ok {
		c.capMissing = true

		return nil, &Error{
			Code: CodeMissingClientCapability, Message: "Missing required client capability",
			Data: map[string]any{"requiredCapabilities": map[string]any{"elicitation": map[string]any{}}},
		}
	}

	if te, ok := errors.AsType[*ToolError](err); ok {
		return &callToolResult{Content: []Content{Text(te.Message)}, IsError: true}, nil
	}

	if he, ok := errors.AsType[*errs.HTTPError](
		err,
	); ok && he.Status >= 400 &&
		he.Status < http.StatusInternalServerError {
		return &callToolResult{Content: []Content{Text(he.Text())}, IsError: true}, nil
	}

	if errors.Is(err, ErrNotFound) {
		return nil, &Error{Code: CodeInvalidParams, Message: "Not found"}
	}

	attrs := []any{"what", what, "err", err}
	if pe, ok := errors.AsType[*panicError](err); ok {
		attrs = append(attrs, "panic", fmt.Sprint(pe.value))
	}

	s.cfg.Logger.ErrorContext(ctx, "mcp: handler failed", attrs...)

	return nil, &Error{Code: CodeInternal, Message: "Internal error"}
}
