package studio

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
)

// The JSON API is internal to Studio's own interface and experimental (see
// docs/api.md). Every route is registered in routes(), so it sits behind the
// same guards as the pages: loopback check, cross-origin protection, session.

var apiParams = params{filter: "filter", perPage: "perPage"} //nolint:gochecknoglobals // read-only

// apiError is the body of every API failure. The message is short and never
// carries what the database said.
type apiError struct {
	Error apiErrorBody `json:"error"`
}

type apiErrorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type apiSource struct {
	Name  string `json:"name"`
	Kind  string `json:"kind"`
	Label string `json:"label"`
}

type apiBootstrap struct {
	Version        string      `json:"version"`
	Write          bool        `json:"write"`
	RefreshSeconds float64     `json:"refreshSeconds"`
	Sources        []apiSource `json:"sources"`
}

// apiRoutes registers the read routes of the API; apiWriteRoutes adds the
// write ones when Studio runs with --write.
func (s *Studio) apiRoutes() {
	s.mux.HandleFunc("GET /api/v1/bootstrap", s.apiBootstrap)
	s.apiSQLRoutes()

	// Anything else under /api/v1/ is a JSON 404, so the interface never gets
	// an HTML page where it expects JSON.
	s.mux.HandleFunc("/api/v1/", func(w http.ResponseWriter, _ *http.Request) {
		writeAPIError(w, http.StatusNotFound, "not_found", "No such API route.")
	})
}

func (s *Studio) apiBootstrap(w http.ResponseWriter, _ *http.Request) {
	sources := make([]apiSource, 0, len(s.cfg.Sources))
	for _, src := range s.cfg.Sources {
		sources = append(sources, apiSource{Name: src.Name(), Kind: string(src.Kind()), Label: src.Label()})
	}

	writeJSON(w, http.StatusOK, apiBootstrap{
		Version: s.cfg.Version, Write: s.cfg.Write,
		RefreshSeconds: refreshInterval(s.cfg.Refresh).Seconds(), Sources: sources,
	})
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

// apiFail answers a data error: 404 for something that is not there, 422 for
// input the browser sent, 500 otherwise. The cause goes to the Studio log;
// the response only has a generic message, or the reason of a refusal that the
// request itself caused.
func (s *Studio) apiFail(w http.ResponseWriter, r *http.Request, err error) {
	status, code, msg := http.StatusInternalServerError, "internal", "The request failed."

	switch {
	case errors.Is(err, ErrNotFound):
		status, code, msg = http.StatusNotFound, "not_found", "Not found."
	case errors.Is(err, errInput):
		status, code = http.StatusUnprocessableEntity, "invalid_input"
		msg = strings.TrimPrefix(strings.TrimPrefix(err.Error(), errInput.Error()), "\n")
	default:
		if _, ok := errors.AsType[*queryError](err); ok {
			code, msg = "query_failed", "The database could not run the request. The details are in the Studio log."
		}
	}

	s.cfg.Log.WarnContext(r.Context(), "studio api request failed",
		"path", r.URL.Path, "status", status, "cause", errors.Unwrap(err))

	writeAPIError(w, status, code, msg)
}
