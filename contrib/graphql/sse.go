package graphql

import (
	"mime"
	"net/http"
	"strings"

	"github.com/AlexGalhardo/vukem/sse"
)

const mediaEventStream = "text/event-stream"

func acceptsEventStream(accept string) bool {
	for part := range strings.SplitSeq(accept, ",") {
		mt, params, err := mime.ParseMediaType(strings.TrimSpace(part))
		if err == nil && mt == mediaEventStream && params["q"] != "0" {
			return true
		}
	}

	return false
}

// serveSSE serves the graphql-sse protocol in "distinct connections" mode:
// a POST that accepts text/event-stream gets one "next" event per result
// and a final "complete" event. Request errors (body, persisted queries,
// parse, validation, variables, limits) are a JSON response with 400
// before the stream opens.
func (h *handler) serveSSE(w http.ResponseWriter, r *http.Request) {
	req, status, msg := h.readPost(w, r)
	if status != 0 {
		writeResult(w, mediaJSON, status, &Response{Errors: []*Error{requestError(CodeBadRequest, msg)}})
		return
	}

	if gerr := h.cfg.Persisted.resolve(r.Context(), &req); gerr != nil {
		writeResult(w, mediaJSON, http.StatusBadRequest, &Response{Errors: []*Error{gerr}})
		return
	}

	if req.Query == "" {
		writeResult(w, mediaJSON, http.StatusBadRequest,
			&Response{Errors: []*Error{requestError(CodeBadRequest, "Must provide query string.")}})

		return
	}

	p, errs := h.s.prepare(req, !h.cfg.DisableIntrospection)
	if errs != nil {
		writeResult(w, mediaJSON, http.StatusBadRequest, &Response{Errors: errs})
		return
	}

	_ = sse.Serve(w, r, func(st *sse.Stream) error {
		stream, resp := h.s.subscribe(st.Context(), p)
		if resp != nil {
			stream = func(yield func(*Response) bool) { yield(resp) }
		}

		// A failed send means the client left; the source stream stops
		// with the stream's context.
		for res := range stream {
			if st.SendJSON("next", res) != nil {
				return nil
			}
		}

		_ = st.Send("complete", "")

		return nil
	})
}
