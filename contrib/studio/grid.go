package studio

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/AlexGalhardo/vukem/database"
)

const (
	// A page deep into a view is read with OFFSET (SQL) or skip (MongoDB), which
	// reads and drops every row before it. A view in primary key order walks an
	// index, so it reaches further than a sorted one, which may scan the table.
	maxSortedRows  = 100_000
	maxOrderedRows = 1_000_000

	// maxAPIPerPage is the most rows one request of the browsing API returns; the
	// interface offers 25, 50, 75 and 100. Exports read 200 at a time internally.
	maxAPIPerPage = 100

	gridTimeout   = 30 * time.Second
	gridCellRunes = 200
	detailCellMax = 64 << 10
)

// maxGridPage is the last page reachable with perPage rows a page: a page past it
// is read as the last one. ordered is true for the primary key order.
func maxGridPage(perPageRows int, ordered bool) int {
	limit := maxSortedRows
	if ordered {
		limit = maxOrderedRows
	}

	return max(limit/perPage(perPageRows), 1)
}

// RowQuery selects a page of rows of one table.
type RowQuery struct {
	Table   string
	Filters []Filter // combined with AND
	Sort    string   // a column name; empty means the primary key
	Desc    bool
	After   []string // keyset cursor: the primary key of the last row
	Page    int      // 1-based; used when there is no cursor (the cursor wins)
	PerPage int      // 1..200, default 50 (the HTTP API accepts 1..100)
	Full    bool     // keep values up to 64 KiB instead of 200 runes (export)
}

// Cell is one value of a result, ready to show.
type Cell struct {
	Text      string // cut at 200 runes in a grid
	Null      bool
	Truncated bool
	Binary    bool // shown as "<binary N bytes>"
	Literal   bool // Text is a JSON number or boolean: an export writes it unquoted
}

// RowPage is a page of rows.
type RowPage struct {
	Columns []Column
	Rows    [][]Cell
	Next    []string // cursor of the next page; nil on the last or when keyset is not in use
	HasMore bool
}

// RowDetail is one row with its values in full (up to 64 KiB each).
type RowDetail struct {
	Columns []Column
	Cells   []Cell
}

// ResultSet is the result of a console statement.
type ResultSet struct {
	Columns  []string
	Rows     [][]Cell
	Truncate bool   // more than 1000 rows came back, or the text of the result passed its size limit
	Kind     string // the statement's verb in upper case, for display
	// Affected is the row count of a write that returns no rows; HasAffected
	// tells it from a result of zero rows.
	Affected    int64
	HasAffected bool
	Elapsed     time.Duration
}

// queryError hides what the database said: its text can carry values, table
// names or paths (CWE-209). The cause stays available to the server log.
type queryError struct{ cause error }

func (*queryError) Error() string   { return "studio: the database could not run the query" }
func (e *queryError) Unwrap() error { return e.cause }

// filterOps are the comparison operators a filter may use besides the LIKE
// and NULL forms handled in buildRows.
var filterOps = map[string]string{
	"eq": "=", "ne": "<>", "lt": "<", "lte": "<=", "gt": ">", "gte": ">=",
}

// likeEscape escapes the LIKE wildcards of v with '!', the escape character
// every query declares with ESCAPE '!': a backslash has its own meaning in a
// MySQL string literal, '!' none in any dialect.
func likeEscape(v string) string {
	return strings.NewReplacer("!", "!!", "%", "!%", "_", "!_").Replace(v)
}

func hasColumn(t Table, name string) bool {
	for _, c := range t.Columns {
		if c.Name == name {
			return true
		}
	}

	return false
}

