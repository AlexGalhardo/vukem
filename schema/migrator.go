package schema

import (
	"cmp"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"hash/fnv"
	"slices"
	"strings"
	"time"

	"github.com/AlexGalhardo/vukem/database"
)

// Migration is one versioned schema change. IDs sort chronologically:
// "20261016120000_create_users_table".
type Migration struct {
	ID   string
	Up   func(s *Schema) error
	Down func(s *Schema) error
	// NoTransaction runs Up and Down outside a transaction, for statements
	// a transaction refuses (PostgreSQL CREATE INDEX CONCURRENTLY, SQLite
	// VACUUM). The migration is recorded after Up succeeds; a failure
	// half-way leaves the statements that ran, so keep such migrations to
	// one statement, written to be re-run (IF NOT EXISTS).
	NoTransaction bool
}

// Status describes a migration's state.
type Status struct {
	ID      string    `json:"id"`
	Ran     bool      `json:"ran"`
	Batch   int       `json:"batch,omitempty"`
	RanAt   time.Time `json:"ran_at,omitzero"`
	Changed bool      `json:"changed,omitempty"` // applied migration whose SQL changed since
	Missing bool      `json:"missing,omitempty"` // recorded in the database but not in code
}

// Migrator applies migrations to a database.
type Migrator struct {
	db         *database.DB
	migrations []Migration
	table      string
}

// NewMigrator validates migrations (unique, non-empty IDs, Up present) and
// sorts them by ID.
func NewMigrator(db *database.DB, migrations []Migration) (*Migrator, error) {
	seen := map[string]bool{}

	for _, m := range migrations {
		if m.ID == "" || m.Up == nil {
			return nil, fmt.Errorf("schema: migration %q needs an ID and Up", m.ID)
		}

		if seen[m.ID] {
			return nil, fmt.Errorf("schema: duplicate migration %q", m.ID)
		}

		seen[m.ID] = true
	}

	sorted := slices.Clone(migrations)
	slices.SortFunc(sorted, func(a, b Migration) int { return cmp.Compare(a.ID, b.ID) })

	return &Migrator{db: db, migrations: sorted, table: "migrations"}, nil
}

type applied struct {
	batch    int
	checksum string
	ranAt    time.Time
}

func (m *Migrator) ensureTable(ctx context.Context) error {
	_, err := m.db.ExecContext(ctx, "CREATE TABLE IF NOT EXISTS "+m.table+
		" (id VARCHAR(255) PRIMARY KEY, batch INTEGER NOT NULL, checksum VARCHAR(64) NOT NULL, ran_at TIMESTAMP NOT NULL)")
	if err != nil {
		return fmt.Errorf("schema: create migrations table: %w", err)
	}

	return nil
}

func (m *Migrator) applied(ctx context.Context) (map[string]applied, error) {
	if err := m.ensureTable(ctx); err != nil {
		return nil, err
	}

	rows, err := m.db.QueryContext(ctx, "SELECT id, batch, checksum, ran_at FROM "+m.table)
	if err != nil {
		return nil, fmt.Errorf("schema: read migrations: %w", err)
	}
	defer rows.Close()

	out := map[string]applied{}

	for rows.Next() {
		var (
			id string
			a  applied
		)

		if err := rows.Scan(&id, &a.batch, &a.checksum, &a.ranAt); err != nil {
			return nil, fmt.Errorf("schema: read migrations: %w", err)
		}

		out[id] = a
	}

	return out, rows.Err()
}

// Checksum returns the SHA-256 of the SQL a migration's Up generates for
// the database's dialect.
func (m *Migrator) Checksum(mig Migration) (string, error) {
	rec, stmts := recorder(m.db.Dialect())
	if err := mig.Up(rec); err != nil {
		return "", fmt.Errorf("schema: checksum %s: %w", mig.ID, err)
	}

	sum := sha256.Sum256([]byte(strings.Join(*stmts, ";\n")))

	return hex.EncodeToString(sum[:]), nil
}

func (m *Migrator) ph(n int) string { return m.db.Dialect().Placeholder(n) }

