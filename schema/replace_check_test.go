package schema

import (
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/AlexGalhardo/vukem/database"
)

func recordingsDB(t *testing.T) *database.DB {
	t.Helper()

	db := rebuildDB(t)
	execAll(t, db,
		`CREATE TABLE recordings (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			title TEXT NOT NULL CHECK (length(title) > 0),
			status TEXT NOT NULL DEFAULT 'draft' CHECK (status IN ('draft', 'done')),
			size INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE INDEX recordings_status ON recordings (status) WHERE status <> 'done'`,
		`CREATE TABLE audit (recording_id INTEGER)`,
		`CREATE TRIGGER recordings_audit AFTER INSERT ON recordings BEGIN INSERT INTO audit VALUES (new.id); END`,
		`INSERT INTO recordings (title, status) VALUES ('a', 'draft'), ('b', 'done'), ('c', 'draft')`,
		`DELETE FROM recordings WHERE id = 3`,
	)

	return db
}

func TestReplaceCheckAddsAValueToAClosedList(t *testing.T) {
	t.Parallel()

	db := recordingsDB(t)
	s := New(t.Context(), db)

	before := indexSQL(t, db)

	if err := s.ReplaceCheck("recordings",
		"status IN ('draft', 'done')",
		"status IN ('draft', 'paused', 'done')",
	); err != nil {
		t.Fatal(err)
	}

	execAll(t, db, `INSERT INTO recordings (title, status) VALUES ('d', 'paused')`)

	if _, err := db.ExecContext(
		t.Context(),
		`INSERT INTO recordings (title, status) VALUES ('e', 'gone')`,
	); err == nil {
		t.Error("INSERT status gone = nil error, want the new CHECK to refuse it")
	}

	if _, err := db.ExecContext(t.Context(), `INSERT INTO recordings (title) VALUES ('')`); err == nil {
		t.Error("INSERT empty title = nil error, want the untouched CHECK to stay")
	}

	var id int
	if err := db.QueryRowContext(t.Context(), "SELECT id FROM recordings WHERE title = 'd'").Scan(&id); err != nil {
		t.Fatal(err)
	}

	if id != 4 {
		t.Errorf("id of the next row = %d, want 4: the AUTOINCREMENT counter must survive", id)
	}

	if got := count(t, db, "recordings"); got != 3 {
		t.Errorf("rows = %d, want 3", got)
	}

	if got := count(t, db, "audit"); got != 4 {
		t.Errorf("audit rows = %d, want 4: the trigger must be recreated", got)
	}

	if diff := cmp.Diff(before, indexSQL(t, db)); diff != "" {
		t.Errorf("indexes (-before +after):\n%s", diff)
	}

	info, _, err := InspectTable(t.Context(), db, "recordings")
	if err != nil {
		t.Fatal(err)
	}

	want := []string{"length(title) > 0", "status IN ('draft', 'paused', 'done')"}
	if diff := cmp.Diff(want, info.SQLite.Checks); diff != "" {
		t.Errorf("checks (-want +got):\n%s", diff)
	}
}

func TestReplaceCheckRefusals(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name, old, expr, want string
	}{
		{"unknown expression", "status IN ('x')", "1", "no CHECK has the expression"},
		{"smuggled column", "status IN ('draft', 'done')", "1), evil TEXT, CHECK (1", "not one balanced CHECK"},
		{"rows that break the new check", "status IN ('draft', 'done')", "status = 'draft'", "CHECK constraint failed"},
		{"empty expression", "status IN ('draft', 'done')", " ", "needs the old and the new expression"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			db := recordingsDB(t)

			err := New(t.Context(), db).ReplaceCheck("recordings", tt.old, tt.expr)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("ReplaceCheck = %v, want an error containing %q", err, tt.want)
			}

			info, _, err := InspectTable(t.Context(), db, "recordings")
			if err != nil {
				t.Fatal(err)
			}

			if !slices.Contains(info.SQLite.Checks, "status IN ('draft', 'done')") || count(t, db, "recordings") != 2 {
				t.Errorf("table changed after a refused ReplaceCheck: checks %q", info.SQLite.Checks)
			}

			if fkPragma(t, db) != 1 {
				t.Error("foreign keys left off")
			}
		})
	}
}

func TestReplaceCheckRecordsAndRefusesOtherDialects(t *testing.T) {
	t.Parallel()

	got, err := Record(database.SQLite, func(s *Schema) error {
		return s.ReplaceCheck("recordings", "status IN ('a')", "status IN ('a', 'b')")
	})
	if err != nil {
		t.Fatal(err)
	}

	if diff := cmp.Diff(
		[]string{"-- replace check on recordings: (status IN ('a')) with (status IN ('a', 'b'))"},
		got,
	); diff != "" {
		t.Errorf("recorded statements (-want +got):\n%s", diff)
	}

	_, err = Record(database.Postgres, func(s *Schema) error {
		return s.ReplaceCheck("recordings", "a", "b")
	})
	if err == nil || !strings.Contains(err.Error(), "ReplaceCheck is for sqlite") {
		t.Errorf("ReplaceCheck on PostgreSQL = %v", err)
	}

	err = rebuildDB(t).Tx(t.Context(), func(tx *database.Tx) error {
		return New(t.Context(), tx).ReplaceCheck("recordings", "a", "b")
	})
	if err == nil || !strings.Contains(err.Error(), "NoTransaction") {
		t.Errorf("ReplaceCheck in a *Tx = %v, want the NoTransaction error", err)
	}
}
