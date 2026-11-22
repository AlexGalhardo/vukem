package studio

import (
	"context"
	"errors"
	"net/http"
)

type apiDoc struct {
	ID   string `json:"id"`
	JSON string `json:"json"`
}

func apiDocPage(pg DocPage) map[string]any {
	docs := make([]apiDoc, len(pg.Docs))
	for i, d := range pg.Docs {
		docs[i] = apiDoc(d)
	}

	out := map[string]any{"docs": docs, "hasMore": pg.HasMore}
	if pg.Next != "" {
		out["next"] = pg.Next
	}

	return out
}

func (s *Studio) apiMongoRoutes() {
	s.mux.HandleFunc("GET /api/v1/sources/{source}/collections", s.apiCollections)
	s.mux.HandleFunc("GET /api/v1/sources/{source}/collections/{collection}/documents", s.apiDocuments)
	s.mux.HandleFunc("GET /api/v1/sources/{source}/collections/{collection}/count", s.apiDocCount)
	s.mux.HandleFunc("GET /api/v1/sources/{source}/collections/{collection}/documents/{id}", s.apiDocument)
	s.mux.HandleFunc("GET /api/v1/sources/{source}/collections/{collection}/export", s.exportDocuments)
	s.mux.HandleFunc("POST /api/v1/sources/{source}/find", s.apiFind)
	s.mux.HandleFunc("POST /api/v1/sources/{source}/aggregate", s.apiAggregate)
}

// apiMongo returns the MongoDB source of the path, answering 404 itself when
// the source does not exist or is not MongoDB.
func (s *Studio) apiMongo(w http.ResponseWriter, r *http.Request) MongoSource {
	m := s.mongo(r)
	if m == nil {
		writeAPIError(w, http.StatusNotFound, "not_found", "Not found.")
	}

	return m
}

func (s *Studio) apiCollections(w http.ResponseWriter, r *http.Request) {
	m := s.apiMongo(w, r)
	if m == nil {
		return
	}

	cols, err := m.Collections(r.Context())
	if err != nil {
		s.apiFail(w, r, err)
		return
	}

	type item struct {
		Name     string `json:"name"`
		Estimate int64  `json:"estimate"`
	}

	out := make([]item, len(cols))
	for i, c := range cols {
		out[i] = item(c)
	}

	writeJSON(w, http.StatusOK, map[string]any{"collections": out})
}

func (s *Studio) apiDocuments(w http.ResponseWriter, r *http.Request) {
	m := s.apiMongo(w, r)
	if m == nil {
		return
	}

	q, err := docQueryFrom(r.PathValue("collection"), r.URL.Query(), apiParams)
	if err != nil {
		s.apiFail(w, r, err)
		return
	}

	pg, err := m.Documents(r.Context(), q)
	if err != nil {
		s.apiFail(w, r, err)
		return
	}

	writeJSON(w, http.StatusOK, apiDocPage(pg))
}

// apiDocCount answers the exact number of documents matching the filters, or
// timedOut when the bounded count did not finish.
func (s *Studio) apiDocCount(w http.ResponseWriter, r *http.Request) {
	m := s.apiMongo(w, r)
	if m == nil {
		return
	}

	filters, err := filtersFrom(r.URL.Query(), apiParams.filter)
	if err != nil {
		s.apiFail(w, r, err)
		return
	}

	n, err := m.Count(r.Context(), r.PathValue("collection"), filters)
	if errors.Is(err, ErrCountTimeout) {
		writeJSON(w, http.StatusOK, map[string]any{"timedOut": true})
		return
	}

	if err != nil {
		s.apiFail(w, r, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"count": n})
}

func (s *Studio) apiDocument(w http.ResponseWriter, r *http.Request) {
	m := s.apiMongo(w, r)
	if m == nil {
		return
	}

	d, err := m.Document(r.Context(), r.PathValue("collection"), r.PathValue("id"))
	if err != nil {
		s.apiFail(w, r, err)
		return
	}

	writeJSON(w, http.StatusOK, apiDoc(d))
}

type findBody struct {
	Collection string `json:"collection"`
	Filter     string `json:"filter"`
}

type aggregateBody struct {
	Collection string `json:"collection"`
	Pipeline   string `json:"pipeline"`
}

// apiFind runs a find with an Extended JSON filter typed in the console.
func (s *Studio) apiFind(w http.ResponseWriter, r *http.Request) {
	if !requireJSON(w, r) {
		return
	}

	m := s.apiMongo(w, r)
	if m == nil {
		return
	}

	var body findBody
	if err := decodeBody(w, r, &body); err != nil {
		s.apiBodyFail(w, r, err)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), mongoTimeout)
	defer cancel()

	pg, err := m.Find(ctx, body.Collection, body.Filter)
	if err != nil {
		// The class only: the decoder and the driver can quote the typed values.
		s.apiWriteFail(w, r, err)
		return
	}

	writeJSON(w, http.StatusOK, apiDocPage(pg))
}

// apiAggregate runs an aggregation pipeline; a stage that writes needs --write
// and is audited.
func (s *Studio) apiAggregate(w http.ResponseWriter, r *http.Request) {
	if !requireJSON(w, r) {
		return
	}

	m := s.apiMongo(w, r)
	if m == nil {
		return
	}

	var body aggregateBody
	if err := decodeBody(w, r, &body); err != nil {
		s.apiBodyFail(w, r, err)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), mongoTimeout)
	defer cancel()

	pg, err := m.Aggregate(ctx, body.Collection, body.Pipeline)
	s.auditConsole(ctx, m, body.Collection, err)

	if err != nil {
		s.apiWriteFail(w, r, err)
		return
	}

	writeJSON(w, http.StatusOK, apiDocPage(pg))
}
