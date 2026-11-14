package studio

import (
	"errors"
	"html/template"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
)

// sqlRoutes registers the SQL browser and console. They are read routes: a
// console statement is read-only unless Studio runs with --write.
func (s *Studio) sqlRoutes() {
	s.view("GET /s/{source}/t/{table}", s.rowsPage)
	s.view("GET /s/{source}/t/{table}/r", s.rowPage)
	s.mux.HandleFunc("GET /s/{source}/t/{table}/export", s.exportRows)
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

	filters, err := filtersFrom(q)
	if err != nil {
		s.fail(w, r, err)
		return
	}

	query := RowQuery{
		Table: r.PathValue("table"), Filters: filters, Sort: q.Get("sort"), Desc: q.Get("desc") == "1",
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
	sorts := make([]sortHead, len(pg.Columns))

	for i, c := range pg.Columns {
		head[i] = c.Name
		sorts[i] = sortLink(r.URL.Path, q, c.Name)
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
		v.Add("back", encodeCursor(q["after"]))
		v.Del("after")
		v["after"] = pg.Next
		next = r.URL.Path + "?" + v.Encode()
	case pg.HasMore && query.Sort != "" || pg.HasMore && len(t.PrimaryKey) == 0:
		next = pageLink(r.URL.Path, q, max(query.Page, 1)+1)
	}

	switch {
	case query.Sort != "" || len(t.PrimaryKey) == 0:
		if p := max(query.Page, 1); p > 1 {
			prev = pageLink(r.URL.Path, q, p-1)
		}
	case len(q["back"]) > 0:
		prev = keysetPrev(r.URL.Path, q)
	}

	s.page(w, r, http.StatusOK, "rows", map[string]any{
		"Source": src.Name(), "Table": t, "Head": head, "Sorts": sorts, "Rows": rows, "Next": next, "Prev": prev,
		"Query": q.Encode(), "Filters": q["f"],
	})
}

// sortHead is the header link of one column: Dir is the aria-sort value of the
// current order ("" when the grid is not sorted by it).
type sortHead struct{ Link, Dir string }

// sortLink sorts by col ascending, then descending, and restarts the paging.
func sortLink(path string, q url.Values, col string) sortHead {
	v := cloneValues(q)
	v.Del("after")
	v.Del("back")
	v.Del("page")
	v.Set("sort", col)
	v.Del("desc")

	h := sortHead{}

	if q.Get("sort") == col {
		h.Dir = "ascending"

		if q.Get("desc") == "1" {
			h.Dir = "descending"
		} else {
			v.Set("desc", "1")
		}
	}

	h.Link = path + "?" + v.Encode()

	return h
}

// encodeCursor packs a keyset cursor into one query value; the first page has
// no cursor and packs to "".
func encodeCursor(key []string) string {
	parts := make([]string, len(key))
	for i, k := range key {
		parts[i] = url.QueryEscape(k)
	}

	return strings.Join(parts, ",")
}

func decodeCursor(s string) []string {
	if s == "" {
		return nil
	}

	parts := strings.Split(s, ",")
	for i, p := range parts {
		parts[i], _ = url.QueryUnescape(p)
	}

	return parts
}

// keysetPrev links to the page before this one. A keyset page only knows the
// cursor after its own last row, so each Next link carries the cursor it left
// from in back (a stack) and Previous pops it.
func keysetPrev(path string, q url.Values) string {
	back := q["back"]
	v := cloneValues(q)
	v.Del("after")
	v.Del("back")

	if cur := decodeCursor(back[len(back)-1]); cur != nil {
		v["after"] = cur
	}

	for _, b := range back[:len(back)-1] {
		v.Add("back", b)
	}

	return path + "?" + v.Encode()
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
		Null, Editable   bool
	}

	// A value that was cut or is binary is shown but never offered for editing:
	// saving it would write the cut text back.
	canWrite := s.cfg.Write && len(t.PrimaryKey) > 0
	fields := make([]field, len(d.Columns))

	for i, c := range d.Columns {
		cell := d.Cells[i]
		fields[i] = field{
			Name: c.Name, Type: c.Type, Text: cell.Text, Null: cell.Null,
			Editable: canWrite && !cell.Truncated && !cell.Binary && !slices.Contains(t.PrimaryKey, c.Name),
		}
	}

	key := r.URL.Query()["k"]

	refs, err := src.References(r.Context(), t.Name)
	if err != nil {
		s.fail(w, r, err)
		return
	}

	type incoming struct{ Table, Link string }

	var in []incoming

	for _, ref := range refs {
		v := url.Values{}

		for i, col := range ref.Columns {
			for j, c := range d.Columns {
				if c.Name == ref.RefColumns[i] && !d.Cells[j].Null && !d.Cells[j].Truncated {
					v.Add("f", col+":eq:"+d.Cells[j].Text)
				}
			}
		}

		if len(v["f"]) == len(ref.Columns) {
			in = append(
				in,
				incoming{
					ref.Table,
					"/s/" + url.PathEscape(src.Name()) + "/t/" + url.PathEscape(ref.Table) + "?" + v.Encode(),
				},
			)
		}
	}

	type outgoing struct{ Table, Columns, Link string }

	var out []outgoing

	for _, fk := range t.ForeignKeys {
		o := outgoing{Table: fk.RefTable, Columns: strings.Join(fk.Columns, ", ")}
		o.Link = s.referencedLink(r, src, d, fk)
		out = append(out, o)
	}

	// Encode already escapes every byte; the template would escape it again and
	// turn the form action into ?k%3d1.
	rowKey := template.URL(url.Values{"k": key}.Encode()) //nolint:gosec // G203: see above

	s.page(w, r, http.StatusOK, "row", map[string]any{
		"Source":   src.Name(),
		"Table":    t,
		"Fields":   fields,
		"Incoming": in,
		"Outgoing": out,
		"CanWrite": canWrite,
		"Key":      rowKey,
		"Confirm":  confirmKey(key),
	})
}

