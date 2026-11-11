package studio

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// maxFormBytes bounds a form body: a document or a value edited in the browser.
const maxFormBytes = 1 << 20

// routes registers every route; nothing is registered outside this method, so
// every route is behind Handler's guards.
func (s *Studio) routes() {
	s.mux.HandleFunc("GET /{$}", s.overview)
	s.mux.Handle("GET /assets/{file...}", s.assets())
	s.view("GET /s/{source}", s.sourcePage)
	s.sqlRoutes()
	s.view("GET /s/{source}/c/{collection}", s.documentsPage)
	s.mux.HandleFunc("GET /s/{source}/c/{collection}/d/{id}", s.documentPage)
	s.mux.HandleFunc("GET /s/{source}/c/{collection}/export", s.exportDocuments)
	s.mux.HandleFunc("GET /s/{source}/mongo-console", s.mongoConsole)
	s.mux.HandleFunc("POST /s/{source}/mongo-console", s.mongoConsole)
	s.view("GET /s/{source}/k/{key...}", s.keyPage)
	s.mux.HandleFunc("GET /s/{source}/redis-console", s.redisConsole)
	s.mux.HandleFunc("POST /s/{source}/redis-console", s.redisConsole)
	s.mux.HandleFunc("GET /events", s.events)

	// Without --write the write routes do not exist: they answer 404.
	if s.cfg.Write {
		s.writeRoutes()
	}
}

func (s *Studio) overview(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, http.StatusOK, "overview", nil)
}

// view registers a read route whose page may refresh itself through /events.
func (s *Studio) view(pattern string, h http.HandlerFunc) {
	s.livePatterns[pattern] = true
	s.mux.HandleFunc(pattern, h)
}

func (s *Studio) source(r *http.Request) Source {
	for _, src := range s.cfg.Sources {
		if src.Name() == r.PathValue("source") {
			return src
		}
	}

	return nil
}

func (s *Studio) mongo(r *http.Request) MongoSource {
	m, _ := s.source(r).(MongoSource)
	return m
}

func (s *Studio) redis(r *http.Request) RedisSource {
	m, _ := s.source(r).(RedisSource)
	return m
}

// fail answers a data error: 404 for something that is not there, 422 for
// input the browser sent, 500 otherwise. The message is the database's own
// (no stack trace) and never holds the connection URL, which sources redact.
func (s *Studio) fail(w http.ResponseWriter, r *http.Request, err error) {
	status := http.StatusInternalServerError

	switch {
	case errors.Is(err, ErrNotFound):
		status = http.StatusNotFound
	case errors.Is(err, errInput):
		status = http.StatusUnprocessableEntity
	}

	s.cfg.Log.WarnContext(r.Context(), "studio request failed", "path", r.URL.Path, "status", status,
		"cause", errors.Unwrap(err))
	s.page(w, r, status, "error", map[string]any{"Message": err.Error()})
}

// errInput marks a refusal caused by what the browser sent.
var errInput = errors.New("invalid input")

func inputError(msg string) error { return errors.Join(errInput, errors.New(msg)) }

// page renders a page inside the layout (see render).
func (s *Studio) page(w http.ResponseWriter, r *http.Request, status int, name string, data map[string]any) {
	s.render(w, r, status, name, data)
}

// sourcePage lists the collections or the keys of a source, by its Kind.
func (s *Studio) sourcePage(w http.ResponseWriter, r *http.Request) {
	switch src := s.source(r).(type) {
	case MongoSource:
		cols, err := src.Collections(r.Context())
		if err != nil {
			s.fail(w, r, err)
			return
		}

		s.page(
			w,
			r,
			http.StatusOK,
			"collections",
			map[string]any{"Source": src.Name(), "Label": src.Label(), "Collections": cols},
		)
	case RedisSource:
		s.keysPage(w, r, src)
	case SQLSource:
		s.tablesPage(w, r, src)
	default:
		http.NotFound(w, r)
	}
}

// filtersFrom reads f=<column>:<op>:<value> (repeatable).
func filtersFrom(q url.Values) []Filter {
	var out []Filter

	for _, raw := range q["f"] {
		col, rest, ok := strings.Cut(raw, ":")
		op, val, _ := strings.Cut(rest, ":")

		if ok {
			out = append(out, Filter{Column: col, Op: op, Value: val})
		}
	}

	return out
}

func intParam(q url.Values, key string) int {
	n, _ := strconv.Atoi(q.Get(key))
	return n
}

func (s *Studio) documentsPage(w http.ResponseWriter, r *http.Request) {
	m := s.mongo(r)
	if m == nil {
		http.NotFound(w, r)
		return
	}

	q := r.URL.Query()
	query := DocQuery{
		Collection: r.PathValue("collection"), Filters: filtersFrom(q), Sort: q.Get("sort"), Desc: q.Get("desc") == "1",
		After: q.Get("after"), Page: intParam(q, "page"), PerPage: intParam(q, "per"),
	}

	pg, err := m.Documents(r.Context(), query)
	if err != nil {
		s.fail(w, r, err)
		return
	}

	next := ""
	if pg.Next != "" {
		v := q
		v.Set("after", pg.Next)
		next = r.URL.Path + "?" + v.Encode()
	}

	s.page(w, r, http.StatusOK, "documents", map[string]any{
		"Source": m.Name(), "Collection": query.Collection, "Page": pg, "Next": next, "Query": q.Encode(),
	})
}

