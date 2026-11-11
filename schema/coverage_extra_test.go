package schema

import (
	"errors"
	"strings"
	"testing"

	"github.com/AlexGalhardo/vukem/database"
)

func TestMarkAppliedRecordsWithoutRunning(t *testing.T) {
	t.Parallel()

	db := openDB(t)

	ran := false
	extra := Migration{ID: "20260103000000_marker", Up: func(s *Schema) error {
		ran = ran || s.record == nil // recording the checksum does not run it
		return s.Create("marker", func(t *Table) { t.ID() })
	}}

	m, err := NewMigrator(db, migrations(extra))
	if err != nil {
		t.Fatal(err)
	}

	ctx := t.Context()

	if err := m.MarkApplied(ctx, "nope"); !errors.Is(err, ErrUnknownMigration) {
		t.Fatalf("MarkApplied(unknown) = %v, want ErrUnknownMigration", err)
	}

	if err := m.MarkApplied(ctx, extra.ID); err != nil {
		t.Fatal(err)
	}

	if ran {
		t.Error("MarkApplied ran the migration")
	}

	if has, _ := New(ctx, db).HasTable("marker"); has {
		t.Error("MarkApplied created the table")
	}

	// A second call is a no-op: one record, not two.
	if err := m.MarkApplied(ctx, extra.ID); err != nil {
		t.Fatalf("second MarkApplied = %v", err)
	}

	var n int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM migrations WHERE id = ?", extra.ID).
		Scan(&n); err != nil ||
		n != 1 {
		t.Errorf("records = %d, %v; want 1", n, err)
	}

	pending, err := m.Pending(ctx)
	if err != nil || len(pending) != 2 {
		t.Fatalf("Pending = %v, %v; want the two others", pending, err)
	}

	// The checksum was stored, so a later edit of the migration is noticed.
	st, err := m.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}

	for _, s := range st {
		if s.ID == extra.ID && (!s.Ran || s.Batch != 1) {
			t.Errorf("status of the marked migration = %+v", s)
		}
	}

	// Migrate then runs the others in the next batch.
	if _, err := m.Up(ctx); err != nil {
		t.Fatal(err)
	}

	if ran {
		t.Error("Up ran a migration that was marked as applied")
	}
}

func TestMarkAppliedOnClosedDB(t *testing.T) {
	t.Parallel()

	db := openDB(t)

	m, err := NewMigrator(db, migrations())
	if err != nil {
		t.Fatal(err)
	}

	_ = db.Close()

	if err := m.MarkApplied(t.Context(), "20260101000000_create_users"); err == nil {
		t.Error("MarkApplied on a closed database did not fail")
	}
}

func TestPartialErrorUnwraps(t *testing.T) {
	t.Parallel()

	cause := errors.New("boom")
	err := &partialError{id: "1", executed: 2, err: cause}

	if !errors.Is(err, cause) || !strings.Contains(err.Error(), "failed after 2 statement(s)") {
		t.Errorf("partialError = %v", err)
	}
}

func TestFindConflictsTableLevelCases(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		migs []Migration
		want string // a substring of the single conflict, "" for none
	}{
		{"rename a table onto an existing one", []Migration{
			mig("1", createPosts),
			mig("2", func(s *Schema) error { return s.Create("articles", func(t *Table) { t.ID() }) }),
			mig("3", func(s *Schema) error { return s.Rename("posts", "articles") }),
		}, "table already created by 2"},
		{"rename a missing table", []Migration{
			mig("1", func(s *Schema) error { return s.Rename("ghost", "x") }),
		}, "table does not exist at this point"},
		{"rebuild a missing table", []Migration{
			mig("1", func(s *Schema) error { return s.Rebuild("ghost", func(t *Table) { t.ID() }) }),
		}, "table does not exist at this point"},
		{"rename a column onto an existing one", []Migration{
			mig("1", createPosts),
			mig("2", alterPosts(func(t *Table) { t.RenameColumn("title", "id") })),
		}, "column already added by 1"},
		{"drop a missing index", []Migration{
			mig("1", createPosts),
			mig("2", alterPosts(func(t *Table) { t.DropIndex("posts_ghost_index") })),
		}, "index posts_ghost_index does not exist"},
		{"a column that is added then renamed then dropped", []Migration{
			mig("1", createPosts),
			mig("2", alterPosts(func(t *Table) { t.RenameColumn("title", "headline") })),
			mig("3", alterPosts(func(t *Table) { t.DropColumn("headline") })),
		}, ""},
		{"drop a table then create it again", []Migration{
			mig("1", createPosts),
			mig("2", func(s *Schema) error { return s.Drop("posts") }),
			mig("3", createPosts),
		}, ""},
		{"two indexes of one name in a Create", []Migration{
			mig("1", func(s *Schema) error {
				return s.Create("posts", func(t *Table) {
					t.ID()
					t.String("title")
					t.Index("title")
					t.Index("title")
				})
			}),
		}, "index posts_title_index already created by 1"},
	}

	for _, tt := range tests {
		got, err := FindConflicts(database.SQLite, tt.migs)
		if err != nil {
			t.Errorf("%s: %v", tt.name, err)
			continue
		}

		switch {
		case tt.want == "" && len(got) != 0:
			t.Errorf("%s: conflicts %+v, want none", tt.name, got)
		case tt.want != "" && (len(got) != 1 || !strings.Contains(got[0].Message, tt.want)):
			t.Errorf("%s: conflicts %+v, want one containing %q", tt.name, got, tt.want)
		}
	}
}

