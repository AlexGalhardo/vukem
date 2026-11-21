package studio

import (
	"net/http"
	"time"
)

// The write routes exist only when Studio runs with --write: without it they
// are not registered, and the catch-all of apiRoutes answers 404. Every one
// is audited without values, and a delete needs the typed confirmation, as the
// HTML forms do.

func (s *Studio) apiWriteRoutes() {
	s.mux.HandleFunc("POST /api/v1/sources/{source}/tables/{table}/rows", s.apiInsertRow)
	s.mux.HandleFunc("PATCH /api/v1/sources/{source}/tables/{table}/rows/{key}", s.apiUpdateRow)
	s.mux.HandleFunc("DELETE /api/v1/sources/{source}/tables/{table}/rows/{key}", s.apiDeleteRow)
	s.mux.HandleFunc("POST /api/v1/sources/{source}/collections/{collection}/documents", s.apiInsertDocument)
	s.mux.HandleFunc("PUT /api/v1/sources/{source}/collections/{collection}/documents/{id}", s.apiReplaceDocument)
	s.mux.HandleFunc("DELETE /api/v1/sources/{source}/collections/{collection}/documents/{id}", s.apiDeleteDocument)
	s.mux.HandleFunc("PATCH /api/v1/sources/{source}/keys/{key...}", s.apiChangeKey)
	s.mux.HandleFunc("DELETE /api/v1/sources/{source}/keys/{key...}", s.apiDeleteKey)
}

type apiField struct {
	Text string `json:"text"`
	Null bool   `json:"null"`
}

type rowBody struct {
	Fields map[string]apiField `json:"fields"`
}

// updateBody is rowBody plus the values the row must still hold.
type updateBody struct {
	Fields map[string]apiField `json:"fields"`
	Expect map[string]apiField `json:"expect"`
}

type confirmBody struct {
	Confirm string `json:"confirm"`
}

type documentBody struct {
	Doc string `json:"doc"`
}

type keyBody struct {
	Action  string `json:"action"`
	Value   string `json:"value"`
	Field   string `json:"field"`
	Seconds *int64 `json:"seconds"`
}

// apiFields turns the body's fields into the values a write takes. A column
// that is not in the table is refused; on insert an empty text that is not
// NULL means "the default" and is dropped, as in the form.
func apiFields(t Table, in map[string]apiField, insert bool) (map[string]Field, error) {
	known := columnsOf(t)
	out := make(map[string]Field, len(in))

	for name, f := range in {
		if _, ok := known[name]; !ok {
			return nil, inputError("unknown column")
		}

		if f.Text == "" && !f.Null && insert {
			continue
		}

		out[name] = Field(f)
	}

	return out, nil
}

// apiWritable returns the table of a write, or fails when it has no primary key.
func (s *Studio) apiWritable(w http.ResponseWriter, r *http.Request, src SQLSource, action string) (Table, bool) {
	t, err := src.Table(r.Context(), r.PathValue("table"))
	if err == nil && len(t.PrimaryKey) == 0 {
		err = ErrNotFound
		s.audit(r.Context(), src, action, t.Name, err)
	}

	if err != nil {
		s.apiWriteFail(w, r, err)
		return Table{}, false
	}

	return t, true
}

func (s *Studio) apiInsertRow(w http.ResponseWriter, r *http.Request) {
	if !requireJSON(w, r) {
		return
	}

	src := s.apiSQL(w, r)
	if src == nil {
		return
	}

	var body rowBody
	if err := decodeBody(w, r, &body); err != nil {
		s.apiBodyFail(w, r, err)
		return
	}

	t, ok := s.apiWritable(w, r, src, "insert")
	if !ok {
		return
	}

	fields, err := apiFields(t, body.Fields, true)
	if err == nil {
		err = src.Insert(r.Context(), t.Name, fields)
		s.audit(r.Context(), src, "insert", t.Name, err, "columns", fieldNames(fields))
	}

	if err != nil {
		s.apiWriteFail(w, r, err)
		return
	}

	writeJSON(w, http.StatusCreated, map[string]any{"ok": true})
}

