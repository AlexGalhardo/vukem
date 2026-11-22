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
	}, DropColumns("legacy_code"), DropIndexes("users_legacy_code_unique")); err != nil {
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
	}, DropColumns("legacy")); err != nil {
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

func TestRebuildRefusesToLoseAColumn(t *testing.T) {
	t.Parallel()

	keepID := func(t *Table) {
		t.ID()
		t.String("name")
	}

	tests := []struct {
		name    string
		fn      func(t *Table)
		opts    []RebuildOption
		wantErr string
	}{
		{name: "an undeclared column", fn: keepID, wantErr: "would lose column(s) approval_id"},
		{name: "accepted by name", fn: keepID, opts: []RebuildOption{DropColumns("approval_id")}},
		{
			name:    "a name the table does not have",
			fn:      keepID,
			opts:    []RebuildOption{DropColumns("approval_id", "ghost")},
			wantErr: "which the table does not have",
		},
		{
			name: "a name the definition keeps",
			fn: func(t *Table) {
				t.ID()
				t.String("name")
				t.String("approval_id")
			},
			opts:    []RebuildOption{DropColumns("approval_id")},
			wantErr: "which the new definition keeps",
		},
		{
			name: "a rename onto an existing column loses the target",
			fn: func(t *Table) {
				t.ID()
				t.String("approval_id")
			},
			opts:    []RebuildOption{RenamedFrom("name", "approval_id")},
			wantErr: "would lose column(s) approval_id",
		},
		{
			name:    "an invalid name",
			fn:      keepID,
			opts:    []RebuildOption{DropColumns("a b")},
			wantErr: "invalid column name",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			db := rebuildDB(t)
			execAll(
				t,
				db,
				`CREATE TABLE jobs (id INTEGER PRIMARY KEY AUTOINCREMENT, name VARCHAR(255) NOT NULL, approval_id TEXT)`,
				`INSERT INTO jobs (name, approval_id) VALUES ('a', 'x')`,
			)

			err := New(t.Context(), db).Rebuild("jobs", tt.fn, tt.opts...)

			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("Rebuild = %v, want nil", err)
				}

				return
			}

			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Rebuild = %v, want an error containing %q", err, tt.wantErr)
			}

			var got string
			if err := db.QueryRowContext(t.Context(), `SELECT approval_id FROM jobs`).
				Scan(&got); err != nil ||
				got != "x" {
				t.Errorf("approval_id after the refusal = %q, %v; want x", got, err)
			}
		})
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
	}, RenamedFrom("code", "id"), DropColumns("id"))
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