// Up runs every pending migration in one new batch, each in its own
// transaction (unless NoTransaction). It returns the IDs that ran.
//
// Up, Rollback, Reset and Fresh hold a lock for the whole run, so two
// processes migrating at once (a rolling deploy) run one after the other
// and the second finds nothing pending: a PostgreSQL advisory lock or a
// MySQL GET_LOCK, keyed by the migrations table. They wait for it until
// ctx is done, and fail without migrating when it cannot be taken. SQLite
// takes no lock: run its migrations from one process.
func (m *Migrator) Up(ctx context.Context) ([]string, error) {
	var ran []string

	err := m.locked(ctx, func() (err error) {
		ran, err = m.up(ctx)
		return err
	})

	return ran, err
}

func (m *Migrator) up(ctx context.Context) ([]string, error) {
	done, err := m.applied(ctx)
	if err != nil {
		return nil, err
	}

	batch := 1
	for _, a := range done {
		batch = max(batch, a.batch+1)
	}

	var ran []string

	for _, mig := range m.migrations {
		if _, ok := done[mig.ID]; ok {
			continue
		}

		sum, err := m.Checksum(mig)
		if err != nil {
			return ran, err
		}

		err = m.inTx(ctx, mig, func(ex database.Executor) error {
			if err := mig.Up(New(ctx, ex)); err != nil {
				return err
			}

			if mig.NoTransaction {
				if err := m.checkInvalidIndexes(ctx); err != nil {
					return err
				}
			}

			_, err := ex.ExecContext(ctx, "INSERT INTO "+m.table+" (id, batch, checksum, ran_at) VALUES ("+
				m.ph(1)+", "+m.ph(2)+", "+m.ph(3)+", "+m.ph(4)+")", mig.ID, batch, sum, time.Now().UTC())

			return err
		})
		if err != nil {
			return ran, fmt.Errorf("schema: migrate %s: %w", mig.ID, err)
		}

		ran = append(ran, mig.ID)
	}

	return ran, nil
}

// checkInvalidIndexes fails when the current PostgreSQL schema holds an
// INVALID index that no backend is building. A failed CREATE INDEX
// CONCURRENTLY leaves one behind, and the retry's IF NOT EXISTS skips it:
// recording the migration then would leave an index that is never used.
func (m *Migrator) checkInvalidIndexes(ctx context.Context) error {
	if m.db.Dialect().Name() != "postgres" {
		return nil
	}

	rows, err := m.db.QueryContext(ctx, `SELECT c.relname FROM pg_index i
		JOIN pg_class c ON c.oid = i.indexrelid
		JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE NOT i.indisvalid AND n.nspname = current_schema()
		AND i.indexrelid NOT IN (SELECT index_relid FROM pg_stat_progress_create_index)
		ORDER BY c.relname`)
	if err != nil {
		return fmt.Errorf("check invalid indexes: %w", err)
	}
	defer rows.Close()

	var names []string

	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return err
		}

		names = append(names, name)
	}

	if err := rows.Err(); err != nil {
		return err
	}

	if len(names) > 0 {
		return fmt.Errorf("INVALID index %s left by a failed CREATE INDEX CONCURRENTLY: "+
			"run DROP INDEX CONCURRENTLY on it, fix the cause and migrate again", strings.Join(names, ", "))
	}

	return nil
}

// inTx runs fn in a transaction, or on the database itself for a
// NoTransaction migration.
func (m *Migrator) inTx(ctx context.Context, mig Migration, fn func(ex database.Executor) error) error {
	if mig.NoTransaction {
		return fn(m.db)
	}

	return m.db.Tx(ctx, func(tx *database.Tx) error { return fn(tx) })
}

// Rollback reverts the last batch, or the last steps migrations when
// steps > 0. It returns the IDs that were reverted, newest first.
func (m *Migrator) Rollback(ctx context.Context, steps int) ([]string, error) {
	var out []string

	err := m.locked(ctx, func() (err error) {
		out, err = m.rollback(ctx, steps)
		return err
	})

	return out, err
}

func (m *Migrator) rollback(ctx context.Context, steps int) ([]string, error) {
	done, err := m.applied(ctx)
	if err != nil {
		return nil, err
	}

	var ids []string
	for id := range done {
		ids = append(ids, id)
	}

	slices.SortFunc(ids, func(a, b string) int {
		if c := cmp.Compare(done[b].batch, done[a].batch); c != 0 {
			return c
		}

		return cmp.Compare(b, a)
	})

	if len(ids) == 0 {
		return nil, nil
	}

	var targets []string

	if steps > 0 {
		targets = ids[:min(steps, len(ids))]
	} else {
		last := done[ids[0]].batch
		for _, id := range ids {
			if done[id].batch == last {
				targets = append(targets, id)
			}
		}
	}

	return m.revert(ctx, targets)
}