func (s *Studio) apiUpdateRow(w http.ResponseWriter, r *http.Request) {
	if !requireJSON(w, r) {
		return
	}

	src := s.apiSQL(w, r)
	if src == nil {
		return
	}

	var body updateBody
	if err := decodeBody(w, r, &body); err != nil {
		s.apiBodyFail(w, r, err)
		return
	}

	t, ok := s.apiWritable(w, r, src, "update")
	if !ok {
		return
	}

	key := pathKey(r)

	fields, err := apiFields(t, body.Fields, false)
	if err == nil {
		expect := make(map[string]Field, len(body.Expect))
		for name, f := range body.Expect {
			expect[name] = Field(f)
		}

		err = src.Update(r.Context(), t.Name, key, fields, expect)
		s.audit(r.Context(), src, "update", t.Name, err, "key", confirmKey(key), "columns", fieldNames(fields),
			"guarded", len(expect))
	}

	if err != nil {
		s.apiWriteFail(w, r, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Studio) apiDeleteRow(w http.ResponseWriter, r *http.Request) {
	if !requireJSON(w, r) {
		return
	}

	src := s.apiSQL(w, r)
	if src == nil {
		return
	}

	var body confirmBody
	if err := decodeBody(w, r, &body); err != nil {
		s.apiBodyFail(w, r, err)
		return
	}

	t, ok := s.apiWritable(w, r, src, "delete")
	if !ok {
		return
	}

	key := pathKey(r)

	if body.Confirm == "" || body.Confirm != confirmKey(key) {
		s.audit(r.Context(), src, "delete", t.Name, inputError("confirmation did not match"), "key", confirmKey(key))
		s.apiFail(w, r, inputError("type the primary key to confirm the deletion"))

		return
	}

	err := src.Delete(r.Context(), t.Name, key)
	s.audit(r.Context(), src, "delete", t.Name, err, "key", confirmKey(key))

	if err != nil {
		s.apiWriteFail(w, r, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Studio) apiInsertDocument(w http.ResponseWriter, r *http.Request) {
	if !requireJSON(w, r) {
		return
	}

	m := s.apiMongo(w, r)
	if m == nil {
		return
	}

	var body documentBody
	if err := decodeBody(w, r, &body); err != nil {
		s.apiBodyFail(w, r, err)
		return
	}

	coll := r.PathValue("collection")
	err := m.Insert(r.Context(), coll, body.Doc)
	s.audit(r.Context(), m, "insert", coll, err)

	if err != nil {
		s.apiWriteFail(w, r, err)
		return
	}

	writeJSON(w, http.StatusCreated, map[string]any{"ok": true})
}

func (s *Studio) apiReplaceDocument(w http.ResponseWriter, r *http.Request) {
	if !requireJSON(w, r) {
		return
	}

	m := s.apiMongo(w, r)
	if m == nil {
		return
	}

	var body documentBody
	if err := decodeBody(w, r, &body); err != nil {
		s.apiBodyFail(w, r, err)
		return
	}

	coll, id := r.PathValue("collection"), r.PathValue("id")
	err := m.Replace(r.Context(), coll, id, body.Doc)
	s.audit(r.Context(), m, "replace", coll, err, "id", id)

	if err != nil {
		s.apiWriteFail(w, r, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Studio) apiDeleteDocument(w http.ResponseWriter, r *http.Request) {
	if !requireJSON(w, r) {
		return
	}

	m := s.apiMongo(w, r)
	if m == nil {
		return
	}

	var body confirmBody
	if err := decodeBody(w, r, &body); err != nil {
		s.apiBodyFail(w, r, err)
		return
	}

	coll, id := r.PathValue("collection"), r.PathValue("id")

	if body.Confirm == "" || body.Confirm != confirmText(id) {
		s.audit(r.Context(), m, "delete", coll, inputError("confirmation did not match"), "id", id)
		s.apiFail(w, r, inputError("type the _id of the document to confirm the deletion"))

		return
	}

	err := m.Delete(r.Context(), coll, id)
	s.audit(r.Context(), m, "delete", coll, err, "id", id)

	if err != nil {
		s.apiWriteFail(w, r, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// apiChangeKey edits one Redis key: a string (keeping its TTL), a hash field
// or the TTL.
func (s *Studio) apiChangeKey(w http.ResponseWriter, r *http.Request) {
	if !requireJSON(w, r) {
		return
	}

	src := s.apiRedis(w, r)
	if src == nil {
		return
	}

	var body keyBody
	if err := decodeBody(w, r, &body); err != nil {
		s.apiBodyFail(w, r, err)
		return
	}

	key := r.PathValue("key")

	var err error

	switch body.Action {
	case "set":
		err = src.SetString(r.Context(), key, body.Value)
	case "hset":
		err = src.SetField(r.Context(), key, body.Field, body.Value)
	case "ttl":
		if body.Seconds == nil {
			s.apiFail(w, r, inputError("seconds must be a whole number"))
			return
		}

		err = src.SetTTL(r.Context(), key, time.Duration(*body.Seconds)*time.Second)
	default:
		s.apiFail(w, r, inputError("unknown action"))
		return
	}

	s.audit(r.Context(), src, body.Action, key, err)

	if err != nil {
		s.apiWriteFail(w, r, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Studio) apiDeleteKey(w http.ResponseWriter, r *http.Request) {
	if !requireJSON(w, r) {
		return
	}

	src := s.apiRedis(w, r)
	if src == nil {
		return
	}

	var body confirmBody
	if err := decodeBody(w, r, &body); err != nil {
		s.apiBodyFail(w, r, err)
		return
	}

	key := r.PathValue("key")

	if body.Confirm == "" || body.Confirm != key {
		s.audit(r.Context(), src, "del", key, inputError("confirmation did not match"))
		s.apiFail(w, r, inputError("type the key name to confirm the deletion"))

		return
	}

	err := src.Delete(r.Context(), key)
	s.audit(r.Context(), src, "del", key, err)

	if err != nil {
		s.apiWriteFail(w, r, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