// filterClause is the condition of one filter, its value bound through bind.
func filterClause(d database.Dialect, t Table, f Filter, bind func(any) string) (string, error) {
	if !hasColumn(t, f.Column) {
		return "", inputError("unknown column in a filter")
	}

	col := d.Quote(f.Column)

	switch f.Op {
	case "null":
		return col + " IS NULL", nil
	case "notnull":
		return col + " IS NOT NULL", nil
	case "contains", "starts":
		v := likeEscape(f.Value) + "%"
		if f.Op == "contains" {
			v = "%" + v
		}

		// PostgreSQL has no LIKE on numbers or dates.
		if database.MustCapabilities(d).Family == "postgres" {
			col = "CAST(" + col + " AS TEXT)"
		}

		return col + " LIKE " + bind(v) + " ESCAPE '!'", nil
	}

	op, ok := filterOps[f.Op]
	if !ok {
		return "", inputError("unknown filter operator")
	}

	return col + " " + op + " " + bind(f.Value), nil
}

// filterClauses is the AND-ed conditions of filters.
func filterClauses(d database.Dialect, t Table, filters []Filter, bind func(any) string) ([]string, error) {
	var where []string

	for _, f := range filters {
		clause, err := filterClause(d, t, f, bind)
		if err != nil {
			return nil, err
		}

		where = append(where, clause)
	}

	return where, nil
}

// buildRows returns the SELECT of q. Every filter and sort column is checked
// against t (read from the catalog) and quoted by the dialect, every value is
// a bound parameter, and a refusal happens before any SQL exists.
func buildRows(d database.Dialect, t Table, q RowQuery) (string, []any, error) {
	var args []any

	bind := func(v any) string {
		args = append(args, v)
		return d.Placeholder(len(args))
	}

	where, err := filterClauses(d, t, q.Filters, bind)
	if err != nil {
		return "", nil, err
	}

	keyset := q.Sort == "" && len(t.PrimaryKey) > 0
	order := append([]string(nil), t.PrimaryKey...)

	if q.Sort != "" {
		if !hasColumn(t, q.Sort) {
			return "", nil, inputError("unknown sort column")
		}

		order = append([]string{q.Sort}, t.PrimaryKey...)
	}

	if keyset && len(q.After) > 0 {
		if len(q.After) != len(order) {
			return "", nil, inputError("the cursor does not match the primary key")
		}

		cols, ph := make([]string, len(order)), make([]string, len(order))
		for i, c := range order {
			cols[i], ph[i] = d.Quote(c), bind(q.After[i])
		}

		cmp := " > "
		if q.Desc {
			cmp = " < "
		}

		where = append(where, "("+strings.Join(cols, ", ")+")"+cmp+"("+strings.Join(ph, ", ")+")")
	}

	cols := make([]string, len(t.Columns))
	for i, c := range t.Columns {
		cols[i] = d.Quote(c.Name)
	}

	var b strings.Builder

	b.WriteString("SELECT " + strings.Join(cols, ", ") + " FROM " + d.Quote(t.Name))

	if len(where) > 0 {
		b.WriteString(" WHERE " + strings.Join(where, " AND "))
	}

	if len(order) > 0 {
		dir := ""
		if q.Desc {
			dir = " DESC"
		}

		parts, seen := make([]string, 0, len(order)), map[string]bool{}

		for _, c := range order {
			if !seen[c] {
				seen[c] = true
				parts = append(parts, d.Quote(c)+dir)
			}
		}

		b.WriteString(" ORDER BY " + strings.Join(parts, ", "))
	}

	// The numbers are computed here, never taken from the client as text.
	n := perPage(q.PerPage)
	b.WriteString(" LIMIT " + strconv.Itoa(n+1))

	// A keyset view follows its cursor; without one, and past the first page, it
	// is read by offset like a sorted view, so a page can be asked for by number.
	if !keyset || (len(q.After) == 0 && q.Page > 1) {
		page := min(max(q.Page, 1), maxGridPage(n, keyset))
		b.WriteString(" OFFSET " + strconv.Itoa((page-1)*n))
	}

	return b.String(), args, nil
}

