package orm

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"reflect"
	"strings"

	"github.com/AlexGalhardo/vukem/errs"
)

// CursorPage is one page of keyset pagination. NextCursor is empty on the
// last page.
type CursorPage[T any] struct {
	Items      []T    `json:"data"`
	PerPage    int    `json:"per_page"`
	NextCursor string `json:"next_cursor,omitempty"`
}

// ErrInvalidCursor is returned (400) when a cursor cannot be decoded.
var ErrInvalidCursor = &errs.HTTPError{Status: 400, Message: "Invalid pagination cursor."}

var errInvalidCursorShape = errors.New("orm: cursor does not match the order keys")

// orderKey is one OrderBy column, kept for keyset pagination.
type orderKey struct {
	col string
	dir Direction
}

// CursorPaginate returns perPage rows after cursor in keyset order. Unlike
// Paginate it needs no COUNT and stays fast and stable on large or
// changing tables. Pass "" for the first page and the returned NextCursor
// for the following ones, with the same query.
//
// Without OrderBy the order is the primary key in dir. With OrderBy
// (Latest included) the keyset is those columns, in their own directions,
// followed by the primary key in dir as the tie-breaker when it is not
// already listed, e.g. OrderBy("score", Desc) pages by (score DESC, id
// dir). The ordered columns must be NOT NULL: rows with NULL keys are
// skipped. perPage is clamped to 1..DefaultMaxPerPage (see MaxPerPage).
func (q Query[T]) CursorPaginate(
	ctx context.Context,
	cursor string,
	perPage int,
	dir Direction,
) (CursorPage[T], error) {
	if q.err != nil {
		return CursorPage[T]{}, q.err
	}

	perPage = q.perPage(perPage)

	keys := q.keys
	if !keysHave(keys, q.m.pk) {
		keys = append(keys, orderKey{q.m.pk, dir})
		q = q.OrderBy(q.m.pk, dir)
	}

	if cursor != "" {
		last, err := q.decodeCursor(cursor, keys)
		if err != nil {
			return CursorPage[T]{}, ErrInvalidCursor
		}

		q = q.after(keys, last)
	}

	items, err := q.Limit(perPage + 1).Get(ctx)
	if err != nil {
		return CursorPage[T]{}, err
	}

	page := CursorPage[T]{PerPage: perPage, Items: items}

	if len(items) > perPage {
		page.Items = items[:perPage]

		if page.NextCursor, err = q.encodeCursor(&page.Items[perPage-1], keys); err != nil {
			return CursorPage[T]{}, err
		}
	}

	return page, nil
}

func keysHave(keys []orderKey, col string) bool {
	for _, k := range keys {
		if k.col == col {
			return true
		}
	}

	return false
}

// after adds the keyset predicate for rows past last:
// (k1 > v1) OR (k1 = v1 AND k2 > v2) OR ..., with < for descending keys.
// The expanded form, unlike a row comparison, allows mixed directions on
// every dialect.
func (q Query[T]) after(keys []orderKey, last []any) Query[T] {
	var (
		groups []string
		args   []any
	)

	for i, k := range keys {
		parts := make([]string, 0, i+1)
		for j := range i {
			parts = append(parts, q.quote(keys[j].col)+" = ?")
			args = append(args, last[j])
		}

		op := ">"
		if k.dir == Desc {
			op = "<"
		}

		parts = append(parts, q.quote(k.col)+" "+op+" ?")
		args = append(args, last[i])
		groups = append(groups, "("+strings.Join(parts, " AND ")+")")
	}

	return q.addCond(false, "("+strings.Join(groups, " OR ")+")", args...)
}

// Cursors are opaque to clients but not secret: they only carry the last
// row's key values, decoded into the columns' Go types and bound as
// parameters, so a forged cursor can only pick another starting row. A
// primary-key-only cursor is the bare value (the format before composite
// keys), a composite one a JSON array.
func (q Query[T]) encodeCursor(row *T, keys []orderKey) (string, error) {
	v := reflect.ValueOf(row).Elem()

	vals := make([]any, len(keys))
	for i, k := range keys {
		vals[i] = v.FieldByIndex(q.m.byColumn[k.col].index).Interface()
	}

	var payload any = vals
	if len(vals) == 1 {
		payload = vals[0]
	}

	b, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}

	return base64.RawURLEncoding.EncodeToString(b), nil
}

func (q Query[T]) decodeCursor(s string, keys []orderKey) ([]any, error) {
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return nil, err
	}

	raws := []json.RawMessage{b}
	if len(keys) > 1 {
		if err := json.Unmarshal(b, &raws); err != nil {
			return nil, err
		}

		if len(raws) != len(keys) {
			return nil, errInvalidCursorShape
		}
	}

	out := make([]any, len(keys))

	for i, k := range keys {
		ptr := reflect.New(q.m.byColumn[k.col].typ)
		if err := json.Unmarshal(raws[i], ptr.Interface()); err != nil {
			return nil, err
		}

		out[i] = ptr.Elem().Interface()
	}

	return out, nil
}
