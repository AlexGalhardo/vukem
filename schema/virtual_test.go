package schema

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/AlexGalhardo/vukem/database"
)

func TestVirtualTable(t *testing.T) {
	t.Parallel()

	db := rebuildDB(t)
	s := New(t.Context(), db)

	if err := s.VirtualTable("asset_search", "fts5", "name", "tags", "tokenize='porter'"); err != nil {
		t.Fatal(err)
	}

	execAll(t, db, `INSERT INTO asset_search (name, tags) VALUES ('red fox', 'animal')`)

	var n int
	if err := db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM asset_search WHERE asset_search MATCH 'fox'`).
		Scan(&n); err != nil || n != 1 {
		t.Fatalf("MATCH fox = %d, %v; want 1", n, err)
	}

	snap, err := Inspect(t.Context(), db)
	if err != nil {
		t.Fatal(err)
	}

	var names []string
	for _, tbl := range snap.Tables {
		names = append(names, tbl.Name)
	}

	if diff := cmp.Diff([]string{"asset_search"}, names); diff != "" {
		t.Errorf("tables, shadow tables left out (-want +got):\n%s", diff)
	}

	if info, ok, err := s.Inspect("asset_search"); err != nil || !ok || !info.SQLite.Virtual {
		t.Errorf("Inspect(asset_search) = %+v, %v, %v; want a virtual table", info.SQLite, ok, err)
	}

	if err := s.DropIfExists("asset_search"); err != nil {
		t.Fatal(err)
	}

	if has, err := s.HasTable("asset_search"); err != nil || has {
		t.Errorf("HasTable after DropIfExists = %v, %v; want false", has, err)
	}
}

func TestVirtualTableRecordsAndValidates(t *testing.T) {
	t.Parallel()

	got, err := Record(
		database.SQLite,
		func(s *Schema) error { return s.VirtualTable("docs", "fts5", "title", "body") },
	)
	if err != nil {
		t.Fatal(err)
	}

	want := []string{`CREATE VIRTUAL TABLE "docs" USING fts5(title, body)`}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("recorded (-want +got):\n%s", diff)
	}

	tests := []struct {
		name    string
		d       database.Dialect
		table   string
		module  string
		args    []string
		wantErr string
	}{
		{name: "postgres", d: database.Postgres, table: "d", module: "fts5", wantErr: "sqlite only"},
		{name: "mysql", d: database.MySQL, table: "d", module: "fts5", wantErr: "sqlite only"},
		{name: "bad table", d: database.SQLite, table: "d d", module: "fts5", wantErr: "invalid table name"},
		{name: "bad module", d: database.SQLite, table: "d", module: "fts5(", wantErr: "invalid module name"},
		{
			name:    "empty argument",
			d:       database.SQLite,
			table:   "d",
			module:  "fts5",
			args:    []string{"a", ""},
			wantErr: "empty",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := Record(tt.d, func(s *Schema) error { return s.VirtualTable(tt.table, tt.module, tt.args...) })
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("VirtualTable = %v, want an error containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestVirtualTableOnTheConflictModel(t *testing.T) {
	t.Parallel()

	conflicts, err := FindConflicts(database.SQLite, []Migration{
		mig("1", func(s *Schema) error { return s.VirtualTable("docs", "fts5", "title", "tokenize='porter'") }),
		mig("2", func(s *Schema) error { return s.VirtualTable("docs", "fts5", "title") }),
	})
	if err != nil {
		t.Fatal(err)
	}

	if len(conflicts) != 1 || !strings.Contains(conflicts[0].Message, "already created by 1") {
		t.Errorf("conflicts = %+v, want the second create reported", conflicts)
	}
}
