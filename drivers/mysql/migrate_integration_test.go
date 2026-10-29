//go:build integration

package mysql

import (
	"context"
	"sync"
	"testing"

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
	each(t, func(t *testing.T, db *database.DB) {
		t.Helper()
		testConcurrentMigrate(t, db, "SELECT SLEEP(0.5)")
	})
}