// cellOf turns a scanned value into a Cell, cutting text at limit runes.
func cellOf(v any, limit int) Cell {
	var s string

	switch x := v.(type) {
	case nil:
		return Cell{Null: true}
	case []byte:
		if !utf8.Valid(x) {
			return Cell{Text: fmt.Sprintf("<binary %d bytes>", len(x)), Binary: true}
		}

		s = string(x)
	case string:
		s = x
	case time.Time:
		s = x.UTC().Format(time.RFC3339Nano)
	case bool:
		return Cell{Text: strconv.FormatBool(x), Literal: true}
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		return Cell{Text: fmt.Sprint(x), Literal: true}
	case float64:
		return floatCell(x, 64)
	case float32:
		return floatCell(float64(x), 32)
	default:
		s = fmt.Sprint(x)
	}

	if utf8.RuneCountInString(s) > limit {
		return Cell{Text: string([]rune(s)[:limit]), Truncated: true}
	}

	return Cell{Text: s}
}

// floatCell is a number cell; NaN and the infinities are not JSON numbers, so
// they stay text.
func floatCell(f float64, bits int) Cell {
	return Cell{
		Text:    strconv.FormatFloat(f, 'g', -1, bits),
		Literal: !math.IsNaN(f) && !math.IsInf(f, 0),
	}
}

// scanRow reads the current row of rows into cells.
func scanRow(rows *sql.Rows, n, limit int) ([]Cell, error) {
	vals, ptrs := make([]any, n), make([]any, n)
	for i := range vals {
		ptrs[i] = &vals[i]
	}

	if err := rows.Scan(ptrs...); err != nil {
		return nil, err
	}

	cells := make([]Cell, n)
	for i, v := range vals {
		cells[i] = cellOf(v, limit)
	}

	return cells, nil
}

// Rows implements SQLSource.
func (s *SQLDB) Rows(ctx context.Context, q RowQuery) (RowPage, error) {
	t, err := s.Table(ctx, q.Table)
	if err != nil {
		return RowPage{}, err
	}

	query, args, err := buildRows(s.db.Dialect(), t, q)
	if err != nil {
		return RowPage{}, err
	}

	ctx, cancel := context.WithTimeout(ctx, gridTimeout)
	defer cancel()

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return RowPage{}, &queryError{err}
	}
	defer rows.Close()

	n, page, limit := perPage(q.PerPage), RowPage{Columns: t.Columns}, gridCellRunes
	if q.Full {
		limit = detailCellMax
	}

	for rows.Next() {
		if len(page.Rows) == n {
			page.HasMore = true
			break
		}

		cells, err := scanRow(rows, len(t.Columns), limit)
		if err != nil {
			return RowPage{}, &queryError{err}
		}

		page.Rows = append(page.Rows, cells)
	}

	if err := rows.Err(); err != nil {
		return RowPage{}, &queryError{err}
	}

	if page.HasMore && q.Sort == "" && len(t.PrimaryKey) > 0 {
		page.Next = keyOf(t, page.Rows[len(page.Rows)-1])
	}

	return page, nil
}

// keyOf is the primary key of a row, or nil when a part of it is null, cut or binary.
func keyOf(t Table, row []Cell) []string {
	var key []string

	for _, pk := range t.PrimaryKey {
		for i, c := range t.Columns {
			if c.Name != pk {
				continue
			}

			if row[i].Null || row[i].Truncated || row[i].Binary {
				return nil
			}

			key = append(key, row[i].Text)
		}
	}

	return key
}

// Row implements SQLSource: one row by its primary key.
func (s *SQLDB) Row(ctx context.Context, table string, key []string) (RowDetail, error) {
	t, err := s.Table(ctx, table)
	if err != nil {
		return RowDetail{}, err
	}

	if len(t.PrimaryKey) == 0 || len(key) != len(t.PrimaryKey) {
		return RowDetail{}, ErrNotFound
	}

	d := s.db.Dialect()
	cols, where, args := make([]string, len(t.Columns)), make([]string, len(key)), make([]any, len(key))

	for i, c := range t.Columns {
		cols[i] = d.Quote(c.Name)
	}

	for i, v := range key {
		where[i], args[i] = d.Quote(t.PrimaryKey[i])+" = "+d.Placeholder(i+1), v
	}

	ctx, cancel := context.WithTimeout(ctx, gridTimeout)
	defer cancel()

	rows, err := s.db.QueryContext(ctx,
		"SELECT "+strings.Join(cols, ", ")+" FROM "+d.Quote(t.Name)+" WHERE "+strings.Join(where, " AND "), args...)
	if err != nil {
		return RowDetail{}, &queryError{err}
	}
	defer rows.Close()

	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return RowDetail{}, &queryError{err}
		}

		return RowDetail{}, ErrNotFound
	}

	cells, err := scanRow(rows, len(t.Columns), detailCellMax)
	if err != nil {
		return RowDetail{}, &queryError{err}
	}

	return RowDetail{Columns: t.Columns, Cells: cells}, nil
}