func (s *Studio) documentPage(w http.ResponseWriter, r *http.Request) {
	m := s.mongo(r)
	if m == nil {
		http.NotFound(w, r)
		return
	}

	d, err := m.Document(r.Context(), r.PathValue("collection"), r.PathValue("id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}

	s.page(w, r, http.StatusOK, "document", map[string]any{
		"Source": m.Name(), "Collection": r.PathValue("collection"), "Doc": d, "Confirm": confirmText(d.ID),
	})
}

func (s *Studio) mongoConsole(w http.ResponseWriter, r *http.Request) {
	m := s.mongo(r)
	if m == nil {
		http.NotFound(w, r)
		return
	}

	data := map[string]any{"Source": m.Name()}

	if r.Method == http.MethodPost {
		r.Body = http.MaxBytesReader(w, r.Body, maxFormBytes)
		coll, kind, text := r.FormValue("collection"), r.FormValue("kind"), r.FormValue("text")
		data["Collection"], data["Kind"], data["Text"] = coll, kind, text

		ctx, cancel := context.WithTimeout(r.Context(), mongoTimeout)
		defer cancel()

		var (
			pg  DocPage
			err error
		)

		if kind == "aggregate" {
			pg, err = m.Aggregate(ctx, coll, text)
			s.auditConsole(ctx, m, coll, err)
		} else {
			pg, err = m.Find(ctx, coll, text)
		}

		if err != nil {
			data["Error"] = err.Error()
		} else {
			data["Page"] = pg
		}
	}

	s.page(w, r, http.StatusOK, "mongoconsole", data)
}

// keysPage lists keys: one SCAN step with the match pattern and cursor.
func (s *Studio) keysPage(w http.ResponseWriter, r *http.Request, src RedisSource) {
	q := r.URL.Query()

	cursor, _ := strconv.ParseUint(q.Get("cursor"), 10, 64)

	pg, err := src.Keys(r.Context(), KeyQuery{Match: q.Get("match"), Cursor: cursor, Count: intParam(q, "count")})
	if err != nil {
		s.fail(w, r, err)
		return
	}

	next := ""
	if pg.Cursor != 0 {
		v := url.Values{"match": {q.Get("match")}, "cursor": {strconv.FormatUint(pg.Cursor, 10)}}
		next = r.URL.Path + "?" + v.Encode()
	}

	s.page(w, r, http.StatusOK, "keys", map[string]any{
		"Source": src.Name(), "Label": src.Label(), "Page": pg, "Match": q.Get("match"), "Next": next,
	})
}

func (s *Studio) keyPage(w http.ResponseWriter, r *http.Request) {
	src := s.redis(r)
	if src == nil {
		http.NotFound(w, r)
		return
	}

	d, err := src.Key(r.Context(), r.PathValue("key"), intParam(r.URL.Query(), "page"))
	if err != nil {
		s.fail(w, r, err)
		return
	}

	ttl := ""
	if d.TTL > 0 {
		ttl = d.TTL.Round(time.Second).String()
	}

	s.page(w, r, http.StatusOK, "key", map[string]any{"Source": src.Name(), "Detail": d, "TTL": ttl, "Confirm": d.Key})
}

func (s *Studio) redisConsole(w http.ResponseWriter, r *http.Request) {
	src := s.redis(r)
	if src == nil {
		http.NotFound(w, r)
		return
	}

	data := map[string]any{"Source": src.Name()}

	if r.Method == http.MethodPost {
		r.Body = http.MaxBytesReader(w, r.Body, maxFormBytes)
		line := r.FormValue("line")
		data["Line"] = line

		out, err := src.Command(r.Context(), line, s.cfg.Write)
		s.auditRedisCommand(r.Context(), src, line, err)

		if err != nil {
			data["Error"] = err.Error()
		} else {
			data["Result"] = out
		}
	}

	s.page(w, r, http.StatusOK, "redisconsole", data)
}

// confirmText is what a person types to confirm deleting the document: the
// ObjectID's hex, a number, a string _id without its quotes, or the _id's JSON.
func confirmText(id string) string {
	if h, ok := strings.CutPrefix(id, `{"$oid":"`); ok && strings.HasSuffix(h, `"}`) {
		return strings.TrimSuffix(h, `"}`)
	}

	for _, wrap := range []string{`{"$numberInt":"`, `{"$numberLong":"`} {
		if n, ok := strings.CutPrefix(id, wrap); ok && strings.HasSuffix(n, `"}`) {
			return strings.TrimSuffix(n, `"}`)
		}
	}

	if len(id) >= 2 && id[0] == '"' && id[len(id)-1] == '"' {
		if s, err := strconv.Unquote(id); err == nil {
			return s
		}
	}

	return id
}