// Reset reverts every applied migration.
func (m *Migrator) Reset(ctx context.Context) ([]string, error) {
	var out []string

	err := m.locked(ctx, func() (err error) {
		out, err = m.reset(ctx)
		return err
	})

	return out, err
}

func (m *Migrator) reset(ctx context.Context) ([]string, error) {
	done, err := m.applied(ctx)
	if err != nil {
		return nil, err
	}

	var ids []string
	for id := range done {
		ids = append(ids, id)
	}

	slices.SortFunc(ids, func(a, b string) int {
		if c := cmp.Compare(done[b].batch, done[a].batch); c != 0 {
			return c
		}

		return cmp.Compare(b, a)
	})

	return m.revert(ctx, ids)
}

func (m *Migrator) revert(ctx context.Context, ids []string) ([]string, error) {
	byID := map[string]Migration{}
	for _, mig := range m.migrations {
		byID[mig.ID] = mig
	}

	var reverted []string

	for _, id := range ids {
		mig, ok := byID[id]
		if !ok {
			return reverted, fmt.Errorf("schema: migration %s is recorded but missing from code", id)
		}

		if mig.Down == nil {
			return reverted, fmt.Errorf("schema: migration %s has no Down", id)
		}

		err := m.inTx(ctx, mig, func(ex database.Executor) error {
			if err := mig.Down(New(ctx, ex)); err != nil {
				return err
			}

			_, err := ex.ExecContext(ctx, "DELETE FROM "+m.table+" WHERE id = "+m.ph(1), id)

			return err
		})
		if err != nil {
			return reverted, fmt.Errorf("schema: rollback %s: %w", id, err)
		}

		reverted = append(reverted, id)
	}

	return reverted, nil
}

// Fresh drops every table and runs all migrations. It is destructive: the
// CLI requires --force in production.
func (m *Migrator) Fresh(ctx context.Context) ([]string, error) {
	var ran []string

	err := m.locked(ctx, func() (err error) {
		if err := dropAll(ctx, m.db); err != nil {
			return err
		}

		ran, err = m.up(ctx)

		return err
	})

	return ran, err
}

// locked runs fn while holding the migration lock (see Up).
func (m *Migrator) locked(ctx context.Context, fn func() error) error {
	var lock, unlock string

	var key any

	switch m.db.Dialect().Name() {
	case "postgres":
		lock, unlock = "SELECT pg_advisory_lock($1)", "SELECT pg_advisory_unlock($1)"
		h := fnv.New64a()
		_, _ = h.Write([]byte("vukem:" + m.table))
		key = int64(h.Sum64()) //nolint:gosec // G115: any 64-bit value is a valid key
	case "mysql":
		// A year: in effect no timeout (MariaDB has no "wait forever"); ctx
		// still cancels the wait.
		lock, unlock = "SELECT GET_LOCK(?, 31536000)", "SELECT RELEASE_LOCK(?)"
		key = "vukem:" + m.table
	default:
		return fn()
	}

	// Session locks belong to one connection: take and release it on a
	// pinned one while the migrations use the pool.
	conn, err := m.db.SQL().Conn(ctx)
	if err != nil {
		return fmt.Errorf("schema: lock migrations: %w", err)
	}
	defer conn.Close()

	// Release before anything can return, a failed Scan included: the lock
	// may be held already, and the connection goes back to the pool.
	// Releasing a lock this session does not hold is harmless.
	defer func() {
		_, _ = conn.ExecContext(context.WithoutCancel(ctx), unlock, key)
	}()

	// pg_advisory_lock answers void ("" once it holds the lock); GET_LOCK
	// answers 1 when it holds it.
	var got sql.NullString
	if err := conn.QueryRowContext(ctx, lock, key).Scan(&got); err != nil {
		return fmt.Errorf("schema: lock migrations: %w", err)
	}

	if m.db.Dialect().Name() == "mysql" && got.String != "1" {
		return errors.New("schema: lock migrations: GET_LOCK refused")
	}

	return fn()
}

