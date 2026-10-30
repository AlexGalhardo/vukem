package admin_test

import (
	"strings"
	"testing"

	"github.com/AlexGalhardo/vukem/admin"
	"github.com/AlexGalhardo/vukem/authz"
	"github.com/AlexGalhardo/vukem/orm"
	"github.com/AlexGalhardo/vukem/z"
)

// pairRow is keyed by (user_id, item_id), the VF-047 case: a detail URL
// must not resolve by user_id alone.
type pairRow struct {
	UserID int64
	ItemID int64
	Note   string `json:"note"`
}

func (pairRow) Table() string         { return "pairs" }
func (pairRow) PrimaryKeys() []string { return []string{"user_id", "item_id"} }

type pairInput struct {
	Note string `form:"note"`
}

type pairKeyInput struct {
	UserID int64  `form:"user_id"`
	Note   string `form:"note"`
}

// tagRow has string keys that themselves contain "-" and "~".
type tagRow struct {
	Scope string
	Name  string
	Label string `json:"label"`
}

func (tagRow) Table() string         { return "tag_pairs" }
func (tagRow) PrimaryKeys() []string { return []string{"scope", "name"} }

type tagInput struct {
	Label string `form:"label"`
}

func TestCompositeKeyURLMatchesEveryColumn(t *testing.T) {
	t.Parallel()

	// A schema key for one column of the composite key is still a boot error.
	bad := admin.NewResource[pairRow](z.Struct[pairKeyInput](z.Shape{
		"user_id": z.Int64(),
		"note":    z.String(),
	}))
	if err := newPanel(t).Register(bad); err == nil || !strings.Contains(err.Error(), "user_id") {
		t.Fatalf("Register(key column) = %v, want an error naming user_id", err)
	}

	app := newApp(t, options{
		resource: func() []admin.AnyResource {
			return []admin.AnyResource{
				admin.NewResource[pairRow](z.Struct[pairInput](z.Shape{"note": z.String()})),
				admin.NewResource[tagRow](z.Struct[tagInput](z.Shape{"label": z.String()})),
			}
		},
		extra: func(g *authz.Gate[*User]) error {
			return g.Register(
				authz.For[*pairRow](adminPolicy[*pairRow]{}),
				authz.For[*tagRow](adminPolicy[*tagRow]{}),
			)
		},
	})

	db, err := app.DB()
	if err != nil {
		t.Fatal(err)
	}

	for _, row := range []pairRow{{1, 10, "row-1-10"}, {1, 20, "row-1-20"}, {2, 10, "row-2-10"}} {
		if err := orm.Create(t.Context(), db, &row); err != nil {
			t.Fatal(err)
		}
	}

	for _, row := range []tagRow{
		{"a-b", "c", "left-side"},
		{"a", "b-c", "right-side"},
		{"a~b", "c", "tilde-row"},
	} {
		if err := orm.Create(t.Context(), db, &row); err != nil {
			t.Fatal(err)
		}
	}

	c := as(t, app, "admin")

	// Same first key, different second key: each URL shows only its row.
	c.Get("/admin/pairs").AssertOK().AssertSee("/admin/pairs/1-10").AssertSee("/admin/pairs/1-20")
	c.Get("/admin/pairs/1-10").AssertOK().AssertSee("row-1-10").AssertDontSee("row-1-20").AssertDontSee("row-2-10")
	c.Get("/admin/pairs/1-20").AssertOK().AssertSee("row-1-20").AssertDontSee("row-1-10")
	c.Get("/admin/pairs/2-10").AssertOK().AssertSee("row-2-10").AssertDontSee("row-1-10")

	// The first column alone, or an extra segment, is not a row.
	c.Get("/admin/pairs/1").AssertNotFound()
	c.Get("/admin/pairs/1-10-99").AssertNotFound()

	// "-" and "~" inside a value stay in that value, so the two splits do not collide.
	c.Get("/admin/tag-pairs").AssertOK().AssertSee("/admin/tag-pairs/a~b-c").AssertSee("/admin/tag-pairs/a-b~c")
	c.Get("/admin/tag-pairs/a~b-c").
		AssertOK().
		AssertSee("left-side").
		AssertDontSee("right-side").
		AssertDontSee("tilde-row")
	c.Get("/admin/tag-pairs/a-b~c").AssertOK().AssertSee("right-side").AssertDontSee("left-side")
	c.Get("/admin/tag-pairs/a~~b-c").AssertOK().AssertSee("tilde-row").AssertDontSee("left-side")
	c.Get("/admin/tag-pairs/a").AssertNotFound()
	c.Get("/admin/tag-pairs/a-b-c").AssertNotFound()
}
