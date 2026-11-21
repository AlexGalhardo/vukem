package studio

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"
)

// The JSON API is internal to Studio's own interface and experimental (see
// docs/api.md). Every route is registered in routes(), so it sits behind the
// same guards as the pages: loopback check, cross-origin protection, session.

// maxAPIBody bounds a JSON request body.
const maxAPIBody = 1 << 20

var apiParams = params{filter: "filter", perPage: "perPage"} //nolint:gochecknoglobals // read-only

// apiError is the body of every API failure. The message is short and never
// carries what the database said.
type apiError struct {
	Error apiErrorBody `json:"error"`
}

type apiErrorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	// Current is set on a 409: the values the guarded columns hold now.
	Current map[string]apiCell `json:"current,omitempty"`
}

type apiSource struct {
	Name  string `json:"name"`
	Kind  string `json:"kind"`
	Label string `json:"label"`
}

type apiBootstrap struct {
	Version        string  `json:"version"`
	Write          bool    `json:"write"`
	RefreshSeconds float64 `json:"refreshSeconds"`
	// SessionKey names this Studio run for the interface's browser storage. It is a one-way
	// hash of the session id, so it cannot be traded for access, and it changes at every start.
	SessionKey string      `json:"sessionKey"`
	Sources    []apiSource `json:"sources"`
}

// apiRoutes registers the read routes of the API; apiWriteRoutes adds the
// write ones when Studio runs with --write.
func (s *Studio) apiRoutes() {
	s.mux.HandleFunc("GET /api/v1/bootstrap", s.apiBootstrap)
	s.apiSQLRoutes()
	s.apiMongoRoutes()
	s.apiRedisRoutes()

	if s.cfg.Write {
		s.apiWriteRoutes()
	}

	// Anything else under /api/v1/ is a JSON 404, so the interface never gets
	// an HTML page where it expects JSON.
	s.mux.HandleFunc("/api/v1/", func(w http.ResponseWriter, _ *http.Request) {
		writeAPIError(w, http.StatusNotFound, "not_found", "No such API route.")
	})
}

// requireJSON answers 415 and returns false unless the request says it sends
// JSON, which a plain HTML form cannot do across origins. Routes that change
// state call it first.
func requireJSON(w http.ResponseWriter, r *http.Request) bool {
	if isJSONRequest(r) {
		return true
	}

	writeAPIError(w, http.StatusUnsupportedMediaType, "unsupported_media_type", "Send Content-Type: application/json.")

	return false
}

func isJSONRequest(r *http.Request) bool {
	mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	return err == nil && mt == "application/json"
}

func (s *Studio) apiBootstrap(w http.ResponseWriter, _ *http.Request) {
	sources := make([]apiSource, 0, len(s.cfg.Sources))
	for _, src := range s.cfg.Sources {
		sources = append(sources, apiSource{Name: src.Name(), Kind: string(src.Kind()), Label: src.Label()})
	}

	writeJSON(w, http.StatusOK, apiBootstrap{
		Version: s.cfg.Version, Write: s.cfg.Write,
		RefreshSeconds: refreshInterval(s.cfg.Refresh).Seconds(), SessionKey: s.sessionKey(), Sources: sources,
	})
}

func (s *Studio) sessionKey() string {
	sum := sha256.Sum256([]byte("vukem-studio-session-key:" + s.sessionID))

	return hex.EncodeToString(sum[:8])
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	body, err := json.Marshal(v)
	if err != nil {
		status = http.StatusInternalServerError
		body = []byte(`{"error":{"code":"internal","message":"The request failed."}}`)
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(append(body, '\n'))
}

func writeAPIError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, apiError{Error: apiErrorBody{Code: code, Message: message}})
}

// studioPrefix starts the messages Studio writes itself, as opposed to what a
// driver reports.
const studioPrefix = "studio: "

// apiFail answers a data error: 404 for something that is not there, 422 for
// input the browser sent, 500 otherwise. The cause goes to the Studio log;
// the response only has a generic message, or the reason of a refusal that the
// request itself caused.
func (s *Studio) apiFail(w http.ResponseWriter, r *http.Request, err error) {
	s.apiAnswerFailure(w, r, err, errors.Unwrap(err))
}

// apiWriteFail is apiFail for a write: the log gets the class of the failure,
// because the driver's text can quote the values being written.
func (s *Studio) apiWriteFail(w http.ResponseWriter, r *http.Request, err error) {
	s.apiAnswerFailure(w, r, err, errorClass(err))
}

// statusClientClosed is the status nginx made common for a request the client gave up on.
const statusClientClosed = 499

func (s *Studio) apiAnswerFailure(w http.ResponseWriter, r *http.Request, err error, cause any) {
	status, code, msg := http.StatusInternalServerError, "internal", "The request failed."

	_, isQuery := errors.AsType[*queryError](err)

	var current map[string]apiCell

	if c, ok := errors.AsType[*ConflictError](err); ok {
		current = make(map[string]apiCell, len(c.Current))
		for name, cell := range c.Current {
			current[name] = apiCell{T: cell.Text, N: cell.Null, X: cell.Truncated, B: cell.Binary, L: cell.Literal}
		}
	}

	switch {
	case current != nil:
		status, code, msg = http.StatusConflict, "conflict", "The row changed since it was read."
	case errors.Is(err, ErrNotFound):
		status, code, msg = http.StatusNotFound, "not_found", "Not found."
	case errors.Is(err, errInput):
		status, code = http.StatusUnprocessableEntity, "invalid_input"
		msg = strings.TrimPrefix(strings.TrimPrefix(err.Error(), errInput.Error()), "\n")
	case isQuery && errors.Is(err, context.DeadlineExceeded):
		status, code, msg = http.StatusGatewayTimeout, "timeout", "The statement ran too long and was stopped."
	case isQuery && errors.Is(err, context.Canceled):
		status, code, msg = statusClientClosed, "canceled", "The statement was cancelled."
	case isQuery:
		// Before the prefix check: queryError's own text starts with "studio: ".
		code, msg = "query_failed", "The database could not run the request. The details are in the Studio log."
	case strings.HasPrefix(err.Error(), studioPrefix):
		// Studio's own refusals (a filter, a pipeline, a command) say why.
		status, code = http.StatusUnprocessableEntity, "invalid_input"
		msg = strings.TrimPrefix(err.Error(), studioPrefix)
	}

	s.cfg.Log.WarnContext(r.Context(), "studio api request failed",
		"path", r.URL.Path, "status", status, "cause", cause)

	writeJSON(w, status, apiError{Error: apiErrorBody{Code: code, Message: msg, Current: current}})
}

// decodeBody reads a JSON body into v: at most 1 MiB, no unknown field, one
// value and nothing after it.
func decodeBody(w http.ResponseWriter, r *http.Request, v any) error {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxAPIBody))
	dec.DisallowUnknownFields()

	if err := dec.Decode(v); err != nil {
		if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
			return errBodyTooLarge
		}

		return inputError("the request body is not valid JSON for this route")
	}

	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return inputError("the request body must hold one JSON value")
	}

	return nil
}

var errBodyTooLarge = errors.New("request body too large")

// apiBodyFail answers a body that could not be read: 413 when it was too
// large, 422 otherwise.
func (s *Studio) apiBodyFail(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, errBodyTooLarge) {
		writeAPIError(w, http.StatusRequestEntityTooLarge, "body_too_large", "The request body is over 1 MiB.")
		return
	}

	s.apiFail(w, r, err)
}