// DropAll drops every table of the database (db:wipe, migrate:fresh).
func DropAll(ctx context.Context, db *database.DB) error { return dropAll(ctx, db) }

func dropAll(ctx context.Context, db *database.DB) error {
	if db.Dialect().Name() == "mysql" {
		return dropAllMySQL(ctx, db)
	}

	var (
		q       string
		cascade string
	)

	if db.Dialect().Name() == "postgres" {
		q = "SELECT table_name FROM information_schema.tables WHERE table_schema = current_schema() AND table_type = 'BASE TABLE'"
		cascade = " CASCADE"
	} else {
		q = "SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%'"

		if _, err := db.ExecContext(ctx, "PRAGMA foreign_keys = OFF"); err != nil {
			return err
		}

		defer func() { _, _ = db.ExecContext(context.WithoutCancel(ctx), "PRAGMA foreign_keys = ON") }()
	}

	rows, err := db.QueryContext(ctx, q)
	if err != nil {
		return fmt.Errorf("schema: list tables: %w", err)
	}
	// Closed explicitly below before the drops reuse the connection; the
	// defer only covers the early returns (Close is idempotent).
	defer rows.Close()

	var tables []string

	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return err
		}

		tables = append(tables, name)
	}

	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return err
	}

	for _, t := range tables {
		if _, err := db.ExecContext(ctx, "DROP TABLE IF EXISTS "+db.Dialect().Quote(t)+cascade); err != nil {
			return fmt.Errorf("schema: drop %s: %w", t, err)
		}
	}

	return nil
}

// Status lists every migration with its state, including applied
// migrations whose generated SQL changed (edited after running) and
// recorded migrations missing from code.
func (m *Migrator) Status(ctx context.Context) ([]Status, error) {
	done, err := m.applied(ctx)
	if err != nil {
		return nil, err
	}

	out := make([]Status, 0, len(m.migrations))
	known := map[string]bool{}

	for _, mig := range m.migrations {
		known[mig.ID] = true
		st := Status{ID: mig.ID}

		if a, ok := done[mig.ID]; ok {
			st.Ran, st.Batch, st.RanAt = true, a.batch, a.ranAt

			sum, err := m.Checksum(mig)
			if err != nil {
				return nil, err
			}

			st.Changed = sum != a.checksum
		}

		out = append(out, st)
	}

	for id, a := range done {
		if !known[id] {
			out = append(out, Status{ID: id, Ran: true, Batch: a.batch, RanAt: a.ranAt, Missing: true})
		}
	}

	slices.SortFunc(out, func(a, b Status) int { return cmp.Compare(a.ID, b.ID) })

	return out, nil
}

// dropAllMySQL disables foreign key checks, which is a session setting,
// so every statement runs on one pinned connection.
func dropAllMySQL(ctx context.Context, db *database.DB) error {
	conn, err := db.SQL().Conn(ctx)
	if err != nil {
		return fmt.Errorf("schema: connect: %w", err)
	}
	defer conn.Close()

	rows, err := conn.QueryContext(
		ctx,
		"SELECT table_name FROM information_schema.tables WHERE table_schema = DATABASE() AND table_type = 'BASE TABLE'",
	)
	if err != nil {
		return fmt.Errorf("schema: list tables: %w", err)
	}
	// Closed explicitly below before the drops reuse the connection; the
	// defer only covers the early returns (Close is idempotent).
	defer rows.Close()

	var tables []string

	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return err
		}

		tables = append(tables, name)
	}

	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return err
	}

	if _, err := conn.ExecContext(ctx, "SET FOREIGN_KEY_CHECKS = 0"); err != nil {
		return fmt.Errorf("schema: disable foreign keys: %w", err)
	}

	// The connection returns to the pool, so restore the check first.
	defer func() { _, _ = conn.ExecContext(context.WithoutCancel(ctx), "SET FOREIGN_KEY_CHECKS = 1") }()

	for _, t := range tables {
		if _, err := conn.ExecContext(ctx, "DROP TABLE IF EXISTS "+db.Dialect().Quote(t)); err != nil {
			return fmt.Errorf("schema: drop %s: %w", t, err)
		}
	}

	return nil
}
