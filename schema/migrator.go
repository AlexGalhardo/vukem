package schema

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/AlexGalhardo/golang-framework/database"
)

// Migration is one versioned schema change. IDs sort chronologically:
// "20261016120000_create_users_table".
type Migration struct {
	ID   string
	Up   func(s *Schema) error
	Down func(s *Schema) error
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
// transaction. It returns the IDs that ran.
func (m *Migrator) Up(ctx context.Context) ([]string, error) {
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

		err = m.db.Tx(ctx, func(tx *database.Tx) error {
			if err := mig.Up(New(ctx, tx)); err != nil {
				return err
			}

			_, err := tx.ExecContext(ctx, "INSERT INTO "+m.table+" (id, batch, checksum, ran_at) VALUES ("+
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

// Rollback reverts the last batch, or the last steps migrations when
// steps > 0. It returns the IDs that were reverted, newest first.
func (m *Migrator) Rollback(ctx context.Context, steps int) ([]string, error) {
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

		err := m.db.Tx(ctx, func(tx *database.Tx) error {
			if err := mig.Down(New(ctx, tx)); err != nil {
				return err
			}

			_, err := tx.ExecContext(ctx, "DELETE FROM "+m.table+" WHERE id = "+m.ph(1), id)

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
	if err := dropAll(ctx, m.db); err != nil {
		return nil, err
	}

	return m.Up(ctx)
}

func dropAll(ctx context.Context, db *database.DB) error {
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

	var tables []string

	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			_ = rows.Close()
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
