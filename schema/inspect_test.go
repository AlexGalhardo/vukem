package schema

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/AlexGalhardo/vukem/database"
)

func inspectFixture(t *testing.T) *database.DB {
	t.Helper()

	db, err := database.Open(t.Context(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = db.Close() })

	s := New(t.Context(), db)

	must := func(err error) {
		t.Helper()

		if err != nil {
			t.Fatal(err)
		}
	}

	must(s.Create("owners", func(t *Table) {
		t.ID()
		t.String("name", 40)
	}))
	must(s.Create("everything", func(t *Table) {
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
		t.ForeignID("owner_id").Constrained("owners").CascadeOnDelete()
		t.Index("count", "big")
		t.UniqueIndex("big", "score").Where("flag")
	}))
	must(s.Create("memberships", func(t *Table) {
		t.ForeignID("user_id")
		t.ForeignID("group_id")
		t.Primary("user_id", "group_id")
	}))

	return db
}

func TestInspect(t *testing.T) {
	t.Parallel()

	db := inspectFixture(t)

	snap, err := Inspect(t.Context(), db)
	if err != nil {
		t.Fatal(err)
	}

	var names []string
	for _, tb := range snap.Tables {
		names = append(names, tb.Name)
	}

	if diff := cmp.Diff([]string{"everything", "memberships", "owners"}, names); diff != "" {
		t.Errorf("tables (-want +got):\n%s", diff)
	}

	all, _ := snap.Table("everything")

	// SQLite keeps no difference between Integer and BigInteger or between
	// Text, JSON, UUID and Enum, so those map to the common builder method.
	wantKinds := map[string]string{
		"id": "ID", "label": "String", "body": "Text", "count": "BigInteger", "score": "SmallInteger",
		"big": "BigInteger", "flag": "Boolean", "price": "Decimal", "ratio": "Float", "day": "Date",
		"at": "Timestamp", "doc": "Text", "ref": "Text", "code": "ULID", "blob": "Binary", "state": "Text",
		"owner_id": "BigInteger",
	}

	got := map[string]string{}
	for _, c := range all.Columns {
		got[c.Name] = c.Kind
	}

	if diff := cmp.Diff(wantKinds, got); diff != "" {
		t.Errorf("column kinds (-want +got):\n%s", diff)
	}

	byName := map[string]ColumnInfo{}
	for _, c := range all.Columns {
		byName[c.Name] = c
	}

	if c := byName["label"]; c.Length != 40 || c.Nullable || c.Type != "varchar(40)" {
		t.Errorf("label = %+v, want varchar(40) NOT NULL", c)
	}

	if c := byName["price"]; c.Precision != 10 || c.Scale != 2 {
		t.Errorf("price = %+v, want precision 10 scale 2", c)
	}

	if c := byName["body"]; !c.Nullable {
		t.Error("body should be nullable")
	}

	if c := byName["count"]; !c.Default.Valid || c.Default.V != "0" {
		t.Errorf("count default = %+v, want 0", c.Default)
	}

	if !cmp.Equal(all.PrimaryKey, []string{"id"}) {
		t.Errorf("primary key = %v", all.PrimaryKey)
	}

	wantIdx := []IndexInfo{
		{Name: "", Columns: []string{"label"}, Unique: true},
		{Name: "everything_big_score_unique", Columns: []string{"big", "score"}, Unique: true, Where: "flag"},
		{Name: "everything_count_big_index", Columns: []string{"count", "big"}},
	}
	if diff := cmp.Diff(wantIdx, all.Indexes); diff != "" {
		t.Errorf("indexes (-want +got):\n%s", diff)
	}

	wantFK := []ForeignKeyInfo{
		{Columns: []string{"owner_id"}, RefTable: "owners", RefColumns: []string{"id"}, OnDelete: "CASCADE"},
	}
	if diff := cmp.Diff(wantFK, all.ForeignKeys); diff != "" {
		t.Errorf("foreign keys (-want +got):\n%s", diff)
	}

	m, _ := snap.Table("memberships")
	if !cmp.Equal(m.PrimaryKey, []string{"user_id", "group_id"}) {
		t.Errorf("composite primary key = %v", m.PrimaryKey)
	}

	if _, ok, err := InspectTable(t.Context(), db, "missing"); ok || err != nil {
		t.Errorf("InspectTable(missing) = %v, %v", ok, err)
	}

	if _, _, err := InspectTable(t.Context(), db, "bad name; DROP"); err == nil {
		t.Error("InspectTable accepted an invalid table name")
	}

	if has, err := New(t.Context(), db).HasColumn("everything", "doc"); err != nil || !has {
		t.Errorf("HasColumn = %v, %v", has, err)
	}
}

func TestInspectSQLiteExpressionIndexTriggersAndCheck(t *testing.T) {
	t.Parallel()

	db := rebuildDB(t)
	execAll(t, db,
		"CREATE TABLE notes (id INTEGER PRIMARY KEY, body TEXT CHECK (length(body) > 1))",
		"CREATE INDEX notes_lower ON notes (lower(body), id)",
		"CREATE TRIGGER notes_t AFTER INSERT ON notes BEGIN SELECT 1; END",
	)

	tbl, ok, err := InspectTable(t.Context(), db, "notes")
	if err != nil || !ok {
		t.Fatalf("InspectTable = %v, %v", ok, err)
	}

	if len(tbl.Indexes) != 1 || !tbl.Indexes[0].Expression || !cmp.Equal(tbl.Indexes[0].Columns, []string{"id"}) {
		t.Errorf("indexes = %+v, want one expression index with the plain column id", tbl.Indexes)
	}

	if tbl.SQLite == nil || !tbl.SQLite.HasCheck || !cmp.Equal(tbl.SQLite.Triggers, []string{"notes_t"}) {
		t.Errorf("SQLite = %+v, want a CHECK and the trigger notes_t", tbl.SQLite)
	}
}
