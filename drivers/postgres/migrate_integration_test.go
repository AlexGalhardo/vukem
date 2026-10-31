//go:build integration

package postgres

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/AlexGalhardo/vukem/database"
	"github.com/AlexGalhardo/vukem/schema"
)

// VF-026 (HexaScore): two processes migrating at once (a rolling deploy)
// run one after the other; the second finds nothing pending.
func testConcurrentMigrate(t *testing.T, db *database.DB, sleep string) {
	t.Helper()

	const id, table = "29991231000000_lock_probe", "lock_probe"

	cleanup := func() {
		ctx := context.Background()
		_, _ = db.ExecContext(ctx, "DROP TABLE IF EXISTS "+table)
		_, _ = db.ExecContext(ctx, "DELETE FROM migrations WHERE id = '"+id+"'")
	}
	cleanup()
	t.Cleanup(cleanup)

	mig := schema.Migration{
		ID: id,
		Up: func(s *schema.Schema) error {
			// Slow enough that unlocked runners would both create the table.
			if err := s.Raw(sleep); err != nil {
				return err
			}

			return s.Raw("CREATE TABLE " + table + " (id INTEGER PRIMARY KEY)")
		},
		Down: func(s *schema.Schema) error { return s.Raw("DROP TABLE " + table) },
	}

	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		runs int
		errs []error
	)

	for range 2 {
		wg.Go(func() {
			m, err := schema.NewMigrator(db, []schema.Migration{mig})
			if err == nil {
				var ran []string
				ran, err = m.Up(t.Context())

				mu.Lock()
				runs += len(ran)
				mu.Unlock()
			}

			if err != nil {
				mu.Lock()
				errs = append(errs, err)
				mu.Unlock()
			}
		})
	}

	wg.Wait()

	if len(errs) != 0 || runs != 1 {
		t.Fatalf("concurrent Up: runs = %d, errors = %v; want one run and no error", runs, errs)
	}
}

func TestConcurrentMigrate(t *testing.T) {
	testConcurrentMigrate(t, open(t), "SELECT pg_sleep(0.5)")
}

// VF-054 (HexaScore): a failed CREATE INDEX CONCURRENTLY leaves an INVALID
// index; the retry's IF NOT EXISTS would skip it and record the migration
// with an index that is never used. migrate fails instead, naming it.
func TestMigrateRefusesInvalidIndexAfterNoTransaction(t *testing.T) {
	db := open(t)

	const id, table, index = "29991231000001_invalid_probe", "invalid_probe", "invalid_probe_k_uq"

	cleanup := func() {
		ctx := context.Background()
		_, _ = db.ExecContext(ctx, "DROP TABLE IF EXISTS "+table)
		_, _ = db.ExecContext(ctx, "DELETE FROM migrations WHERE id = '"+id+"'")
	}
	cleanup()
	t.Cleanup(cleanup)

	if _, err := db.ExecContext(t.Context(), "CREATE TABLE "+table+" (k INTEGER); INSERT INTO "+table+
		" VALUES (1), (1)"); err != nil {
		t.Fatal(err)
	}

	mig := schema.Migration{
		ID:            id,
		NoTransaction: true,
		Up: func(s *schema.Schema) error {
			return s.Raw("CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS " + index + " ON " + table + " (k)")
		},
		Down: func(s *schema.Schema) error { return s.Raw("DROP INDEX CONCURRENTLY IF EXISTS " + index) },
	}

	m, err := schema.NewMigrator(db, []schema.Migration{mig})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := m.Up(t.Context()); err == nil {
		t.Fatal("Up() over duplicates = nil, want the unique violation")
	}

	if _, err := db.ExecContext(t.Context(), "DELETE FROM "+table); err != nil {
		t.Fatal(err)
	}

	// The retry: IF NOT EXISTS finds the invalid index and succeeds.
	ran, err := m.Up(t.Context())
	if err == nil || !strings.Contains(err.Error(), index) || !strings.Contains(err.Error(), "INVALID") ||
		len(ran) != 0 {
		t.Fatalf("Up() retry = %v, %v; want an INVALID index error naming %s and nothing recorded", ran, err, index)
	}

	// Dropping it, as the error says, lets the migration finish.
	if _, err := db.ExecContext(t.Context(), "DROP INDEX CONCURRENTLY "+index); err != nil {
		t.Fatal(err)
	}

	if ran, err := m.Up(t.Context()); err != nil || len(ran) != 1 {
		t.Fatalf("Up() after the drop = %v, %v; want it recorded", ran, err)
	}
}

// VF-NEW-migrator-lock-snapshot (HexaScore): a runner waiting for the lock
// must not hold a snapshot, or the holder's CREATE INDEX CONCURRENTLY waits
// for it and both hang with no deadlock reported.
func TestConcurrentMigrateWithConcurrentIndex(t *testing.T) {
	db := open(t)

	const id, table, index = "29991231000002_cic_probe", "cic_probe", "cic_probe_k_idx"

	cleanup := func() {
		ctx := context.Background()
		_, _ = db.ExecContext(ctx, "DROP TABLE IF EXISTS "+table)
		_, _ = db.ExecContext(ctx, "DELETE FROM migrations WHERE id = '"+id+"'")
	}
	cleanup()
	t.Cleanup(cleanup)

	if _, err := db.ExecContext(t.Context(), "CREATE TABLE "+table+" (k INTEGER)"); err != nil {
		t.Fatal(err)
	}

	mig := schema.Migration{
		ID:            id,
		NoTransaction: true,
		Up: func(s *schema.Schema) error {
			// Lets the other runner reach the lock before the index build starts.
			if err := s.Raw("SELECT pg_sleep(0.5)"); err != nil {
				return err
			}

			return s.Raw("CREATE INDEX CONCURRENTLY IF NOT EXISTS " + index + " ON " + table + " (k)")
		},
		Down: func(s *schema.Schema) error { return s.Raw("DROP INDEX CONCURRENTLY IF EXISTS " + index) },
	}

	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()

	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		runs int
		errs []error
	)

	for range 2 {
		wg.Go(func() {
			m, err := schema.NewMigrator(db, []schema.Migration{mig})
			if err == nil {
				var ran []string
				ran, err = m.Up(ctx)

				mu.Lock()
				runs += len(ran)
				mu.Unlock()
			}

			if err != nil {
				mu.Lock()
				errs = append(errs, err)
				mu.Unlock()
			}
		})
	}

	wg.Wait()

	if len(errs) != 0 || runs != 1 {
		t.Fatalf("concurrent Up: runs = %d, errors = %v; want one run and no error", runs, errs)
	}
}
