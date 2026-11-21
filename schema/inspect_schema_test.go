package schema

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/AlexGalhardo/vukem/database"
)

func TestSQLiteChecks(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		sql  string
		want []string
	}{
		{name: "none", sql: `CREATE TABLE t (id INTEGER, checked INTEGER)`},
		{name: "column check", sql: `CREATE TABLE t (n INTEGER CHECK (n > 0))`, want: []string{"n > 0"}},
		{
			name: "named table check with nested parentheses",
			sql:  `CREATE TABLE t (kind TEXT, CONSTRAINT kinds CHECK (kind IN ('a', 'b') AND (length(kind) < (10))))`,
			want: []string{"kind IN ('a', 'b') AND (length(kind) < (10))"},
		},
		{
			name: "parenthesis and keyword inside a string",
			sql:  `CREATE TABLE t (s TEXT CHECK (s <> ') check (' ), CHECK(s != ''))`,
			want: []string{"s <> ') check ('", "s != ''"},
		},
		{
			name: "quoted identifier named check",
			sql:  `CREATE TABLE "check" ("check (x)" INTEGER, "a""b" TEXT CHECK ("a""b" <> ''))`,
			want: []string{`"a""b" <> ''`},
		},
		{
			name: "comments",
			sql:  "CREATE TABLE t (n INTEGER, -- CHECK (nope)\n/* CHECK (nope) */ CHECK (n < 3))",
			want: []string{"n < 3"},
		},
		{name: "lowercase and spaced", sql: `create table t (n int check   (n=1))`, want: []string{"n=1"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if diff := cmp.Diff(tt.want, sqliteChecks(tt.sql)); diff != "" {
				t.Errorf("sqliteChecks (-want +got):\n%s", diff)
			}
		})
	}
}

func TestSchemaInspect(t *testing.T) {
	t.Parallel()

	db := rebuildDB(t)
	s := New(t.Context(), db)

	if err := s.Create("projects", func(t *Table) { t.ID() }); err != nil {
		t.Fatal(err)
	}

	execAll(t, db, `CREATE TABLE jobs (
		id INTEGER PRIMARY KEY,
		project_id INTEGER REFERENCES projects (id),
		kind TEXT CHECK (kind IN ('a', 'b'))
	)`)

	info, ok, err := s.Inspect("jobs")
	if err != nil || !ok {
		t.Fatalf("Inspect(jobs) = %v, %v", ok, err)
	}

	if len(info.ForeignKeys) != 1 || info.ForeignKeys[0].RefTable != "projects" {
		t.Errorf("foreign keys = %+v, want one to projects", info.ForeignKeys)
	}

	if diff := cmp.Diff([]string{"kind IN ('a', 'b')"}, info.SQLite.Checks); diff != "" {
		t.Errorf("checks (-want +got):\n%s", diff)
	}

	if _, ok, err := s.Inspect("absent"); err != nil || ok {
		t.Errorf("Inspect(absent) = %v, %v; want false, nil", ok, err)
	}

	if _, _, err := s.Inspect("a b"); err == nil {
		t.Error("Inspect with an invalid name = nil, want an error")
	}
}

func TestSchemaInspectWhileRecording(t *testing.T) {
	t.Parallel()

	var got bool

	if _, err := Record(rebuildDB(t).Dialect(), func(s *Schema) error {
		_, ok, err := s.Inspect("jobs")
		got = ok

		return err
	}); err != nil {
		t.Fatal(err)
	}

	if got {
		t.Error("Inspect while recording = true, want false")
	}
}

func TestSchemaInspectOnTheConflictModel(t *testing.T) {
	t.Parallel()

	var cols []string

	var found bool

	conflicts, err := FindConflicts(database.SQLite, []Migration{
		mig("1", createPosts),
		mig("2", func(s *Schema) error {
			info, ok, err := s.Inspect("posts")
			found = ok

			for _, c := range info.Columns {
				cols = append(cols, c.Name)
			}

			return err
		}),
	})
	if err != nil || len(conflicts) != 0 {
		t.Fatalf("FindConflicts = %v, %v; want none", conflicts, err)
	}

	if !found {
		t.Fatal("Inspect(posts) on the model = false, want true")
	}

	if diff := cmp.Diff([]string{"id", "title"}, cols); diff != "" {
		t.Errorf("columns (-want +got):\n%s", diff)
	}
}
