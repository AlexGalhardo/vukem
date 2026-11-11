package studio

import (
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestSchemaCatalog(t *testing.T) {
	t.Parallel()

	h := newSQLHarness(t)

	for _, q := range []string{
		`CREATE TABLE owners (id INTEGER PRIMARY KEY, name TEXT NOT NULL DEFAULT 'x')`,
		`CREATE TABLE pets (id INTEGER PRIMARY KEY, owner_id INTEGER REFERENCES owners (id), tag TEXT)`,
		`CREATE INDEX pets_lower_tag ON pets (lower(tag))`,
	} {
		if _, err := h.db.ExecContext(t.Context(), q); err != nil {
			t.Fatal(err)
		}
	}

	cat := schemaCatalog{h.db}

	got, ok, err := cat.Table(t.Context(), "pets")
	if err != nil || !ok {
		t.Fatalf("Table(pets) = %v, %v, %v", got, ok, err)
	}

	want := Table{
		Name: "pets", PrimaryKey: []string{"id"},
		Columns: []Column{
			{Name: "id", Type: "integer"},
			{Name: "owner_id", Type: "integer", Nullable: true},
			{Name: "tag", Type: "text", Nullable: true},
		},
		ForeignKeys: []ForeignKey{{Columns: []string{"owner_id"}, RefTable: "owners", RefColumns: []string{"id"}}},
	}

	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("Table(pets) (-want +got):\n%s", diff)
	}

	tables, err := cat.Tables(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	var names []string
	for _, tb := range tables {
		names = append(names, tb.Name)
	}

	if diff := cmp.Diff([]string{"log", "owners", "pairs", "pets", "users"}, names); diff != "" {
		t.Errorf("Tables (-want +got):\n%s", diff)
	}

	if _, ok, err := cat.Table(t.Context(), "nope"); ok || err != nil {
		t.Errorf("Table(nope) = %v, %v", ok, err)
	}

	if _, ok, err := cat.Table(t.Context(), "pets; DROP TABLE users"); ok || err != nil {
		t.Errorf("a hostile name = %v, %v; want not found and no error", ok, err)
	}
}
