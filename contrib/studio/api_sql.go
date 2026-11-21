package studio

import (
	"errors"
	"net/http"
)

type apiColumn struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Nullable bool   `json:"nullable"`
	Default  string `json:"default,omitempty"`
}

type apiForeignKey struct {
	Columns    []string `json:"columns"`
	RefTable   string   `json:"refTable"`
	RefColumns []string `json:"refColumns"`
}

// apiCell is one value: t is the text (cut for a grid), n is NULL, x is cut,
// b is binary and l says the text is a JSON number or boolean.
type apiCell struct {
	T string `json:"t"`
	N bool   `json:"n,omitempty"`
	X bool   `json:"x,omitempty"`
	B bool   `json:"b,omitempty"`
	L bool   `json:"l,omitempty"`
}

func apiCells(row []Cell) []apiCell {
	out := make([]apiCell, len(row))
	for i, c := range row {
		out[i] = apiCell{T: c.Text, N: c.Null, X: c.Truncated, B: c.Binary, L: c.Literal}
	}

	return out
}

func apiColumns(cols []Column) []apiColumn {
	out := make([]apiColumn, len(cols))
	for i, c := range cols {
		out[i] = apiColumn(c)
	}

	return out
}

func apiForeignKeys(fks []ForeignKey) []apiForeignKey {
	out := make([]apiForeignKey, len(fks))
	for i, fk := range fks {
		out[i] = apiForeignKey(fk)
	}

	return out
}

func orEmpty(s []string) []string {
	if s == nil {
		return []string{}
	}

	return s
}

func (s *Studio) apiSQLRoutes() {
	s.mux.HandleFunc("GET /api/v1/sources/{source}/tables", s.apiTables)
	s.mux.HandleFunc("GET /api/v1/sources/{source}/tables/{table}", s.apiTable)
	s.mux.HandleFunc("GET /api/v1/sources/{source}/tables/{table}/rows", s.apiRows)
	s.mux.HandleFunc("GET /api/v1/sources/{source}/tables/{table}/count", s.apiCount)
	s.mux.HandleFunc("GET /api/v1/sources/{source}/tables/{table}/rows/{key}", s.apiRow)
	s.mux.HandleFunc("GET /api/v1/sources/{source}/tables/{table}/references", s.apiReferences)
	s.mux.HandleFunc("POST /api/v1/sources/{source}/query", s.apiQuery)
	s.mux.HandleFunc("POST /api/v1/sources/{source}/explain", s.apiExplain)
}

// apiSQL returns the SQL source of the path, answering 404 itself when the
// source does not exist or is not relational.
func (s *Studio) apiSQL(w http.ResponseWriter, r *http.Request) SQLSource {
	src := s.sql(r)
	if src == nil {
		writeAPIError(w, http.StatusNotFound, "not_found", "Not found.")
	}

	return src
}

func (s *Studio) apiTables(w http.ResponseWriter, r *http.Request) {
	src := s.apiSQL(w, r)
	if src == nil {
		return
	}

	ts, err := src.Tables(r.Context())
	if err != nil {
		s.apiFail(w, r, err)
		return
	}

	type item struct {
		Name        string   `json:"name"`
		RowEstimate int64    `json:"rowEstimate"`
		PrimaryKey  []string `json:"primaryKey"`
	}

	out := make([]item, len(ts))
	for i, t := range ts {
		out[i] = item{Name: t.Name, RowEstimate: t.RowEstimate, PrimaryKey: orEmpty(t.PrimaryKey)}
	}

	writeJSON(w, http.StatusOK, map[string]any{"tables": out})
}

func (s *Studio) apiTable(w http.ResponseWriter, r *http.Request) {
	src := s.apiSQL(w, r)
	if src == nil {
		return
	}

	t, err := src.Table(r.Context(), r.PathValue("table"))
	if err != nil {
		s.apiFail(w, r, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"name": t.Name, "columns": apiColumns(t.Columns), "primaryKey": orEmpty(t.PrimaryKey),
		"foreignKeys": apiForeignKeys(t.ForeignKeys),
	})
}

func (s *Studio) apiRows(w http.ResponseWriter, r *http.Request) {
	src := s.apiSQL(w, r)
	if src == nil {
		return
	}

	q, err := rowQueryFrom(r.PathValue("table"), r.URL.Query(), apiParams)
	if err != nil {
		s.apiFail(w, r, err)
		return
	}

	// The after cursor travels as one packed value, as in the page links.
	if after := r.URL.Query().Get("after"); after != "" {
		q.After = decodeCursor(after)
	} else {
		q.After = nil
	}

	pg, err := src.Rows(r.Context(), q)
	if err != nil {
		s.apiFail(w, r, err)
		return
	}

	rows := make([][]apiCell, len(pg.Rows))
	for i, row := range pg.Rows {
		rows[i] = apiCells(row)
	}

	body := map[string]any{"columns": apiColumns(pg.Columns), "rows": rows, "hasMore": pg.HasMore}
	if pg.Next != nil {
		body["next"] = encodeCursor(pg.Next)
	}

	writeJSON(w, http.StatusOK, body)
}

