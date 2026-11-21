package studio

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/AlexGalhardo/vukem/database"
)

// gridFirstPageBudget is the server time the first page of a large table may take.
const gridFirstPageBudget = 150 * time.Millisecond

func TestGridFirstPageBudget(t *testing.T) {
	t.Parallel()

	db, err := database.Open(t.Context(), "file:"+filepath.Join(t.TempDir(), "big.db"))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = db.Close() })

	for _, q := range []string{
		`CREATE TABLE events (id INTEGER PRIMARY KEY, kind TEXT NOT NULL, amount REAL, active BOOLEAN, at DATETIME,
			payload JSON, note TEXT)`,
		`WITH RECURSIVE seq(n) AS (SELECT 1 UNION ALL SELECT n + 1 FROM seq WHERE n < 100000)
			INSERT INTO events (id, kind, amount, active, at, payload, note)
			SELECT n, 'kind-' || (n % 10), n * 1.5, n % 2, datetime('2026-01-01', '+' || n || ' minutes'),
				'{"n":' || n || '}', 'note ' || n FROM seq`,
	} {
		if _, err := db.ExecContext(t.Context(), q); err != nil {
			t.Fatal(err)
		}
	}

	src := NewSQL("db", "sqlite file", db, fakeCatalog{{
		Name: "events",
		Columns: []Column{
			{Name: "id", Type: "integer"},
			{Name: "kind", Type: "text"},
			{Name: "amount", Type: "real", Nullable: true},
			{Name: "active", Type: "boolean", Nullable: true},
			{Name: "at", Type: "datetime", Nullable: true},
			{Name: "payload", Type: "json", Nullable: true},
			{Name: "note", Type: "text", Nullable: true},
		},
		PrimaryKey: []string{"id"},
	}})

	// The first request warms the connection; the budget is for a page after it,
	// the way a person opening a table sees it. The median of five keeps one slow
	// scheduling hiccup from failing the budget.
	if _, err := src.Rows(t.Context(), RowQuery{Table: "events", PerPage: 100}); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name   string
		q      RowQuery
		budget time.Duration
	}{
		{"keyset page", RowQuery{Table: "events", PerPage: 100}, gridFirstPageBudget},
		{"keyset, descending", RowQuery{Table: "events", PerPage: 100, Desc: true}, gridFirstPageBudget},
		{
			"filtered keyset page",
			RowQuery{Table: "events", PerPage: 100, Filters: []Filter{{Column: "kind", Op: "eq", Value: "kind-3"}}},
			gridFirstPageBudget,
		},
		// A sort on a column without an index reads the whole table. Under the race detector it is
		// timed but not held to the budget (a budget of zero).
		{"sorted, offset page 1", RowQuery{Table: "events", PerPage: 100, Sort: "amount", Page: 1}, sortedBudget()},
	} {
		name, q := tc.name, tc.q

		times := make([]time.Duration, 5)

		for i := range times {
			start := time.Now()

			pg, err := src.Rows(t.Context(), q)
			if err != nil {
				t.Fatalf("%s: %v", name, err)
			}

			times[i] = time.Since(start)

			if len(pg.Rows) != 100 || !pg.HasMore {
				t.Fatalf("%s: got %d rows (more = %v), want a full first page", name, len(pg.Rows), pg.HasMore)
			}
		}

		median := sortedDurations(times)[len(times)/2]
		t.Logf("%s: median %v of %v", name, median, times)

		if tc.budget > 0 && median > tc.budget {
			t.Errorf("%s: first page took %v, budget %v", name, median, tc.budget)
		}
	}
}

func sortedBudget() time.Duration {
	if raceEnabled {
		return 0
	}

	return gridFirstPageBudget
}

func sortedDurations(ds []time.Duration) []time.Duration {
	out := append([]time.Duration(nil), ds...)

	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}

	return out
}
