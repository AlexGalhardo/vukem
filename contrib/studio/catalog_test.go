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
		Indexes: []Index{
			{Columns: []string{"id"}, Unique: true, Primary: true},
			{Name: "pets_lower_tag", Expression: true},
		},
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

func TestCatalogIndexes(t *testing.T) {
	t.Parallel()

	h := newSQLHarness(t)

	for _, q := range []string{
		`CREATE TABLE members (id INTEGER PRIMARY KEY, org_id INTEGER NOT NULL, email TEXT, role TEXT, bio TEXT)`,
		`CREATE UNIQUE INDEX members_email ON members (email)`,
		`CREATE INDEX members_org_role ON members (org_id, role)`,
		`CREATE INDEX members_lower_bio ON members (lower(bio))`,
		`CREATE TABLE pairs2 (a INTEGER, b INTEGER, PRIMARY KEY (a, b))`,
		`CREATE TABLE plain (x TEXT)`,
	} {
		if _, err := h.db.ExecContext(t.Context(), q); err != nil {
			t.Fatal(err)
		}
	}

	cat := schemaCatalog{h.db}

	got, ok, err := cat.Table(t.Context(), "members")
	if err != nil || !ok {
		t.Fatalf("Table(members) = %v, %v", ok, err)
	}

	want := []Index{
		{Columns: []string{"id"}, Unique: true, Primary: true},
		{Name: "members_email", Columns: []string{"email"}, Unique: true},
		{Name: "members_lower_bio", Expression: true},
		{Name: "members_org_role", Columns: []string{"org_id", "role"}},
	}

	if len(got.Indexes) != len(want) {
		t.Fatalf("members indexes = %+v, want %d", got.Indexes, len(want))
	}

	// The primary key comes first, then the rest as the catalog sorts them.
	if diff := cmp.Diff(want[0], got.Indexes[0]); diff != "" {
		t.Errorf("primary index (-want +got):\n%s", diff)
	}

	if diff := cmp.Diff(want[1], got.Indexes[1]); diff != "" {
		t.Errorf("unique index (-want +got):\n%s", diff)
	}

	if diff := cmp.Diff(want[3], got.Indexes[3]); diff != "" {
		t.Errorf("composite index keeps its column order (-want +got):\n%s", diff)
	}

	if !got.Indexes[2].Expression {
		t.Errorf("an expression index = %+v, want Expression set", got.Indexes[2])
	}

	pair, _, _ := cat.Table(t.Context(), "pairs2")
	if diff := cmp.Diff([]Index{{Columns: []string{"a", "b"}, Unique: true, Primary: true}}, pair.Indexes); diff != "" {
		t.Errorf("composite primary key (-want +got):\n%s", diff)
	}

	plain, _, _ := cat.Table(t.Context(), "plain")
	if len(plain.Indexes) != 0 {
		t.Errorf("a table with no key has indexes %+v", plain.Indexes)
	}
}
