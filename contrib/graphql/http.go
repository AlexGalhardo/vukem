package graphql

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"
)

// DefaultMaxBodyBytes bounds a POST body (1 MiB).
const DefaultMaxBodyBytes = 1 << 20

// Media types of GraphQL over HTTP.
const (
	mediaJSON          = "application/json"
	mediaGraphQLResult = "application/graphql-response+json"
)

// HandlerConfig tunes Handler.
type HandlerConfig struct {
	// MaxBodyBytes bounds a POST body (default 1 MiB).
	MaxBodyBytes int64
	// Persisted enables persisted queries (nil: off; a request carrying a
	// persistedQuery extension gets PERSISTED_QUERY_NOT_SUPPORTED).
	Persisted *PersistedQueries
	// DisableIntrospection rejects __schema and __type (__typename stays).
	DisableIntrospection bool
	// DisableGET accepts POST only.
	DisableGET bool
}

// Handler serves GraphQL over HTTP: POST with an application/json body,
// and GET with query parameters for query operations only (mutations
// over GET get 405, so a link or image tag cannot change state).
//
// Responses are application/graphql-response+json when the client
// accepts it (request errors then use 400), otherwise application/json
// with 200, as the GraphQL over HTTP specification describes.
func (s *Schema) Handler(cfg HandlerConfig) http.Handler {
	if cfg.MaxBodyBytes <= 0 {
		cfg.MaxBodyBytes = DefaultMaxBodyBytes
	}

	if cfg.Persisted != nil && cfg.Persisted.Store == nil {
		p := *cfg.Persisted
		if p.AllowlistOnly {
			p.Store = NewAllowlist() // fail closed: an empty allowlist
		} else {
			p.Store = NewMemoryStore(0)
		}

		cfg.Persisted = &p
	}

	return &handler{s: s, cfg: cfg}
}

type handler struct {
	s   *Schema
	cfg HandlerConfig
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	media, ok := negotiate(r.Header.Get("Accept"))
	if !ok {
		http.Error(
			w,
			"Not Acceptable: use application/graphql-response+json or application/json",
			http.StatusNotAcceptable,
		)
		return
	}

	var (
		req    Request
		status int
		msg    string
	)

	switch {
	case r.Method == http.MethodPost:
		req, status, msg = h.readPost(w, r)
	case r.Method == http.MethodGet && !h.cfg.DisableGET:
		req, status, msg = readGet(r)
	default:
		allow := "GET, POST"
		if h.cfg.DisableGET {
			allow = "POST"
		}

		w.Header().Set("Allow", allow)
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)

		return
	}

	if status != 0 {
		writeResult(w, media, status, &Response{Errors: []*Error{requestError(CodeBadRequest, msg)}})
		return
	}

	if gerr := h.cfg.Persisted.resolve(r.Context(), &req); gerr != nil {
		writeResult(w, media, requestStatus(media), &Response{Errors: []*Error{gerr}})
		return
	}

	if req.Query == "" {
		writeResult(
			w,
			media,
			http.StatusBadRequest,
			&Response{Errors: []*Error{requestError(CodeBadRequest, "Must provide query string.")}},
		)
		return
	}

	p, errs := h.s.prepare(req, !h.cfg.DisableIntrospection)
	if errs != nil {
		writeResult(w, media, requestStatus(media), &Response{Errors: errs})
		return
	}

	if r.Method == http.MethodGet && p.op.kind != opQuery {
		w.Header().Set("Allow", "POST")
		writeResult(w, media, http.StatusMethodNotAllowed, &Response{Errors: []*Error{
			requestError(CodeBadRequest, "Can only perform a "+string(p.op.kind)+" operation from a POST request."),
		}})

		return
	}

	writeResult(w, media, http.StatusOK, h.s.run(r.Context(), p))
}

// requestStatus is the status of a request error (parse, validation,
// variables): 400 for the GraphQL response media type, 200 for legacy
// application/json clients.
func requestStatus(media string) int {
	if media == mediaGraphQLResult {
		return http.StatusBadRequest
	}

	return http.StatusOK
}

// negotiate picks the response media type from Accept.
func negotiate(accept string) (string, bool) {
	if strings.TrimSpace(accept) == "" {
		return mediaJSON, true
	}

	best := ""

	for part := range strings.SplitSeq(accept, ",") {
		mt, params, err := mime.ParseMediaType(strings.TrimSpace(part))
		if err != nil || params["q"] == "0" {
			continue
		}

		switch mt {
		case mediaGraphQLResult:
			return mediaGraphQLResult, true
		case mediaJSON, "application/*", "*/*":
			best = mediaJSON
		}
	}

	return best, best != ""
}

func (h *handler) readPost(w http.ResponseWriter, r *http.Request) (Request, int, string) {
	var req Request

	mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mt != mediaJSON {
		return req, http.StatusUnsupportedMediaType, "Content-Type must be application/json."
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, h.cfg.MaxBodyBytes))
	if err != nil {
		if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
			return req, http.StatusRequestEntityTooLarge, "Request body too large."
		}

		return req, http.StatusBadRequest, "Could not read the request body."
	}

	if trimmed := bytes.TrimSpace(body); len(trimmed) > 0 && trimmed[0] == '[' {
		return req, http.StatusBadRequest, "Batched requests are not supported."
	}

	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()

	if err := dec.Decode(&req); err != nil {
		return req, http.StatusBadRequest, "Body must be a JSON object with a \"query\" string."
	}

	return req, 0, ""
}

func readGet(r *http.Request) (Request, int, string) {
	q := r.URL.Query()
	req := Request{Query: q.Get("query"), OperationName: q.Get("operationName")}

	for name, dst := range map[string]*map[string]any{"variables": &req.Variables, "extensions": &req.Extensions} {
		raw := q.Get(name)
		if raw == "" {
			continue
		}

		dec := json.NewDecoder(strings.NewReader(raw))
		dec.UseNumber()

		if err := dec.Decode(dst); err != nil {
			return req, http.StatusBadRequest, "The " + name + " parameter must be a JSON object."
		}
	}

	return req, 0, ""
}

func writeResult(w http.ResponseWriter, media string, status int, resp *Response) {
	body, err := json.Marshal(resp)
	if err != nil {
		status = http.StatusInternalServerError
		body = []byte(`{"errors":[{"message":"Internal server error","extensions":{"code":"INTERNAL_SERVER_ERROR"}}]}`)
	}

	h := w.Header()
	h.Set("Content-Type", media+"; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}
