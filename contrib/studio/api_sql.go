package studio

import (
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
	s.mux.HandleFunc("GET /api/v1/sources/{source}/tables/{table}/rows/{key}", s.apiRow)
	s.mux.HandleFunc("GET /api/v1/sources/{source}/tables/{table}/references", s.apiReferences)
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