func TestRebuildIndexes(t *testing.T) {
	t.Parallel()

	const (
		kindState = `CREATE INDEX jobs_kind_state_index ON jobs (kind, state)`
		partial   = `CREATE UNIQUE INDEX jobs_active ON jobs (kind) WHERE state = 'active'`
	)

	body := func(t *Table) {
		t.ID()
		t.String("kind")
		t.String("state")
	}

	tests := []struct {
		name    string
		fn      func(t *Table)
		opts    []RebuildOption
		wantErr string
		wantSQL []string // sqlite_master text of the explicit indexes afterwards
	}{
		{
			name: "a lost index is refused, naming the fix",
			fn:   body,
			wantErr: "would lose index(es) jobs_active, jobs_kind_state_index; declare them, pass schema.KeepIndexes() " +
				`to recreate them or schema.DropIndexes("jobs_active", "jobs_kind_state_index") to accept the loss`,
		},
		{
			name:    "a lost index points at the upgrade guide",
			fn:      body,
			wantErr: "docs/upgrade/1.0.md, section \"Schema.Rebuild refuses to lose a column or an index\"",
		},
		{
			name:    "KeepIndexes keeps the original text",
			fn:      body,
			opts:    []RebuildOption{KeepIndexes()},
			wantSQL: []string{partial, kindState},
		},
		{
			name:    "DropIndexes accepts the loss",
			fn:      body,
			opts:    []RebuildOption{DropIndexes("jobs_active", "jobs_kind_state_index")},
			wantSQL: nil,
		},
		{
			name:    "KeepIndexes skips a dropped one",
			fn:      body,
			opts:    []RebuildOption{KeepIndexes(), DropIndexes("jobs_active")},
			wantSQL: []string{kindState},
		},
		{
			name: "a declared index is not kept",
			fn: func(t *Table) {
				body(t)
				t.Index("kind", "state")
			},
			opts:    []RebuildOption{KeepIndexes()},
			wantSQL: []string{partial, `CREATE INDEX "jobs_kind_state_index" ON "jobs" ("kind", "state")`},
		},
		{
			name:    "DropIndexes names a missing index",
			fn:      body,
			opts:    []RebuildOption{KeepIndexes(), DropIndexes("ghost")},
			wantErr: "which the table does not have",
		},
		{
			name: "DropIndexes names a declared index",
			fn: func(t *Table) {
				body(t)
				t.Index("kind", "state")
			},
			opts:    []RebuildOption{DropIndexes("jobs_kind_state_index", "jobs_active")},
			wantErr: "which the new definition declares",
		},
		{
			name: "KeepIndexes on an index whose column is gone",
			fn: func(t *Table) {
				t.ID()
				t.String("kind")
			},
			opts:    []RebuildOption{KeepIndexes(), DropColumns("state")},
			wantErr: "index jobs_kind_state_index uses column state",
		},
		{
			name: "a dropped column with its index dropped too",
			fn: func(t *Table) {
				t.ID()
				t.String("kind")
			},
			opts: []RebuildOption{
				KeepIndexes(),
				DropColumns("state"),
				DropIndexes("jobs_kind_state_index", "jobs_active"),
			},
			wantErr: "",
			wantSQL: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			db := rebuildDB(t)
			execAll(
				t,
				db,
				`CREATE TABLE jobs (id INTEGER PRIMARY KEY AUTOINCREMENT, kind TEXT, state TEXT)`,
				kindState,
				partial,
				`INSERT INTO jobs (kind, state) VALUES ('a', 'idle')`,
			)

			err := New(t.Context(), db).Rebuild("jobs", tt.fn, tt.opts...)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("Rebuild = %v, want an error containing %q", err, tt.wantErr)
				}

				var n int
				if err := db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND sql IS NOT NULL`).
					Scan(&n); err != nil ||
					n != 2 {
					t.Errorf("indexes after the refusal = %d, %v; want 2", n, err)
				}

				return
			}

			if err != nil {
				t.Fatalf("Rebuild = %v, want nil", err)
			}

			got := indexSQL(t, db)

			if diff := cmp.Diff(tt.wantSQL, got); diff != "" {
				t.Errorf("indexes (-want +got):\n%s", diff)
			}
		})
	}
}

func indexSQL(t *testing.T, db *database.DB) []string {
	t.Helper()

	rows, err := db.QueryContext(
		t.Context(),
		`SELECT sql FROM sqlite_master WHERE type = 'index' AND sql IS NOT NULL ORDER BY name`,
	)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()

	var out []string

	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatal(err)
		}

		out = append(out, s)
	}

	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}

	return out
}

// VF-113: a Down written before Rebuild refused implicit drops leaves out the
// column its Up added. It is refused by default, names the fix, and the fix
// (DropColumns) neither changes the Up checksum nor reopens silent loss.
func TestRebuildInDownRefusesTheColumnItsUpAdded(t *testing.T) {
	t.Parallel()

	down := func(opts ...RebuildOption) func(*Schema) error {
		return func(s *Schema) error {
			return s.Rebuild("budgets", func(t *Table) {
				t.ID()
				t.String("name")
			}, opts...)
		}
	}

	build := func(opts ...RebuildOption) []Migration {
		return []Migration{
			{
				ID: "20261122130000_create_budgets",
				Up: func(s *Schema) error {
					return s.Create("budgets", func(t *Table) {
						t.ID()
						t.String("name")
					})
				},
			},
			{
				ID:            "20261122140000_add_rollover",
				NoTransaction: true,
				Up: func(s *Schema) error {
					return s.Table("budgets", func(t *Table) { t.Integer("rollover").Default(0) })
				},
				Down: down(opts...),
			},
		}
	}

	db := rebuildDB(t)

	m, err := NewMigrator(db, build())
	if err != nil {
		t.Fatal(err)
	}

	if _, err := m.Up(t.Context()); err != nil {
		t.Fatal(err)
	}

	_, refused := m.Rollback(t.Context(), 1)
	if refused == nil || !strings.Contains(refused.Error(), "would lose column(s) rollover") {
		t.Fatalf("Rollback() = %v, want the refusal naming rollover", refused)
	}

	// The refusal names the exact fix and the guide, so the person who hits it in a Down finds both.
	for _, want := range []string{
		`schema.DropColumns("rollover")`,
		"docs/upgrade/1.0.md",
		"Schema.Rebuild refuses to lose a column or an index",
	} {
		if !strings.Contains(refused.Error(), want) {
			t.Errorf("Rollback() error = %q, want it to contain %q", refused, want)
		}
	}

	fixed, err := NewMigrator(db, build(DropColumns("rollover")))
	if err != nil {
		t.Fatal(err)
	}

	if back, err := fixed.Rollback(t.Context(), 1); err != nil || len(back) != 1 {
		t.Fatalf("Rollback() with DropColumns = %v, %v, want it to run", back, err)
	}

	before, _ := m.Checksum(build()[1])
	after, _ := fixed.Checksum(build(DropColumns("rollover"))[1])

	if before != after {
		t.Errorf("checksum changed after editing Down: %s != %s", before, after)
	}
}
