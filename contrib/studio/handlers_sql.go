package studio

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// sqlRoutes registers the SQL browser and console. They are read routes: a
// console statement is read-only unless Studio runs with --write.
func (s *Studio) sqlRoutes() {
	s.view("GET /s/{source}/t/{table}", s.rowsPage)
	s.view("GET /s/{source}/t/{table}/r", s.rowPage)
	s.mux.HandleFunc("GET /s/{source}/console", s.sqlConsole)
	s.mux.HandleFunc("POST /s/{source}/console", s.sqlConsole)
}

func (s *Studio) sql(r *http.Request) SQLSource {
	src, _ := s.source(r).(SQLSource)
	return src
}

// tablesPage lists the tables of a SQL source.
func (s *Studio) tablesPage(w http.ResponseWriter, r *http.Request, src SQLSource) {
	ts, err := src.Tables(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}

	s.page(w, r, http.StatusOK, "tables", map[string]any{"Source": src.Name(), "Label": src.Label(), "Tables": ts})
}

// gridCell is a Cell with the link the table partial draws.
type gridCell struct {
	Text string
	Null bool
	Link string
}

func rowLink(source, table string, key []string) string {
	v := url.Values{"k": key}
	return "/s/" + url.PathEscape(source) + "/t/" + url.PathEscape(table) + "/r?" + v.Encode()
}

func (s *Studio) rowsPage(w http.ResponseWriter, r *http.Request) {
	src := s.sql(r)
	if src == nil {
		http.NotFound(w, r)
		return
	}

	q := r.URL.Query()
	query := RowQuery{
		Table: r.PathValue("table"), Filters: filtersFrom(q), Sort: q.Get("sort"), Desc: q.Get("desc") == "1",
		After: q["after"], Page: intParam(q, "page"), PerPage: intParam(q, "per"),
	}

	t, err := src.Table(r.Context(), query.Table)
	if err != nil {
		s.fail(w, r, err)
		return
	}

	pg, err := src.Rows(r.Context(), query)
	if err != nil {
		s.fail(w, r, err)
		return
	}

	head := make([]string, len(pg.Columns))
	for i, c := range pg.Columns {
		head[i] = c.Name
	}

	rows := gridCells(pg.Rows)

	for i, row := range pg.Rows {
		if key := keyOf(t, row); key != nil && len(rows[i]) > 0 {
			rows[i][0].Link = rowLink(src.Name(), t.Name, key)
		}
	}

	var prev, next string

	switch {
	case pg.Next != nil:
		v := cloneValues(q)
		v.Del("after")
		v["after"] = pg.Next
		next = r.URL.Path + "?" + v.Encode()
	case pg.HasMore && query.Sort != "" || pg.HasMore && len(t.PrimaryKey) == 0:
		next = pageLink(r.URL.Path, q, max(query.Page, 1)+1)
	}

	if query.Sort != "" || len(t.PrimaryKey) == 0 {
		if p := max(query.Page, 1); p > 1 {
			prev = pageLink(r.URL.Path, q, p-1)
		}
	}

	s.page(w, r, http.StatusOK, "rows", map[string]any{
		"Source": src.Name(), "Table": t, "Head": head, "Rows": rows, "Next": next, "Prev": prev,
		"Query": q.Encode(), "Filters": q["f"],
	})
}

func cloneValues(v url.Values) url.Values {
	out := make(url.Values, len(v))
	for k, vs := range v {
		out[k] = append([]string(nil), vs...)
	}

	return out
}

func pageLink(path string, q url.Values, page int) string {
	v := cloneValues(q)
	v.Set("page", strconv.Itoa(page))

	return path + "?" + v.Encode()
}

func (s *Studio) rowPage(w http.ResponseWriter, r *http.Request) {
	src := s.sql(r)
	if src == nil {
		http.NotFound(w, r)
		return
	}

	t, err := src.Table(r.Context(), r.PathValue("table"))
	if err != nil {
		s.fail(w, r, err)
		return
	}

	d, err := src.Row(r.Context(), t.Name, r.URL.Query()["k"])
	if err != nil {
		s.fail(w, r, err)
		return
	}

	type field struct {
		Name, Type, Text string
		Null             bool
	}

	fields := make([]field, len(d.Columns))
	for i, c := range d.Columns {
		fields[i] = field{Name: c.Name, Type: c.Type, Text: d.Cells[i].Text, Null: d.Cells[i].Null}
	}

	s.page(w, r, http.StatusOK, "row", map[string]any{"Source": src.Name(), "Table": t, "Fields": fields})
}

func (s *Studio) sqlConsole(w http.ResponseWriter, r *http.Request) {
	src := s.sql(r)
	if src == nil {
		http.NotFound(w, r)
		return
	}

	data := map[string]any{"Source": src.Name(), "CanWrite": s.cfg.Write}

	if r.Method == http.MethodPost {
		r.Body = http.MaxBytesReader(w, r.Body, maxFormBytes)

		text, explain := r.FormValue("sql"), r.FormValue("explain") == "1"
		write := s.cfg.Write && r.FormValue("write") == "1"
		data["SQL"], data["Explain"], data["WriteMode"] = text, explain, write

		run := text
		if explain {
			run = src.Explain(text)
		}

		rs, err := src.Query(r.Context(), run, write)
		if write {
			s.audit(r.Context(), src, "console", "sql", err, "statement", truncateBytes(text, 200))
		}

		if err != nil {
			s.cfg.Log.WarnContext(
				r.Context(),
				"studio console failed",
				"error",
				err.Error(),
				"cause",
				errors.Unwrap(err),
			)
			data["Error"] = clientMessage(err)
		} else {
			data["Result"], data["ResultRows"] = rs, gridCells(rs.Rows)
		}
	}

	s.page(w, r, http.StatusOK, "sqlconsole", data)
}

// clientMessage is what the browser may read about err: the reason of a
// refusal it caused, never the text of a database error.
func clientMessage(err error) string {
	if _, ok := errors.AsType[*queryError](err); ok {
		return "The database refused or could not run the statement. The details are in the Studio log."
	}

	if errors.Is(err, errInput) {
		return strings.TrimPrefix(strings.TrimPrefix(err.Error(), errInput.Error()), "\n")
	}

	return "The statement failed."
}

func truncateBytes(s string, n int) string {
	if len(s) <= n {
		return s
	}

	return s[:n]
}

func gridCells(rows [][]Cell) [][]gridCell {
	out := make([][]gridCell, len(rows))

	for i, row := range rows {
		out[i] = make([]gridCell, len(row))
		for j, c := range row {
			out[i][j] = gridCell{Text: c.Text, Null: c.Null}
		}
	}

	return out
}