func TestRebuildRefusesBadInput(t *testing.T) {
	t.Parallel()

	db := rebuildDB(t)
	s := New(t.Context(), db)
	execAll(t, db, "CREATE TABLE notes (id INTEGER PRIMARY KEY, body TEXT)")

	def := func(t *Table) { t.ID() }

	tests := []struct {
		name  string
		table string
		fn    func(t *Table)
		opts  []RebuildOption
		want  string
	}{
		{"an invalid table name", "bad name", def, nil, "invalid table name"},
		{"an invalid renamed column", "notes", def, []RebuildOption{RenamedFrom("a b", "id")}, "invalid column name"},
		{"a missing table", "ghost", def, nil, "no such table"},
		{"a builder error", "notes", func(t *Table) { t.String("bad name") }, nil, "invalid column name"},
		{"a primary key declared twice", "notes", func(t *Table) {
			t.Integer("a")
			t.Primary("a")
			t.Primary("a")
		}, nil, "already has a primary key"},
	}

	for _, tt := range tests {
		err := s.Rebuild(tt.table, tt.fn, tt.opts...)
		if err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("%s: Rebuild = %v, want an error containing %q", tt.name, err, tt.want)
		}
	}

	// A new NOT NULL column without a default is refused only when rows exist.
	if err := s.Rebuild("notes", func(t *Table) {
		t.ID()
		t.String("slug")
	}); err != nil {
		t.Fatalf("Rebuild adding a NOT NULL column to an empty table = %v", err)
	}

	execAll(t, db, "INSERT INTO notes (slug) VALUES ('a')")

	err := s.Rebuild("notes", func(t *Table) {
		t.ID()
		t.String("slug")
		t.String("extra")
	})
	if err == nil || !strings.Contains(err.Error(), "NOT NULL without a default") {
		t.Errorf("Rebuild adding a NOT NULL column to a table with rows = %v", err)
	}
}

func TestSchemaSmallBranches(t *testing.T) {
	t.Parallel()

	db := openDB(t)
	s := New(t.Context(), db)

	if err := s.Create("flags", func(t *Table) {
		t.ID()
		t.Float("ratio").Default(float32(1.5))
		t.Float("other").Default(2.25)
		t.Integer("n").Default(int8(3))
	}); err != nil {
		t.Fatal(err)
	}

	if got := toFloat("x"); got != 0 {
		t.Errorf("toFloat(string) = %v, want 0", got)
	}

	if has, err := s.HasTable("flags"); err != nil || !has {
		t.Errorf("HasTable = %v, %v", has, err)
	}

	if has, err := s.HasTable("absent"); err != nil || has {
		t.Errorf("HasTable(absent) = %v, %v", has, err)
	}

	if err := s.Table("flags", func(t *Table) { t.DropCheck("bad name") }); err == nil {
		t.Error("DropCheck accepted an invalid name")
	}

	if err := s.Create("gen", func(t *Table) {
		t.ID()
		t.Integer("a")
		t.Integer("b").StoredAs("a * 2").Default(1)
	}); err == nil || !strings.Contains(err.Error(), "cannot have a default") {
		t.Errorf("a generated column with a default = %v", err)
	}
}
