package schema

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/AlexGalhardo/vukem/database"
)

// stmtLog records every statement sent through a DB.
type stmtLog struct {
	mu   sync.Mutex
	stmt []string
}

func (l *stmtLog) ObserveQuery(_ context.Context, q database.Query) {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.stmt = append(l.stmt, q.SQL)
}

func (l *stmtLog) all() []string {
	l.mu.Lock()
	defer l.mu.Unlock()

	return append([]string(nil), l.stmt...)
}

// serverDialectDB runs SQL rendered for dl on SQLite, which has no
// information_schema: the server-only queries fail after being recorded,
// so their text and error paths are checked without a server.
func serverDialectDB(t *testing.T, dl database.Dialect) (*database.DB, *stmtLog) {
	t.Helper()

	db := database.FromSQL(openDB(t).SQL(), dl)
	log := &stmtLog{}
	db.SetQueryObserver(log)

	return db, log
}

func tableNames(t *testing.T, db *database.DB) []string {
	t.Helper()

	rows, err := db.QueryContext(t.Context(),
		"SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%' ORDER BY name")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()

	var out []string

	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatal(err)
		}

		out = append(out, n)
	}

	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}

	return out
}

func TestDropAllSQLiteDropsTablesAndRestoresForeignKeys(t *testing.T) {
	t.Parallel()

	db := openDB(t)
	ctx := t.Context()

	if err := usersPosts(New(ctx, db)); err != nil {
		t.Fatal(err)
	}

	if _, err := db.ExecContext(ctx, `INSERT INTO users (id, name, email) VALUES (1, 'ana', 'a@x')`); err != nil {
		t.Fatal(err)
	}

	if _, err := db.ExecContext(ctx, `INSERT INTO posts (id, user_id, title) VALUES (1, 1, 'hi')`); err != nil {
		t.Fatal(err)
	}

	// users is referenced by posts, so dropping it first needs the
	// foreign key check off.
	if err := DropAll(ctx, db); err != nil {
		t.Fatalf("DropAll() = %v, want nil", err)
	}

	if got := tableNames(t, db); len(got) != 0 {
		t.Errorf("tables after DropAll() = %q, want none", got)
	}

	var fk int
	if err := db.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&fk); err != nil {
		t.Fatal(err)
	}

	if fk != 1 {
		t.Errorf("PRAGMA foreign_keys after DropAll() = %d, want 1 (restored)", fk)
	}
}

func TestFreshDropsDataAndReruns(t *testing.T) {
	t.Parallel()

	db := openDB(t)
	ctx := t.Context()

	m, err := NewMigrator(db, migrations())
	if err != nil {
		t.Fatal(err)
	}

	if _, err := m.Up(ctx); err != nil {
		t.Fatal(err)
	}

	if _, err := db.ExecContext(ctx, `CREATE TABLE stray (id INTEGER)`); err != nil {
		t.Fatal(err)
	}

	ran, err := m.Fresh(ctx)
	if err != nil {
		t.Fatalf("Fresh() = %v, want nil", err)
	}

	if len(ran) != len(migrations()) {
		t.Errorf("Fresh() ran %q, want every migration", ran)
	}

	for _, name := range tableNames(t, db) {
		if name == "stray" {
			t.Error("Fresh() kept a table no migration creates")
		}
	}

	st, err := m.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}

	for _, s := range st {
		if !s.Ran || s.Batch != 1 {
			t.Errorf("Status() after Fresh() = %+v, want ran in batch 1", s)
		}
	}
}

func TestDropAllServerDialects(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		dialect database.Dialect
		want    string
	}{
		{"postgres", database.Postgres, "table_schema = current_schema() AND table_type = 'BASE TABLE'"},
		{"mysql", database.MySQL, "table_schema = DATABASE() AND table_type = 'BASE TABLE'"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			db, log := serverDialectDB(t, tt.dialect)

			err := DropAll(t.Context(), db)
			if err == nil || !strings.Contains(err.Error(), "schema: list tables") {
				t.Fatalf("DropAll() on SQLite = %v, want list tables error", err)
			}

			m, _ := NewMigrator(db, migrations())
			if _, err := m.Fresh(t.Context()); err == nil {
				t.Error("Fresh() = nil error, want the DropAll error")
			}

			// The MySQL listing runs on a pinned connection, outside the observer.
			if tt.dialect == database.MySQL {
				return
			}

			if got := log.all(); len(got) == 0 || !strings.Contains(got[0], tt.want) {
				t.Errorf("statements = %q, want a listing with %q", got, tt.want)
			}
		})
	}
}