// apiCount answers the exact number of rows matching the filters, or
// timedOut when the bounded COUNT(*) did not finish.
func (s *Studio) apiCount(w http.ResponseWriter, r *http.Request) {
	src := s.apiSQL(w, r)
	if src == nil {
		return
	}

	filters, err := filtersFrom(r.URL.Query(), apiParams.filter)
	if err != nil {
		s.apiFail(w, r, err)
		return
	}

	n, err := src.Count(r.Context(), r.PathValue("table"), filters)
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

func (s *Studio) apiRow(w http.ResponseWriter, r *http.Request) {
	src := s.apiSQL(w, r)
	if src == nil {
		return
	}

	d, err := src.Row(r.Context(), r.PathValue("table"), pathKey(r))
	if err != nil {
		s.apiFail(w, r, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"columns": apiColumns(d.Columns), "cells": apiCells(d.Cells)})
}

func (s *Studio) apiReferences(w http.ResponseWriter, r *http.Request) {
	src := s.apiSQL(w, r)
	if src == nil {
		return
	}

	t, err := src.Table(r.Context(), r.PathValue("table"))
	if err != nil {
		s.apiFail(w, r, err)
		return
	}

	refs, err := src.References(r.Context(), t.Name)
	if err != nil {
		s.apiFail(w, r, err)
		return
	}

	type item struct {
		Table      string   `json:"table"`
		Columns    []string `json:"columns"`
		RefColumns []string `json:"refColumns"`
	}

	out := make([]item, len(refs))
	for i, ref := range refs {
		out[i] = item{Table: ref.Table, Columns: ref.Columns, RefColumns: ref.RefColumns}
	}

	writeJSON(w, http.StatusOK, map[string]any{"references": out})
}

// pathKey is the {key} path segment of a row: the key parts packed as the
// keyset cursor is (see encodeCursor).
func pathKey(r *http.Request) []string { return decodeCursor(r.PathValue("key")) }

// maxSQLBytes bounds one console statement; the body cap alone would let 1 MiB through.
const maxSQLBytes = 64 << 10

type queryBody struct {
	SQL   string `json:"sql"`
	Write bool   `json:"write"`
}

type explainBody struct {
	SQL string `json:"sql"`
}

// apiQuery runs one console statement: read-only unless Studio runs with
// --write and the body asks for write mode.
func (s *Studio) apiQuery(w http.ResponseWriter, r *http.Request) {
	if !requireJSON(w, r) {
		return
	}

	src := s.apiSQL(w, r)
	if src == nil {
		return
	}

	var body queryBody
	if err := decodeBody(w, r, &body); err != nil {
		s.apiBodyFail(w, r, err)
		return
	}

	if body.Write && !s.cfg.Write {
		writeAPIError(w, http.StatusForbidden, "write_disabled", "Studio runs read-only: start it with --write.")
		return
	}

	s.apiRunSQL(w, r, src, body.SQL, body.SQL, body.Write)
}

// apiExplain returns the plan of a statement, always read-only.
func (s *Studio) apiExplain(w http.ResponseWriter, r *http.Request) {
	if !requireJSON(w, r) {
		return
	}

	src := s.apiSQL(w, r)
	if src == nil {
		return
	}

	var body explainBody
	if err := decodeBody(w, r, &body); err != nil {
		s.apiBodyFail(w, r, err)
		return
	}

	run := body.SQL
	if len(body.SQL) <= maxSQLBytes {
		run = src.Explain(body.SQL)
	}

	s.apiRunSQL(w, r, src, body.SQL, run, false)
}

// apiRunSQL checks the size of the statement text the person typed, then runs
// run, which is that text or the statement that explains it.
func (s *Studio) apiRunSQL(w http.ResponseWriter, r *http.Request, src SQLSource, text, run string, write bool) {
	if len(text) > maxSQLBytes {
		s.apiFail(w, r, inputError("the statement is over 64 KiB"))
		return
	}

	rs, err := src.Query(r.Context(), run, write)
	if write {
		// The statement is not logged: it can carry values.
		s.audit(r.Context(), src, "console", "sql", err, "bytes", len(text))
	}

	if err != nil {
		s.apiFail(w, r, err)
		return
	}

	rows := make([][]apiCell, len(rs.Rows))
	for i, row := range rs.Rows {
		rows[i] = apiCells(row)
	}

	out := map[string]any{
		"columns": orEmpty(rs.Columns), "rows": rows, "truncated": rs.Truncate,
		"elapsedMs": rs.Elapsed.Milliseconds(),
	}
	if rs.HasAffected {
		out["affected"] = rs.Affected
	}

	writeJSON(w, http.StatusOK, out)
}
