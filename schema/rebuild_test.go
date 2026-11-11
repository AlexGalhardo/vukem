package schema

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/AlexGalhardo/vukem/database"
	"github.com/AlexGalhardo/vukem/internal/testutil"
)

func rebuildDB(t *testing.T) *database.DB {
	t.Helper()

	db, err := database.Open(t.Context(), "file:"+testutil.TempDir(t)+"/rebuild.db")
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = db.Close() })

	return db
}

func execAll(t *testing.T, db *database.DB, stmts ...string) {
	t.Helper()

	for _, q := range stmts {
		if _, err := db.ExecContext(t.Context(), q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
}

func count(t *testing.T, db *database.DB, table string) int {
	t.Helper()

	var n int
	if err := db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM "+table).Scan(&n); err != nil {
		t.Fatal(err)
	}

	return n
}

func fkPragma(t *testing.T, db *database.DB) int {
	t.Helper()

	var n int
	if err := db.QueryRowContext(t.Context(), "PRAGMA foreign_keys").Scan(&n); err != nil {
		t.Fatal(err)
	}

	return n
}

func TestRebuildDropsAnIndexedColumn(t *testing.T) {
	t.Parallel()

	db := rebuildDB(t)
	s := New(t.Context(), db)

	if err := s.Create("users", func(t *Table) {
		t.ID()
		t.String("name")
		t.String("email").Unique()
		t.String("legacy_code", 20)
		t.UniqueIndex("legacy_code")
	}); err != nil {
		t.Fatal(err)
	}

	execAll(t, db,
		`INSERT INTO users (name, email, legacy_code) VALUES ('Ana', 'ana@x.io', 'A1'), ('Bob', 'bob@x.io', 'B2')`)

	// DROP COLUMN fails on a column with an index; Rebuild does not.
	if err := s.Table("users", func(t *Table) { t.DropColumn("legacy_code") }); err == nil {
		t.Fatal("DROP COLUMN of an indexed column succeeded on SQLite; the test premise is wrong")
	}

	if err := s.Rebuild("users", func(t *Table) {
		t.ID()
		t.String("name")
		t.String("email").Unique()
	}); err != nil {
		t.Fatal(err)
	}

	info, ok, err := InspectTable(t.Context(), db, "users")
	if err != nil || !ok {
		t.Fatalf("InspectTable = %v, %v", ok, err)
	}

	var cols []string
	for _, c := range info.Columns {
		cols = append(cols, c.Name)
	}

	if diff := cmp.Diff([]string{"id", "name", "email"}, cols); diff != "" {
		t.Errorf("columns (-want +got):\n%s", diff)
	}

	if n := count(t, db, "users"); n != 2 {
		t.Errorf("rows after Rebuild = %d, want 2", n)
	}

	// The UNIQUE on email survived (it was declared again).
	if _, err := db.ExecContext(t.Context(), `INSERT INTO users (name, email) VALUES ('Eve', 'ana@x.io')`); err == nil {
		t.Error("the unique email was lost")
	}
}

func TestRebuildKeepsChildrenOfACascade(t *testing.T) {
	t.Parallel()

	db := rebuildDB(t)
	s := New(t.Context(), db)

	if err := s.Create("parents", func(t *Table) {
		t.ID()
		t.String("name")
		t.String("legacy").Nullable()
	}); err != nil {
		t.Fatal(err)
	}

	if err := s.Create("children", func(t *Table) {
		t.ID()
		t.ForeignID("parent_id").Constrained("parents").CascadeOnDelete()
	}); err != nil {
		t.Fatal(err)
	}

	execAll(t, db,
		`INSERT INTO parents (name) VALUES ('p1'), ('p2')`,
		`INSERT INTO children (parent_id) VALUES (1), (1), (2)`)

	if err := s.Rebuild("parents", func(t *Table) {
		t.ID()
		t.String("name")
	}); err != nil {
		t.Fatal(err)
	}

	if n := count(t, db, "children"); n != 3 {
		t.Fatalf("children after rebuilding the parent table = %d, want 3 (DROP TABLE must not cascade)", n)
	}

	if n := count(t, db, "parents"); n != 2 {
		t.Errorf("parents = %d, want 2", n)
	}

	// The cascade is still wired to the new table.
	execAll(t, db, `DELETE FROM parents WHERE id = 1`)

	if n := count(t, db, "children"); n != 1 {
		t.Errorf("children after deleting a parent = %d, want 1 (the cascade must still work)", n)
	}
}

func TestRebuildRenamedFromCopiesValues(t *testing.T) {
	t.Parallel()

	db := rebuildDB(t)
	s := New(t.Context(), db)

	if err := s.Create("users", func(t *Table) {
		t.ID()
		t.String("nickname", 30)
	}); err != nil {
		t.Fatal(err)
	}

	execAll(t, db, `INSERT INTO users (nickname) VALUES ('ada')`)

	if err := s.Rebuild("users", func(t *Table) {
		t.ID()
		t.String("handle", 30)
	}, RenamedFrom("nickname", "handle")); err != nil {
		t.Fatal(err)
	}

	var got string
	if err := db.QueryRowContext(t.Context(), `SELECT handle FROM users`).Scan(&got); err != nil || got != "ada" {
		t.Errorf("handle = %q, %v; want ada", got, err)
	}
}

func TestRebuildRefusesANewRequiredColumnBeforeChangingAnything(t *testing.T) {
	t.Parallel()

	db := rebuildDB(t)
	s := New(t.Context(), db)

	if err := s.Create("users", func(t *Table) {
		t.ID()
		t.String("name")
	}); err != nil {
		t.Fatal(err)
	}

	execAll(t, db, `INSERT INTO users (name) VALUES ('ana')`)

	err := s.Rebuild("users", func(t *Table) {
		t.ID()
		t.String("name")
		t.String("role") // NOT NULL, no default
	})
	if err == nil || !strings.Contains(err.Error(), "new column role is NOT NULL without a default") {
		t.Fatalf("Rebuild = %v, want the NOT NULL error", err)
	}

	info, _, _ := InspectTable(t.Context(), db, "users")
	if len(info.Columns) != 2 || count(t, db, "users") != 1 {
		t.Errorf("the table changed despite the error: %+v", info)
	}

	// With a default it is fine.
	if err := s.Rebuild("users", func(t *Table) {
		t.ID()
		t.String("name")
		t.String("role").Default("member")
	}); err != nil {
		t.Errorf("Rebuild with a default = %v", err)
	}
}

func TestRebuildRollsBackOnAForeignKeyViolation(t *testing.T) {
	t.Parallel()

	db := rebuildDB(t)
	s := New(t.Context(), db)

	if err := s.Create("parents", func(t *Table) {
		t.ID()
		t.Integer("code")
	}); err != nil {
		t.Fatal(err)
	}

	if err := s.Create("children", func(t *Table) {
		t.ID()
		t.ForeignID("parent_id").Constrained("parents")
	}); err != nil {
		t.Fatal(err)
	}

	execAll(t, db,
		`INSERT INTO parents (code) VALUES (100), (200)`,
		`INSERT INTO children (parent_id) VALUES (1), (2)`)

	// The new ids come from the codes: the children now point nowhere.
	err := s.Rebuild("parents", func(t *Table) {
		t.ID()
		t.Integer("code")
	}, RenamedFrom("code", "id"))
	if err == nil || !strings.Contains(err.Error(), "foreign key violation") {
		t.Fatalf("Rebuild = %v, want a foreign key violation error", err)
	}

	var first int
	if err := db.QueryRowContext(t.Context(), `SELECT id FROM parents ORDER BY id LIMIT 1`).
		Scan(&first); err != nil ||
		first != 1 {
		t.Errorf("parents after the rollback: first id %d, %v; want the original 1", first, err)
	}

	if n := fkPragma(t, db); n != 1 {
		t.Errorf("PRAGMA foreign_keys = %d after a failed rebuild, want 1", n)
	}
}

func TestRebuildInsideATransactionNamesNoTransaction(t *testing.T) {
	t.Parallel()

	db := rebuildDB(t)

	err := db.Tx(t.Context(), func(tx *database.Tx) error {
		return New(t.Context(), tx).Rebuild("users", func(t *Table) { t.ID() })
	})
	if err == nil || !strings.Contains(err.Error(), "NoTransaction") {
		t.Errorf("Rebuild in a *Tx = %v, want the NoTransaction error", err)
	}
}

func TestRebuildIsSQLiteOnly(t *testing.T) {
	t.Parallel()

	_, err := Record(database.Postgres, func(s *Schema) error {
		return s.Rebuild("users", func(t *Table) { t.ID() })
	})
	if err == nil || !strings.Contains(err.Error(), "Rebuild is for sqlite; use Table and Column.Change on postgres") {
		t.Errorf("Rebuild on PostgreSQL = %v", err)
	}
}

func TestRebuildCleansALeftoverTable(t *testing.T) {
	t.Parallel()

	db := rebuildDB(t)
	s := New(t.Context(), db)

	if err := s.Create("users", func(t *Table) {
		t.ID()
		t.String("name")
	}); err != nil {
		t.Fatal(err)
	}

	execAll(t, db, `CREATE TABLE _vk_rebuild_users (junk TEXT)`)

	if err := s.Rebuild("users", func(t *Table) {
		t.ID()
		t.String("name")
	}); err != nil {
		t.Fatal(err)
	}

	snap, err := Inspect(t.Context(), db)
	if err != nil {
		t.Fatal(err)
	}

	if _, ok := snap.Table("_vk_rebuild_users"); ok {
		t.Error("the leftover _vk_rebuild_users table is still there")
	}
}

func TestRebuildRecordsTheProcedure(t *testing.T) {
	t.Parallel()

	got, err := Record(database.SQLite, func(s *Schema) error {
		return s.Rebuild("users", func(t *Table) {
			t.ID()
			t.String("email").Unique()
		})
	})
	if err != nil {
		t.Fatal(err)
	}

	want := []string{
		"PRAGMA foreign_keys = OFF",
		"BEGIN IMMEDIATE",
		"CREATE TABLE \"_vk_rebuild_users\" (\n  \"id\" INTEGER PRIMARY KEY AUTOINCREMENT,\n  \"email\" VARCHAR(255) NOT NULL UNIQUE\n)",
		`INSERT INTO "_vk_rebuild_users" ("id", "email") SELECT "id", "email" FROM "users"`,
		`DROP TABLE "users"`,
		`ALTER TABLE "_vk_rebuild_users" RENAME TO "users"`,
		"PRAGMA foreign_key_check",
		"COMMIT",
		"PRAGMA foreign_keys = ON",
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("recorded statements (-want +got):\n%s", diff)
	}
}

func TestRestoreConnDiscardsAConnectionItCannotRestore(t *testing.T) {
	t.Parallel()

	db := rebuildDB(t)

	conn, err := db.SQL().Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	// No transaction is open, so the ROLLBACK fails.
	err = restoreConn(t.Context(), conn, false, true, "posts")
	if err == nil || !strings.Contains(err.Error(), "connection discarded") {
		t.Fatalf("restoreConn = %v, want the discard error", err)
	}

	_ = conn.Close() // already closed by the discard

	if n := db.SQL().Stats().OpenConnections; n != 0 {
		t.Errorf("%d connection(s) back in the pool, want it closed", n)
	}
}

func TestRebuildKeepsTheAutoincrementCounter(t *testing.T) {
	t.Parallel()

	db := rebuildDB(t)
	execAll(t, db,
		"CREATE TABLE notes (id INTEGER PRIMARY KEY AUTOINCREMENT, body TEXT NOT NULL)",
		"INSERT INTO notes (body) VALUES ('a'), ('b'), ('c')",
		"DELETE FROM notes WHERE id = 3",
	)

	if err := New(t.Context(), db).Rebuild("notes", func(t *Table) {
		t.ID()
		t.Text("body")
	}); err != nil {
		t.Fatal(err)
	}

	execAll(t, db, "INSERT INTO notes (body) VALUES ('d')")

	var id int
	if err := db.QueryRowContext(t.Context(), "SELECT MAX(id) FROM notes").Scan(&id); err != nil {
		t.Fatal(err)
	}

	if id != 4 {
		t.Errorf("next id after the rebuild = %d, want 4: an id that was used must not be reused", id)
	}
}

func TestRebuildRefusesATableWithTriggersUnlessAsked(t *testing.T) {
	t.Parallel()

	db := rebuildDB(t)
	execAll(t, db,
		"CREATE TABLE notes (id INTEGER PRIMARY KEY AUTOINCREMENT, body TEXT NOT NULL)",
		"CREATE TABLE audit (note_id INTEGER)",
		"CREATE TRIGGER notes_audit AFTER INSERT ON notes BEGIN INSERT INTO audit VALUES (new.id); END",
	)

	def := func(t *Table) {
		t.ID()
		t.Text("body")
	}

	err := New(t.Context(), db).Rebuild("notes", def)
	if err == nil || !strings.Contains(err.Error(), "notes_audit") || !strings.Contains(err.Error(), "DropTriggers") {
		t.Fatalf("Rebuild of a table with a trigger = %v, want a refusal naming the trigger and DropTriggers", err)
	}

	if err := New(t.Context(), db).Rebuild("notes", def, DropTriggers()); err != nil {
		t.Fatal(err)
	}

	var n int
	if err := db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM sqlite_master WHERE type = 'trigger'").
		Scan(&n); err != nil ||
		n != 0 {
		t.Errorf("triggers left = %d, %v; DropTriggers must drop them", n, err)
	}
}
