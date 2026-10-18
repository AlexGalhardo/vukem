package schema

import (
	"context"
	"testing"

	"github.com/AlexGalhardo/vukem/database"
)

// TestSQLDialect: a Schema hands out the dialect it builds DDL for, so a
// store can render one set of statements for both its boot-time
// CREATE TABLE and its migration.
func TestSQLDialect(t *testing.T) {
	t.Parallel()

	for _, dl := range []database.Dialect{database.SQLite, database.Postgres, database.MySQL} {
		rec, _ := recorder(dl)
		if got := rec.SQLDialect(); got != dl {
			t.Errorf("recorder(%s).SQLDialect() = %v, want %v", dl.Name(), got, dl)
		}

		s := New(context.Background(), database.FromSQL(nil, dl))
		if got := s.SQLDialect(); got != dl {
			t.Errorf("New(%s).SQLDialect() = %v, want %v", dl.Name(), got, dl)
		}

		if got := s.SQLDialect().Quote("jobs"); got != dl.Quote("jobs") {
			t.Errorf("SQLDialect().Quote() = %q, want %q", got, dl.Quote("jobs"))
		}
	}
}
