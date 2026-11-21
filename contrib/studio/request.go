package studio

import (
	"net/url"
	"strconv"
	"strings"
)

// params names the query parameters a surface uses: the HTML pages and the
// JSON API read the same things under slightly different names.
type params struct {
	filter, perPage string
}

var htmlParams = params{filter: "f", perPage: "per"} //nolint:gochecknoglobals // read-only

// filtersFrom reads <name>=<column>:<op>:<value> (repeatable). A filter
// without an operator is refused: dropping it would show every row as if it
// matched. Column and operator are checked against the catalog later, where
// the query is built.
func filtersFrom(q url.Values, name string) ([]Filter, error) {
	var out []Filter

	for _, raw := range q[name] {
		col, rest, ok := strings.Cut(raw, ":")
		if !ok || col == "" {
			return nil, inputError("a filter must look like column:op:value, for example qty:gt:5")
		}

		op, val, _ := strings.Cut(rest, ":")
		out = append(out, Filter{Column: col, Op: op, Value: val})
	}

	return out, nil
}

func intParam(q url.Values, key string) int {
	n, _ := strconv.Atoi(q.Get(key))
	return n
}

func boolParam(q url.Values, key string) bool {
	v := q.Get(key)
	return v == "1" || v == "true"
}

// rowQueryFrom builds the page request of a table from the query string. The
// table, sort and filter columns are still names from the request: the source
// checks every one against the introspected catalog before it builds SQL.
func rowQueryFrom(table string, q url.Values, p params) (RowQuery, error) {
	filters, err := filtersFrom(q, p.filter)
	if err != nil {
		return RowQuery{}, err
	}

	return RowQuery{
		Table: table, Filters: filters, Sort: q.Get("sort"), Desc: boolParam(q, "desc"),
		After: q["after"], Page: intParam(q, "page"), PerPage: intParam(q, p.perPage),
	}, nil
}

// docQueryFrom builds the page request of a MongoDB collection.
func docQueryFrom(collection string, q url.Values, p params) (DocQuery, error) {
	filters, err := filtersFrom(q, p.filter)
	if err != nil {
		return DocQuery{}, err
	}

	return DocQuery{
		Collection: collection, Filters: filters, Sort: q.Get("sort"), Desc: boolParam(q, "desc"),
		After: q.Get("after"), Page: intParam(q, "page"), PerPage: intParam(q, p.perPage),
	}, nil
}

// keyQueryFrom builds one SCAN step of a Redis source.
func keyQueryFrom(q url.Values) KeyQuery {
	cursor, _ := strconv.ParseUint(q.Get("cursor"), 10, 64)

	return KeyQuery{Match: q.Get("match"), Cursor: cursor, Count: intParam(q, "count")}
}