func TestHasTableDialects(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		dialect database.Dialect
		want    string
	}{
		{"postgres", database.Postgres, "table_schema = current_schema() AND table_name = $1"},
		{"mysql", database.MySQL, "table_schema = DATABASE() AND table_name = ?"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			db, log := serverDialectDB(t, tt.dialect)

			ok, err := New(t.Context(), db).HasTable("users")
			if ok || err == nil || !strings.Contains(err.Error(), "schema: has table") {
				t.Fatalf("HasTable() on SQLite = %v, %v, want has table error", ok, err)
			}

			if got := log.all(); len(got) != 1 || !strings.Contains(got[0], tt.want) {
				t.Errorf("statements = %q, want %q", got, tt.want)
			}
		})
	}

	s, _ := recorder(database.SQLite)
	if ok, err := s.HasTable("users"); ok || err != nil {
		t.Errorf("HasTable() while recording = %v, %v, want false, nil", ok, err)
	}
}

func TestMigratorOnClosedDB(t *testing.T) {
	t.Parallel()

	db := openDB(t)
	m, _ := NewMigrator(db, migrations())

	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	ctx := t.Context()
	calls := map[string]func() error{
		"Up":       func() error { _, err := m.Up(ctx); return err },
		"Rollback": func() error { _, err := m.Rollback(ctx, 0); return err },
		"Reset":    func() error { _, err := m.Reset(ctx); return err },
		"Status":   func() error { _, err := m.Status(ctx); return err },
	}

	for name, call := range calls {
		if err := call(); err == nil || !strings.Contains(err.Error(), "schema: create migrations table") {
			t.Errorf("%s() on closed DB = %v, want create migrations table error", name, err)
		}
	}
}

func TestMigratorCorruptRecord(t *testing.T) {
	t.Parallel()

	db := openDB(t)
	ctx := t.Context()
	m, _ := NewMigrator(db, migrations())

	if _, err := m.Status(ctx); err != nil { // creates the migrations table
		t.Fatal(err)
	}

	if _, err := db.ExecContext(
		ctx,
		`INSERT INTO migrations (id, batch, checksum, ran_at) VALUES ('x', 'not a number', '', CURRENT_TIMESTAMP)`,
	); err != nil {
		t.Fatal(err)
	}

	if _, err := m.Up(ctx); err == nil || !strings.Contains(err.Error(), "schema: read migrations") {
		t.Errorf("Up() with a corrupt record = %v, want read migrations error", err)
	}
}

func TestResetStopsAtFailingDown(t *testing.T) {
	t.Parallel()

	db := openDB(t)
	ctx := t.Context()
	boom := errors.New("boom")

	create := func(name string) func(*Schema) error {
		return func(s *Schema) error { return s.Create(name, func(t *Table) { t.ID() }) }
	}

	m, _ := NewMigrator(db, []Migration{
		{ID: "1_a", Up: create("a"), Down: func(*Schema) error { return boom }},
		{ID: "2_b", Up: create("b"), Down: func(s *Schema) error { return s.Drop("b") }},
	})

	if _, err := m.Up(ctx); err != nil {
		t.Fatal(err)
	}

	reverted, err := m.Reset(ctx)
	if !errors.Is(err, boom) || !strings.Contains(err.Error(), "schema: rollback 1_a") {
		t.Fatalf("Reset() error = %v, want rollback 1_a: boom", err)
	}

	if len(reverted) != 1 || reverted[0] != "2_b" {
		t.Errorf("Reset() reverted %q, want [2_b] before the failure", reverted)
	}

	st, _ := m.Status(ctx)
	if len(st) != 2 || !st[0].Ran || st[1].Ran {
		t.Errorf("Status() = %+v, want 1_a still applied, 2_b reverted", st)
	}
}

func TestAlterErrorsOnSQLite(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		fn   func(s *Schema) error
		want string
	}{
		{"add column to missing table", func(s *Schema) error {
			return s.Table("nope", func(t *Table) { t.String("x") })
		}, "no such table"},
		{"add primary key", func(s *Schema) error {
			return s.Table("users", func(t *Table) { t.ID() })
		}, "cannot add a primary key"},
		{"rename missing column", func(s *Schema) error {
			return s.Table("users", func(t *Table) { t.RenameColumn("ghost", "spirit") })
		}, "ghost"},
		{"rename to invalid name", func(s *Schema) error {
			return s.Table("users", func(t *Table) { t.RenameColumn("name", "bad name") })
		}, "invalid column name"},
		{"drop missing index", func(s *Schema) error {
			return s.Table("users", func(t *Table) { t.DropIndex("ghost_index") })
		}, "no such index"},
		{"drop missing column", func(s *Schema) error {
			return s.Table("users", func(t *Table) { t.DropColumn("ghost") })
		}, "ghost"},
		{"unique index over duplicate rows", func(s *Schema) error {
			if err := s.Raw(`INSERT INTO users (name, email) VALUES ('ana', 'a@x'), ('ana', 'b@x')`); err != nil {
				return err
			}

			return s.Table("users", func(t *Table) { t.UniqueIndex("name") })
		}, "UNIQUE constraint failed"},
		{"create with invalid column", func(s *Schema) error {
			return s.Create("things", func(t *Table) { t.String("bad-name") })
		}, "invalid column name"},
		{"create existing table", func(s *Schema) error {
			return s.Create("users", func(t *Table) { t.ID() })
		}, "already exists"},
		{"restrict without constrained", func(s *Schema) error {
			return s.Create("things", func(t *Table) { t.BigInteger("user_id").RestrictOnDelete() })
		}, "call Constrained()"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			db := openDB(t)
			s := New(t.Context(), db)

			if err := usersPosts(s); err != nil {
				t.Fatal(err)
			}

			if err := tt.fn(s); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("%s = %v, want error containing %q", tt.name, err, tt.want)
			}
		})
	}
}

