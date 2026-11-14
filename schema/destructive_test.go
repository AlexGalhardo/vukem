package schema

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/AlexGalhardo/vukem/database"
)

func TestDestructive(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		up   func(s *Schema) error
		want []string
	}{
		{"create is safe", createPosts, nil},
		{"drop table", func(s *Schema) error { return s.Drop("posts") }, []string{"drop table posts"}},
		{"drop if exists", func(s *Schema) error { return s.DropIfExists("posts") }, []string{"drop table posts"}},
		{"drop column", func(s *Schema) error {
			return s.Table("posts", func(t *Table) { t.DropColumn("title") })
		}, []string{`alter table "posts" drop column "title"`}},
		{"raw drop table", func(s *Schema) error { return s.Raw("DROP TABLE legacy") }, []string{"drop table legacy"}},
		{"drop table with a schema", func(s *Schema) error {
			return s.Raw(`DROP TABLE IF EXISTS "app"."legacy"`)
		}, []string{"drop table app.legacy"}},
		{
			"drop several tables",
			func(s *Schema) error { return s.Raw("DROP TABLE a, b") },
			[]string{"drop table a", "drop table b"},
		},
		{"drop column inside a combined alter", func(s *Schema) error {
			return s.Raw("ALTER TABLE posts ADD slug INT, DROP COLUMN title")
		}, []string{"alter table posts add slug int, drop column title"}},
		{"drop constraint is not a drop column", func(s *Schema) error {
			return s.Raw("ALTER TABLE posts DROP CONSTRAINT posts_title_key")
		}, nil},
		{"drop default is not a drop column", func(s *Schema) error {
			return s.Raw(`ALTER TABLE posts ALTER COLUMN title DROP DEFAULT`)
		}, nil},
		{"a rebuild that keeps every column is not a drop", func(s *Schema) error {
			return s.Rebuild("posts", func(t *Table) {
				t.ID()
				t.String("title")
				t.String("slug").Nullable()
			})
		}, nil},
		{"a rebuild that renames a column is not a drop", func(s *Schema) error {
			return s.Rebuild("posts", func(t *Table) {
				t.ID()
				t.String("headline")
			}, RenamedFrom("title", "headline"))
		}, nil},
		{"a rebuild that leaves a column out", func(s *Schema) error {
			return s.Rebuild("posts", func(t *Table) { t.ID() })
		}, []string{"rebuild posts drops column title"}},
	}

	db, err := database.Open(t.Context(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = db.Close() })

	if err := createPosts(New(t.Context(), db)); err != nil {
		t.Fatal(err)
	}

	for _, tt := range tests {
		got, err := Destructive(t.Context(), db, Migration{ID: "1", Up: tt.up})
		if err != nil || !cmp.Equal(got, tt.want) {
			t.Errorf("%s: Destructive = %q, %v; want %q", tt.name, got, err, tt.want)
		}
	}
}

func TestDropsColumn(t *testing.T) {
	t.Parallel()

	tests := []struct {
		stmt string
		want bool
	}{
		{"ALTER TABLE t DROP COLUMN c", true},
		{"ALTER TABLE t DROP COLUMN IF EXISTS c", true},
		{`ALTER TABLE "t" DROP "c"`, true},
		{"ALTER TABLE t DROP c", true},
		{"ALTER TABLE t DROP IF EXISTS c", true},
		{"alter table t add slug int, drop column title", true},
		{"ALTER TABLE t DROP CONSTRAINT c, DROP COLUMN x", true},
		{"ALTER TABLE t DROP COLUMN x, DROP CONSTRAINT c", true},
		{"ALTER TABLE t DROP CONSTRAINT c", false},
		{"ALTER TABLE t DROP CONSTRAINT IF EXISTS c CASCADE", false},
		{`ALTER TABLE "t" ALTER COLUMN "c" DROP DEFAULT`, false},
		{`ALTER TABLE "t" ALTER COLUMN "c" DROP NOT NULL`, false},
		{"ALTER TABLE t ALTER COLUMN c DROP IDENTITY IF EXISTS", false},
		{"ALTER TABLE t ALTER COLUMN c DROP EXPRESSION", false},
		{"ALTER TABLE t DROP INDEX i", false},
		{"ALTER TABLE t DROP KEY i", false},
		{"ALTER TABLE t DROP PRIMARY KEY", false},
		{"ALTER TABLE t DROP FOREIGN KEY fk", false},
		{"ALTER TABLE t DROP CHECK ck", false},
		{"ALTER TABLE t DROP CONSTRAINT a, ALTER COLUMN c DROP DEFAULT", false},
		{"DROP INDEX i", false},
	}

	for _, tt := range tests {
		if got := dropsColumn(tt.stmt); got != tt.want {
			t.Errorf("dropsColumn(%q) = %v; want %v", tt.stmt, got, tt.want)
		}
	}
}

func TestStatusMarksOutOfOrderMigrations(t *testing.T) {
	t.Parallel()

	db, err := database.Open(t.Context(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = db.Close() })

	first := mig("20260101_a", createPosts)
	late := mig("20260301_c", func(s *Schema) error { return s.Create("c", func(t *Table) { t.ID() }) })

	m, err := NewMigrator(db, []Migration{first, late})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := m.Up(t.Context()); err != nil {
		t.Fatal(err)
	}

	// Another branch's migration, older than the last one that ran.
	other := mig("20260201_b", func(s *Schema) error { return s.Create("b", func(t *Table) { t.ID() }) })

	m2, err := NewMigrator(db, []Migration{first, other, late})
	if err != nil {
		t.Fatal(err)
	}

	st, err := m2.Status(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	got := map[string]bool{}
	for _, s := range st {
		got[s.ID] = s.OutOfOrder
	}

	if diff := cmp.Diff(
		map[string]bool{"20260101_a": false, "20260201_b": true, "20260301_c": false},
		got,
	); diff != "" {
		t.Errorf("out of order (-want +got):\n%s", diff)
	}

	pending, err := m2.Pending(t.Context())
	if err != nil || len(pending) != 1 || pending[0].ID != "20260201_b" {
		t.Errorf("Pending = %v, %v", pending, err)
	}
}
