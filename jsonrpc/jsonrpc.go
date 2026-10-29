// Package jsonrpc serves JSON-RPC 2.0 (https://www.jsonrpc.org/specification)
// over HTTP. A Server is an http.Handler, so it mounts on the router like any
// handler and the router's middleware (auth, rate limits, request ids,
// logging, tracing) applies to every call:
//
//	rpc := jsonrpc.NewServer(jsonrpc.Config{Logger: app.Logger})
//	jsonrpc.Method(rpc, "items.get", func(ctx context.Context, p GetItem) (Item, error) {
//		return items.Find(ctx, p.ID)
//	})
//	r.Handle("POST /rpc", rpc)
//
// Params are a JSON object (or an array, matched to P's fields in order)
// decoded into P and validated with its `validate` tags (z.For); unknown
// members are rejected. Errors map to JSON-RPC error objects: an *Error as
// is, an *errs.ValidationError to -32602, a 4xx *errs.HTTPError to
// -32000-(status-400) with its client-safe message, anything else (and a
// panic) to -32603 without details, logged with Config.Logger.
//
// See https://github.com/AlexGalhardo/vukem/blob/main/docs/api.md#json-rpc.
package jsonrpc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"reflect"
	"strings"

	"github.com/AlexGalhardo/vukem/errs"
	"github.com/AlexGalhardo/vukem/z"
)

// Standard error codes of the specification.
const (
	CodeParseError     = -32700
	CodeInvalidRequest = -32600
	CodeMethodNotFound = -32601
	CodeInvalidParams  = -32602
	CodeInternalError  = -32603
)

// Error is a JSON-RPC error object. Return one from a method to choose the
// code; Message and Data are sent to the client as they are.
type Error struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// Error implements error.
func (e *Error) Error() string { return fmt.Sprintf("jsonrpc: %d %s", e.Code, e.Message) }

// Guard runs before a method; a non-nil error is the call's error and the
// method does not run (for notifications too). Use it for abilities:
//
//	func(ctx context.Context) error { return authz.Authorize(ctx, "items.delete") }
type Guard func(ctx context.Context) error

// Config sets the server limits. Zero values take the defaults.
type Config struct {
	// MaxBatch is the largest batch accepted (default 50); a larger one is
	// refused whole with -32600.
	MaxBatch int
	// MaxBodyBytes caps the request body (default 1 MiB); more is a 413.
	MaxBodyBytes int64
	// Logger receives internal errors and panics hidden from clients
	// (default: discarded).
	Logger *slog.Logger
}

// Server dispatches JSON-RPC requests to registered methods.
type Server struct {
	cfg     Config
	methods map[string]*method
}

type method struct {
	call   func(ctx context.Context, params json.RawMessage) (any, error)
	guards []Guard
}

// NewServer returns a Server with cfg's limits.
func NewServer(cfg Config) *Server {
	if cfg.MaxBatch <= 0 {
		cfg.MaxBatch = 50
	}

	if cfg.MaxBodyBytes <= 0 {
		cfg.MaxBodyBytes = 1 << 20
	}

	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.DiscardHandler)
	}

	return &Server{cfg: cfg, methods: map[string]*method{}}
}

// Method registers fn as name. P must be a struct (use struct{} for no
// params). Like http.ServeMux, it panics on a programming error: an empty,
// reserved ("rpc.") or duplicate name, or a non-struct P.
func Method[P, R any](s *Server, name string, fn func(context.Context, P) (R, error), guards ...Guard) {
	if name == "" || strings.HasPrefix(name, "rpc.") {
		panic(fmt.Sprintf("jsonrpc: invalid method name %q", name))
	}

	if _, dup := s.methods[name]; dup {
		panic(fmt.Sprintf("jsonrpc: method %q registered twice", name))
	}

	pt := reflect.TypeFor[P]()
	if pt.Kind() != reflect.Struct {
		panic(fmt.Sprintf("jsonrpc: method %q: params type %s is not a struct", name, pt))
	}

	schema := z.For[P]().Strict()
	fields := fieldNames(pt)

	s.methods[name] = &method{guards: guards, call: func(ctx context.Context, raw json.RawMessage) (any, error) {
		input, err := paramsInput(raw, fields)
		if err != nil {
			return nil, err
		}

		p, err := schema.Parse(input)
		if err != nil {
			if is, ok := z.AsIssues(err); ok {
				return nil, &Error{
					Code:    CodeInvalidParams,
					Message: "Invalid params",
					Data:    map[string]any{"fields": is.Fields()},
				}
			}

			return nil, err
		}

		return fn(ctx, p)
	}}
}