func TestColumnDDLDetails(t *testing.T) {
	t.Parallel()

	type alias string

	define := func(s *Schema) error {
		return s.Create("events", func(t *Table) {
			t.ID()
			t.BigInteger("views").Default(int64(0))
			t.ForeignID("user_id").Constrained().RestrictOnDelete()
			t.Float("ratio").Default(float32(0.5))
			t.JSON("meta").Default("{}")
			t.Timestamp("seen_at").Default("2026-01-01 00:00:00")
			t.String("label").Default(alias("it's"))
		})
	}

	tests := []struct {
		dialect database.Dialect
		want    []string
	}{
		{database.SQLite, []string{
			`"views" INTEGER NOT NULL DEFAULT 0`, `"ratio" REAL NOT NULL DEFAULT 0.5`,
			`REFERENCES "users" ("id") ON DELETE RESTRICT`, `DEFAULT 'it''s'`,
		}},
		{database.MySQL, []string{
			"`views` BIGINT NOT NULL DEFAULT 0", "`meta` JSON NOT NULL DEFAULT ('{}')",
			"`seen_at` DATETIME(6) NOT NULL DEFAULT '2026-01-01 00:00:00'", "ON DELETE RESTRICT",
		}},
	}

	for _, tt := range tests {
		t.Run(tt.dialect.Name(), func(t *testing.T) {
			t.Parallel()

			ddl := strings.Join(record(t, tt.dialect, define), "\n")
			for _, w := range tt.want {
				if !strings.Contains(ddl, w) {
					t.Errorf("DDL does not contain %q:\n%s", w, ddl)
				}
			}
		})
	}
}

// VF-017 (HexaScore): statements a transaction refuses run in a
// NoTransaction migration and are recorded like the others.
func TestNoTransactionMigration(t *testing.T) {
	t.Parallel()

	vacuum := func(noTx bool) Migration {
		return Migration{
			ID:            "20261029000000_vacuum",
			Up:            func(s *Schema) error { return s.Raw("VACUUM") },
			Down:          func(s *Schema) error { return s.Raw("VACUUM") },
			NoTransaction: noTx,
		}
	}

	inTx, _ := NewMigrator(openDB(t), []Migration{vacuum(false)})
	if _, err := inTx.Up(t.Context()); err == nil || !strings.Contains(err.Error(), "transaction") {
		t.Fatalf("VACUUM in a transaction = %v, want SQLite's refusal", err)
	}

	m, _ := NewMigrator(openDB(t), []Migration{vacuum(true)})

	ran, err := m.Up(t.Context())
	if err != nil || len(ran) != 1 {
		t.Fatalf("Up() = %v, %v, want the NoTransaction migration to run", ran, err)
	}

	if st, err := m.Status(t.Context()); err != nil || len(st) != 1 || !st[0].Ran {
		t.Fatalf("Status() = %+v, %v, want it recorded", st, err)
	}

	if back, err := m.Rollback(t.Context(), 0); err != nil || len(back) != 1 {
		t.Fatalf("Rollback() = %v, %v", back, err)
	}
}

// VF-026 (HexaScore): on PostgreSQL and MySQL every run takes a lock
// first; when it cannot (here: SQLite has no such function) nothing runs.
func TestMigrationLockFailsClosed(t *testing.T) {
	t.Parallel()

	for _, dl := range []database.Dialect{database.Postgres, database.MySQL} {
		db, _ := serverDialectDB(t, dl)
		m, _ := NewMigrator(db, migrations())

		runs := map[string]func() error{
			"Up":       func() error { _, err := m.Up(t.Context()); return err },
			"Rollback": func() error { _, err := m.Rollback(t.Context(), 0); return err },
			"Reset":    func() error { _, err := m.Reset(t.Context()); return err },
			"Fresh":    func() error { _, err := m.Fresh(t.Context()); return err },
		}

		for name, run := range runs {
			if err := run(); err == nil || !strings.Contains(err.Error(), "schema: lock migrations") {
				t.Errorf("%s %s() = %v, want the lock error", dl.Name(), name, err)
			}
		}

		if tables := tableNames(t, db); len(tables) != 0 {
			t.Errorf("%s: tables created without the lock: %v", dl.Name(), tables)
		}
	}
}
