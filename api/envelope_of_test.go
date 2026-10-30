package api

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/AlexGalhardo/vukem/orm"
	"github.com/AlexGalhardo/vukem/web"
)

// camelPage is an app envelope with camelCase meta keys.
type camelPage[R any] struct {
	Items      []R     `json:"items"`
	Page       int     `json:"page,omitempty"`
	PerPage    int     `json:"perPage"`
	Total      *int64  `json:"total,omitempty"`
	NextCursor string  `json:"nextCursor,omitempty"`
	Next       *string `json:"next"`
}

func camel[R any](data []R, m *Meta, l *Links) camelPage[R] {
	out := camelPage[R]{Items: data}
	if m != nil {
		out.Page, out.PerPage, out.Total, out.NextCursor = m.Page, m.PerPage, m.Total, m.NextCursor
	}

	if l != nil {
		out.Next = l.Next
	}

	return out
}

func TestEnvelopeOf(t *testing.T) {
	t.Parallel()

	db := setup(t)

	_, page := call(t, "/products?page=2", func(c *web.Ctx) error {
		req, err := Query(c, orm.QueryOf[Product](db), Sorts("id"), PerPage(2, 3))
		if err != nil {
			return err
		}

		p, err := req.Paginate(c.Context())
		if err != nil {
			return err
		}

		return c.JSON(200, PageOf(c, p, toJSON, camel))
	})

	want := map[string]any{
		"items": []any{
			map[string]any{"id": 3.0, "name": "Lamp", "price": 40.0},
			map[string]any{"id": 4.0, "name": "Book", "price": 15.0},
		},
		"page": 2.0, "perPage": 2.0, "total": 5.0, "next": "/products?page=3",
	}
	if diff := cmp.Diff(want, page); diff != "" {
		t.Errorf("PageOf (-want +got):\n%s", diff)
	}

	_, cursor := call(t, "/p", func(c *web.Ctx) error {
		req, err := Query(c, orm.QueryOf[Product](db), PerPage(2, 10))
		if err != nil {
			return err
		}

		p, err := req.Query.CursorPaginate(c.Context(), req.Cursor, req.PerPage, orm.Asc)
		if err != nil {
			return err
		}

		return c.JSON(200, CursorOf(c, p, toJSON, camel))
	})
	if cursor["nextCursor"] == "" || cursor["perPage"] != 2.0 || cursor["next"] == nil ||
		len(cursor["items"].([]any)) != 2 {
		t.Errorf("CursorOf = %v", cursor)
	}

	_, coll := call(t, "/p", func(c *web.Ctx) error {
		ps, err := orm.QueryOf[Product](db).Where("price", ">", 100).Get(c.Context())
		if err != nil {
			return err
		}

		return c.JSON(200, CollectionOf(ps, toJSON, camel))
	})
	if diff := cmp.Diff([]string{"Desk", "Chair"}, itemNames(coll)); diff != "" || coll["next"] != nil {
		t.Errorf("CollectionOf = %v", coll)
	}
}

func itemNames(body map[string]any) []string {
	return names(map[string]any{"data": body["items"]})
}