// fieldNames lists P's JSON member names in declaration order, the names
// positional (array) params are matched to.
func fieldNames(t reflect.Type) []string {
	var names []string

	for f := range t.Fields() {
		if !f.IsExported() {
			continue
		}

		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if name == "-" {
			continue
		}

		if name == "" {
			name = f.Name
		}

		names = append(names, name)
	}

	return names
}

var errParams = &Error{Code: CodeInvalidParams, Message: "Invalid params"}

func paramsInput(raw json.RawMessage, fields []string) (map[string]any, error) {
	input := map[string]any{}

	switch {
	case len(raw) == 0:
		return input, nil
	case raw[0] == '[':
		var list []any
		if err := decode(raw, &list); err != nil || len(list) > len(fields) {
			return nil, errParams
		}

		for i, v := range list {
			input[fields[i]] = v
		}

		return input, nil
	default:
		if err := decode(raw, &input); err != nil {
			return nil, errParams
		}

		return input, nil
	}
}

func decode(data []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()

	return dec.Decode(v)
}

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  json.RawMessage `json:"method"`
	Params  json.RawMessage `json:"params"`
	ID      json.RawMessage `json:"id"`
}

type response struct {
	JSONRPC string          `json:"jsonrpc"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *Error          `json:"error,omitempty"`
	ID      json.RawMessage `json:"id"`
}

var null = json.RawMessage("null")

// ServeHTTP implements http.Handler. Only POST with a JSON body is served
// (405, 415 otherwise; 413 over MaxBodyBytes). Every JSON-RPC exchange,
// errors included, is a 200; a request of notifications only is a 204.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)

		return
	}

	if mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type")); err != nil || mt != "application/json" {
		http.Error(w, "content type must be application/json", http.StatusUnsupportedMediaType)
		return
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, s.cfg.MaxBodyBytes))
	if err != nil {
		if _, tooLarge := errors.AsType[*http.MaxBytesError](err); tooLarge {
			s.writeJSON(
				r.Context(),
				w,
				http.StatusRequestEntityTooLarge,
				failure(null, CodeInvalidRequest, "Request too large"),
			)
			return
		}

		s.writeJSON(r.Context(), w, http.StatusBadRequest, failure(null, CodeParseError, "Parse error"))

		return
	}

	out, ok := s.handle(r.Context(), body)
	if !ok {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	s.writeJSON(r.Context(), w, http.StatusOK, out)
}

// handle returns the response body, or false when nothing is answered.
func (s *Server) handle(ctx context.Context, body []byte) (any, bool) {
	trimmed := bytes.TrimLeft(body, " \t\r\n")
	if len(trimmed) == 0 || !json.Valid(trimmed) {
		return failure(null, CodeParseError, "Parse error"), true
	}

	if trimmed[0] != '[' {
		res, ok := s.call(ctx, trimmed)
		return res, ok
	}

	var batch []json.RawMessage
	if err := json.Unmarshal(trimmed, &batch); err != nil || len(batch) == 0 {
		return failure(null, CodeInvalidRequest, "Invalid Request"), true
	}

	if len(batch) > s.cfg.MaxBatch {
		return failure(null, CodeInvalidRequest, fmt.Sprintf("Batch too large (max %d)", s.cfg.MaxBatch)), true
	}

	out := make([]response, 0, len(batch))

	for _, item := range batch {
		if res, ok := s.call(ctx, item); ok {
			out = append(out, res)
		}
	}

	return out, len(out) > 0
}

// call runs one request; false means a notification (no response).
func (s *Server) call(ctx context.Context, raw json.RawMessage) (response, bool) {
	var req request

	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()

	if err := dec.Decode(&req); err != nil {
		return failure(null, CodeInvalidRequest, "Invalid Request"), true
	}

	id, notification := req.ID, req.ID == nil
	if !notification && !validID(id) {
		return failure(null, CodeInvalidRequest, "Invalid Request"), true
	}

	if id == nil {
		id = null
	}

	var name string
	if req.JSONRPC != "2.0" || json.Unmarshal(req.Method, &name) != nil || name == "" ||
		(len(req.Params) > 0 && req.Params[0] != '{' && req.Params[0] != '[') {
		return failure(id, CodeInvalidRequest, "Invalid Request"), true
	}

	result, err := s.invoke(ctx, name, req.Params)
	if notification {
		if err != nil {
			_ = s.toError(ctx, name, err) // logs internal errors; nobody gets the answer
		}

		return response{}, false
	}

	if err != nil {
		return response{JSONRPC: "2.0", Error: s.toError(ctx, name, err), ID: id}, true
	}

	data, err := json.Marshal(result)
	if err != nil {
		return response{JSONRPC: "2.0", Error: s.toError(ctx, name, err), ID: id}, true
	}

	return response{JSONRPC: "2.0", Result: data, ID: id}, true
}

func (s *Server) invoke(ctx context.Context, name string, params json.RawMessage) (result any, err error) {
	m, ok := s.methods[name]
	if !ok {
		return nil, &Error{Code: CodeMethodNotFound, Message: "Method not found"}
	}

	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("panic: %v", p)
		}
	}()

	for _, g := range m.guards {
		if err := g(ctx); err != nil {
			return nil, err
		}
	}

	return m.call(ctx, params)
}

// toError maps err to the error object a client may see; internal errors
// are logged and answered as -32603 with no detail.
func (s *Server) toError(ctx context.Context, name string, err error) *Error {
	if e, ok := errors.AsType[*Error](err); ok {
		return e
	}

	if v, ok := errors.AsType[*errs.ValidationError](err); ok {
		return &Error{Code: CodeInvalidParams, Message: "Invalid params", Data: map[string]any{"fields": v.Fields}}
	}

	if h, ok := errors.AsType[*errs.HTTPError](err); ok && h.Status >= 400 && h.Status < 500 {
		data := map[string]any{"status": h.Status}
		for k, v := range h.Extensions {
			if _, taken := data[k]; !taken {
				data[k] = v
			}
		}

		return &Error{Code: -32000 - (h.Status - 400), Message: h.Text(), Data: data}
	}

	s.cfg.Logger.ErrorContext(ctx, "jsonrpc: method failed", "method", name, "err", err)

	return &Error{Code: CodeInternalError, Message: "Internal error"}
}

// validID reports whether id is a string, a number or null.
func validID(id json.RawMessage) bool {
	switch id[0] {
	case '"', 'n', '-', '0', '1', '2', '3', '4', '5', '6', '7', '8', '9':
		return true
	default:
		return false
	}
}

func failure(id json.RawMessage, code int, msg string) response {
	return response{JSONRPC: "2.0", Error: &Error{Code: code, Message: msg}, ID: id}
}

func (s *Server) writeJSON(ctx context.Context, w http.ResponseWriter, status int, v any) {
	body, err := json.Marshal(v)
	if err != nil {
		// Only an app's *Error.Data can fail to encode (results are
		// marshalled per call).
		s.cfg.Logger.ErrorContext(ctx, "jsonrpc: encode response", "err", err)
		body, status = []byte(
			`{"jsonrpc":"2.0","error":{"code":-32603,"message":"Internal error"},"id":null}`,
		), http.StatusOK
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_, _ = w.Write(append(body, '\n'))
}
