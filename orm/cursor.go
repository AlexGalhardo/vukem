package orm

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"

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

// CursorPaginate returns perPage rows after cursor, ordered by the primary
// key in dir. Unlike Paginate it needs no COUNT and stays fast and stable
// on large or changing tables. Pass "" for the first page and the
// returned NextCursor for the following ones. The query must not have
// its own OrderBy: the keyset order is the primary key. perPage is
// clamped to 1..DefaultMaxPerPage (see MaxPerPage).
func (q Query[T]) CursorPaginate(
	ctx context.Context,
	cursor string,
	perPage int,
	dir Direction,
) (CursorPage[T], error) {
	perPage = q.perPage(perPage)

	if len(q.orders) > 0 {
		return CursorPage[T]{}, errors.New("orm: CursorPaginate orders by the primary key; remove OrderBy")
	}

	op := ">"
	if dir == Desc {
		op = "<"
	}

	if cursor != "" {
		last, err := decodeCursor(cursor)
		if err != nil {
			return CursorPage[T]{}, ErrInvalidCursor
		}

		q = q.Where(q.m.pk, op, last)
	}

	items, err := q.OrderBy(q.m.pk, dir).Limit(perPage + 1).Get(ctx)
	if err != nil {
		return CursorPage[T]{}, err
	}

	page := CursorPage[T]{PerPage: perPage, Items: items}

	if len(items) > perPage {
		page.Items = items[:perPage]

		pk := reflect.ValueOf(&page.Items[perPage-1]).Elem().FieldByIndex(q.m.byColumn[q.m.pk].index).Interface()
		if page.NextCursor, err = encodeCursor(pk); err != nil {
			return CursorPage[T]{}, err
		}
	}

	return page, nil
}

// Cursors are opaque to clients but not secret: they only carry the last
// primary key, which is bound as a parameter.
func encodeCursor(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}

	return base64.RawURLEncoding.EncodeToString(b), nil
}

func decodeCursor(s string) (any, error) {
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return nil, err
	}

	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		return nil, err
	}

	switch v.(type) {
	case float64, string:
		return v, nil
	default:
		return nil, fmt.Errorf("orm: cursor value %T", v)
	}
}