// Tables implements SQLSource.
func (s *SQLDB) Tables(ctx context.Context) ([]Table, error) {
	ts, err := s.catalog.Tables(ctx)
	if err != nil {
		return nil, &queryError{err}
	}

	// The catalog may hand out a cached slice; the estimates go on a copy.
	ts = slices.Clone(ts)

	// One budget for every COUNT, so a big database cannot stall the page.
	cctx, cancel := context.WithTimeout(ctx, estimateBudget)
	defer cancel()

	for i := range ts {
		ts[i].RowEstimate = s.count(cctx, ts[i].Name)
	}

	return ts, nil
}

// Table implements SQLSource; a name the catalog does not list is ErrNotFound
// and no SQL has been sent.
func (s *SQLDB) Table(ctx context.Context, name string) (Table, error) {
	t, ok, err := s.catalog.Table(ctx, name)
	if err != nil {
		return Table{}, &queryError{err}
	}

	if !ok {
		return Table{}, ErrNotFound
	}

	t.RowEstimate = -1

	return t, nil
}

const estimateBudget = 2 * time.Second

// count is the number of rows of a table the catalog listed, -1 when the
// count did not finish in time.
func (s *SQLDB) count(ctx context.Context, table string) int64 {
	var n int64

	row := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+s.db.Dialect().Quote(table))
	if err := row.Scan(&n); err != nil {
		return -1
	}

	return n
}

// References implements SQLSource.
func (s *SQLDB) References(ctx context.Context, table string) ([]Reference, error) {
	ts, err := s.catalog.Tables(ctx)
	if err != nil {
		return nil, &queryError{err}
	}

	var out []Reference

	for _, t := range ts {
		for _, fk := range t.ForeignKeys {
			if fk.RefTable == table {
				out = append(out, Reference{Table: t.Name, ForeignKey: fk})
			}
		}
	}

	return out, nil
}

// errNoCatalog is what a SQLDB without a schema catalog answers.
var errNoCatalog = errors.New("studio: the schema catalog is not available")

// countTimeout bounds an exact count: COUNT(*) scans the table on some
// engines, so a huge table answers "unavailable" instead of tying up Studio.
const countTimeout = 10 * time.Second

// ErrCountTimeout is returned by Count when the bounded COUNT(*) ran out of time.
var ErrCountTimeout = errors.New("studio: the count took too long")

// Count implements SQLSource: the exact number of rows matching filters,
// within countTimeout.
func (s *SQLDB) Count(ctx context.Context, table string, filters []Filter) (int64, error) {
	t, err := s.Table(ctx, table)
	if err != nil {
		return 0, err
	}

	d := s.db.Dialect()

	var args []any

	where, err := filterClauses(d, t, filters, func(v any) string {
		args = append(args, v)
		return d.Placeholder(len(args))
	})
	if err != nil {
		return 0, err
	}

	query := "SELECT COUNT(*) FROM " + d.Quote(t.Name)
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}

	limit := countTimeout
	if s.countLimit > 0 {
		limit = s.countLimit
	}

	ctx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()

	var n int64
	if err := s.db.QueryRowContext(ctx, query, args...).Scan(&n); err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return 0, ErrCountTimeout
		}

		return 0, &queryError{err}
	}

	return n, nil
}
