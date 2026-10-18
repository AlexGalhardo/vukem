package api

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/AlexGalhardo/vukem/errs"
	"github.com/AlexGalhardo/vukem/orm"
	"github.com/AlexGalhardo/vukem/web"
)

// Option allowlists one query convention for an endpoint.
type Option func(*spec)

type spec struct {
	filters, sorts, includes, fields []string
	perPage, maxPerPage              int
	maxPage, maxFilterValues         int
}

// Default bounds of Query; see MaxPage and MaxFilterValues.
const (
	DefaultMaxPage         = 1000
	DefaultMaxFilterValues = 100
)

// MaxPage bounds ?page (default DefaultMaxPage), so a request cannot make
// the database skip millions of rows (OFFSET); use ?cursor for deep lists.
// n <= 0 keeps the default.
func MaxPage(n int) Option {
	return func(s *spec) {
		if n > 0 {
			s.maxPage = n
		}
	}
}

// MaxFilterValues bounds the comma-separated values of one ?filter[col]
// (default DefaultMaxFilterValues). n <= 0 keeps the default.
func MaxFilterValues(n int) Option {
	return func(s *spec) {
		if n > 0 {
			s.maxFilterValues = n
		}
	}
}

// Filters allows ?filter[col]=value (comma-separated values mean IN).
func Filters(cols ...string) Option { return func(s *spec) { s.filters = cols } }

// Sorts allows ?sort=col,-col. The last entry is the default sort; a
// leading "-" means descending.
func Sorts(cols ...string) Option { return func(s *spec) { s.sorts = cols } }

// Includes allows ?include=rel,rel; handlers eager-load Request.Includes.
func Includes(rels ...string) Option { return func(s *spec) { s.includes = rels } }

// Fields allows ?fields=col,col (sparse fieldsets).
func Fields(cols ...string) Option { return func(s *spec) { s.fields = cols } }

// PerPage sets the default and maximum ?per_page (larger values are
// capped); the maximum also replaces orm.DefaultMaxPerPage for the query.
func PerPage(def, maxPerPage int) Option {
	return func(s *spec) { s.perPage, s.maxPerPage = def, maxPerPage }
}

// Request is a query refined by the allowlisted conventions.
type Request[T any] struct {
	Query    orm.Query[T]
	Page     int
	PerPage  int
	Cursor   string
	Includes []string
}

// Paginate runs an offset page of the refined query.
func (r Request[T]) Paginate(ctx context.Context) (orm.Page[T], error) {
	return r.Query.Paginate(ctx, r.Page, r.PerPage)
}

// Query applies ?filter, ?sort, ?fields, ?include, ?page, ?per_page and
// ?cursor to q. Every column must be allowlisted: an unknown filter, sort,
// field or include is a 400 problem naming the allowed values, so request
// input never chooses columns freely. A ?page above MaxPage or a filter
// with more than MaxFilterValues values is a 400 too.
func Query[T any](c *web.Ctx, q orm.Query[T], opts ...Option) (Request[T], error) {
	s := spec{perPage: 20, maxPerPage: 100, maxPage: DefaultMaxPage, maxFilterValues: DefaultMaxFilterValues}
	for _, o := range opts {
		o(&s)
	}

	values := c.Request().URL.Query()
	req := Request[T]{Page: max(c.QueryInt("page", 1), 1), Cursor: values.Get("cursor")}
	if req.Page > s.maxPage {
		return req, errs.New(http.StatusBadRequest, fmt.Sprintf("Query parameter page must be at most %d.", s.maxPage))
	}

	req.PerPage = min(max(c.QueryInt("per_page", s.perPage), 1), s.maxPerPage)

	for key, vals := range values {
		col, ok := strings.CutPrefix(key, "filter[")
		if !ok {
			continue
		}

		col, ok = strings.CutSuffix(col, "]")
		if !ok || !slices.Contains(s.filters, col) {
			return req, unknown("filter", col, s.filters)
		}

		parts := strings.Split(vals[len(vals)-1], ",")
		if len(parts) > s.maxFilterValues {
			return req, errs.New(http.StatusBadRequest,
				fmt.Sprintf("Query parameter filter[%s] accepts at most %d values.", col, s.maxFilterValues))
		}
		args := make([]any, len(parts))

		for i, p := range parts {
			args[i] = filterValue(p)
		}

		if len(args) == 1 {
			q = q.Where(col, args[0])
		} else {
			q = q.WhereIn(col, args...)
		}
	}

	sorts := values.Get("sort")
	if sorts == "" && len(s.sorts) > 0 {
		sorts = s.sorts[len(s.sorts)-1]
	}

	for item := range strings.SplitSeq(sorts, ",") {
		if item == "" {
			continue
		}

		col, desc := strings.CutPrefix(item, "-")
		if !slices.Contains(s.sorts, col) && !slices.Contains(s.sorts, "-"+col) {
			return req, unknown("sort", item, s.sorts)
		}

		dir := orm.Asc
		if desc {
			dir = orm.Desc
		}

		q = q.OrderBy(col, dir)
	}

	if f := values.Get("fields"); f != "" {
		cols := strings.Split(f, ",")
		for _, col := range cols {
			if !slices.Contains(s.fields, col) {
				return req, unknown("field", col, s.fields)
			}
		}

		q = q.Select(cols...)
	}

	if inc := values.Get("include"); inc != "" {
		for rel := range strings.SplitSeq(inc, ",") {
			if !slices.Contains(s.includes, rel) {
				return req, unknown("include", rel, s.includes)
			}

			req.Includes = append(req.Includes, rel)
		}
	}

	// The endpoint's maximum replaces orm.DefaultMaxPerPage, which would
	// otherwise lower a PerPage(…, 500) to 100.
	if s.maxPerPage >= 1 {
		q = q.MaxPerPage(s.maxPerPage)
	}

	req.Query = q

	return req, nil
}

// filterValue maps "true"/"false" to booleans (SQLite stores them as 0/1);
// other values stay strings and are bound as parameters.
func filterValue(s string) any {
	if b, err := strconv.ParseBool(s); err == nil && (s == "true" || s == "false") {
		return b
	}

	return s
}

func unknown(kind, got string, allowed []string) error {
	list := "none"
	if len(allowed) > 0 {
		list = strings.Join(allowed, ", ")
	}

	return errs.New(400, fmt.Sprintf("Unknown %s %q. Allowed: %s.", kind, got, list))
}