// referencedLink is where a foreign key of the row points: the referenced row
// when the key is the primary key of that table, otherwise the referenced
// table filtered to the matching rows; the table itself when a value is NULL.
func (s *Studio) referencedLink(r *http.Request, src SQLSource, d RowDetail, fk ForeignKey) string {
	base := "/s/" + url.PathEscape(src.Name()) + "/t/" + url.PathEscape(fk.RefTable)
	vals := make([]string, len(fk.Columns))

	for i, col := range fk.Columns {
		found := false

		for j, c := range d.Columns {
			if c.Name == col && !d.Cells[j].Null && !d.Cells[j].Truncated {
				vals[i], found = d.Cells[j].Text, true
			}
		}

		if !found {
			return base
		}
	}

	if ref, err := src.Table(r.Context(), fk.RefTable); err == nil && slices.Equal(ref.PrimaryKey, fk.RefColumns) {
		return rowLink(src.Name(), fk.RefTable, vals)
	}

	v := url.Values{}
	for i, col := range fk.RefColumns {
		v.Add("f", col+":eq:"+vals[i])
	}

	return base + "?" + v.Encode()
}

func (s *Studio) sqlConsole(w http.ResponseWriter, r *http.Request) {
	src := s.sql(r)
	if src == nil {
		http.NotFound(w, r)
		return
	}

	data := map[string]any{"Source": src.Name(), "CanWrite": s.cfg.Write}

	if r.Method == http.MethodPost {
		if !isLoopbackRequest(r) {
			http.Error(w, "SQL console POST is only allowed from localhost", http.StatusForbidden)
			return
		}
		if s.cfg.Token == "" || r.Header.Get("X-Studio-Token") != s.cfg.Token {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}

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

func isLoopbackRequest(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return false
	}

	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
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
