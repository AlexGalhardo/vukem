package dbtest

import (
	"context"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/AlexGalhardo/vukem/database"
	"github.com/AlexGalhardo/vukem/schema"
)

// testInspect proves the catalog queries of schema.Inspect on the real
// engine: every column method, an index, a partial index (not MySQL), a
// foreign key with ON DELETE CASCADE and a composite primary key.
func testInspect(ctx context.Context, t *testing.T, db *database.DB) {
	t.Helper()

	fam := database.MustCapabilities(db.Dialect()).Family
	s := schema.New(ctx, db)

	mustOK(t, "create owners", s.Create("dbt_inspect_owners", func(t *schema.Table) {
		t.ID()
		t.String("name", 40)
	}))
	mustOK(t, "create table", s.Create("dbt_inspect", func(t *schema.Table) {
		t.ID()
		t.String("label", 40).Unique()
		t.Text("body").Nullable()
		t.Integer("count").Default(0)
		t.SmallInteger("score").Nullable()
		t.BigInteger("big")
		t.Boolean("flag").Default(false)
		t.Decimal("price", 10, 2)
		t.Float("ratio").Nullable()
		t.Date("day").Nullable()
		t.Timestamp("at").Nullable()
		t.JSON("doc").Nullable()
		t.UUID("ref").Nullable()
		t.ULID("code").Nullable()
		t.Binary("blob").Nullable()
		t.Enum("state", "a", "b").Default("a")
		t.ForeignID("owner_id").Constrained("dbt_inspect_owners").CascadeOnDelete()
		t.Index("count", "big")

		if fam != "mysql" {
			t.UniqueIndex("big", "score").Where("flag")
		}
	}))
	mustOK(t, "create memberships", s.Create("dbt_inspect_members", func(t *schema.Table) {
		t.ForeignID("user_id")
		t.ForeignID("group_id")
		t.Primary("user_id", "group_id")
	}))

	t.Cleanup(func() {
		for _, name := range []string{"dbt_inspect_members", "dbt_inspect", "dbt_inspect_owners"} {
			_ = s.DropIfExists(name)
		}
	})

	tbl, ok, err := schema.InspectTable(ctx, db, "dbt_inspect")
	if err != nil || !ok {
		t.Fatalf("InspectTable = %v, %v", ok, err)
	}

	want := map[string]map[string]string{
		"sqlite": {
			"id": "ID", "label": "String", "body": "Text", "count": "BigInteger", "score": "SmallInteger",
			"big": "BigInteger", "flag": "Boolean", "price": "Decimal", "ratio": "Float", "day": "Date",
			"at": "Timestamp", "doc": "Text", "ref": "Text", "code": "ULID", "blob": "Binary", "state": "Text",
			"owner_id": "BigInteger",
		},
		"postgres": {
			"id": "ID", "label": "String", "body": "Text", "count": "Integer", "score": "SmallInteger",
			"big": "BigInteger", "flag": "Boolean", "price": "Decimal", "ratio": "Float", "day": "Date",
			"at": "Timestamp", "doc": "JSON", "ref": "UUID", "code": "ULID", "blob": "Binary", "state": "Text",
			"owner_id": "BigInteger",
		},
		"mysql": {
			"id": "ID", "label": "String", "body": "Text", "count": "Integer", "score": "SmallInteger",
			"big": "BigInteger", "flag": "Boolean", "price": "Decimal", "ratio": "Float", "day": "Date",
			"at": "Timestamp", "doc": "JSON", "ref": "UUID", "code": "ULID", "blob": "Binary", "state": "String",
			"owner_id": "BigInteger",
		},
	}[fam]

	got := map[string]string{}
	for _, c := range tbl.Columns {
		got[c.Name] = c.Kind
	}

	// MariaDB stores JSON as LONGTEXT, which no builder method can tell apart.
	if fam == "mysql" && got["doc"] == "Text" {
		got["doc"] = "JSON"
	}

	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("column kinds on %s (-want +got):\n%s", fam, diff)
	}

	if len(tbl.PrimaryKey) != 1 || tbl.PrimaryKey[0] != "id" {
		t.Errorf("primary key = %v, want [id]", tbl.PrimaryKey)
	}

	byName := map[string]schema.ColumnInfo{}
	for _, c := range tbl.Columns {
		byName[c.Name] = c
	}

	if c := byName["label"]; c.Length != 40 || c.Nullable {
		t.Errorf("label = %+v, want length 40 NOT NULL", c)
	}

	if c := byName["price"]; c.Precision != 10 || c.Scale != 2 {
		t.Errorf("price = %+v, want precision 10 scale 2", c)
	}

	if len(tbl.ForeignKeys) != 1 || tbl.ForeignKeys[0].RefTable != "dbt_inspect_owners" ||
		tbl.ForeignKeys[0].OnDelete != "CASCADE" || len(tbl.ForeignKeys[0].Columns) != 1 ||
		tbl.ForeignKeys[0].Columns[0] != "owner_id" {
		t.Errorf("foreign keys = %+v, want owner_id -> dbt_inspect_owners CASCADE", tbl.ForeignKeys)
	}

	idx := map[string]schema.IndexInfo{}
	for _, i := range tbl.Indexes {
		idx[i.Name] = i
	}

	if i, ok := idx["dbt_inspect_count_big_index"]; !ok || i.Unique || len(i.Columns) != 2 {
		t.Errorf("index count,big = %+v (found %v)", i, ok)
	}

	if fam != "mysql" {
		i, ok := idx["dbt_inspect_big_score_unique"]
		if !ok || !i.Unique || i.Where == "" || len(i.Columns) != 2 {
			t.Errorf("partial unique index = %+v (found %v)", i, ok)
		}
	}

	// MariaDB has no functional indexes; the other two do.
	if fam == "sqlite" || fam == "postgres" {
		_, err := db.ExecContext(ctx, "CREATE INDEX dbt_inspect_expr ON dbt_inspect (lower(label), big)")
		mustOK(t, "expression index", err)

		withExpr, _, err := schema.InspectTable(ctx, db, "dbt_inspect")
		mustOK(t, "inspect expression index", err)

		var found bool

		for _, ix := range withExpr.Indexes {
			if ix.Name == "dbt_inspect_expr" {
				found = true

				if !ix.Expression {
					t.Errorf("an index on an expression must report Expression: %+v", ix)
				}

				for _, c := range ix.Columns {
					if c == "" {
						t.Errorf("an expression must not appear as an empty column: %+v", ix)
					}
				}
			}
		}

		if !found {
			t.Error("the expression index is not listed")
		}
	}

	members, _, err := schema.InspectTable(ctx, db, "dbt_inspect_members")
	mustOK(t, "inspect composite", err)

	if len(members.PrimaryKey) != 2 || members.PrimaryKey[0] != "user_id" || members.PrimaryKey[1] != "group_id" {
		t.Errorf("composite primary key = %v", members.PrimaryKey)
	}

	snap, err := schema.Inspect(ctx, db)
	mustOK(t, "inspect all", err)

	if _, ok := snap.Table("dbt_inspect"); !ok {
		t.Error("Inspect does not list dbt_inspect")
	}

	if has, err := s.HasColumn("dbt_inspect", "owner_id"); err != nil || !has {
		t.Errorf("HasColumn = %v, %v", has, err)
	}
}
