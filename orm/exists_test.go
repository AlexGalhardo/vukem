package orm

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"github.com/AlexGalhardo/vukem/database"
)

// rowSpyEx records the statements sent through QueryRowContext.
type rowSpyEx struct {
	database.Executor

	rows []string
}

func (s *rowSpyEx) QueryRowContext(ctx context.Context, q string, args ...any) *sql.Row {
	s.rows = append(s.rows, q)
	return s.Executor.QueryRowContext(ctx, q, args...)
}

func TestExistsStopsAtFirstRow(t *testing.T) {
	t.Parallel()

	db, ctx := setup(t)
	spy := &rowSpyEx{Executor: db}

	q := QueryOf[User](spy).Where("age", ">", 1).OrderBy("id", Asc).Offset(3).LockForUpdate()
	if _, err := q.Exists(ctx); err != nil {
		t.Fatal(err)
	}

	if len(spy.rows) != 1 {
		t.Fatalf("Exists() sent %d statements, want 1", len(spy.rows))
	}

	got := spy.rows[0]
	bad := !strings.HasPrefix(got, "SELECT 1 FROM") || !strings.HasSuffix(got, "LIMIT 1")
	if bad || strings.Contains(got, "COUNT(") {
		t.Fatalf("Exists() SQL = %q, want SELECT 1 FROM ... LIMIT 1 without COUNT(", got)
	}
}

func TestExistsBehaviour(t *testing.T) {
	t.Parallel()

	db, ctx := setup(t)

	if got, err := QueryOf[User](db).Exists(ctx); err != nil || got {
		t.Fatalf("Exists() on empty table = %v, %v, want false, nil", got, err)
	}

	users := seedUsers(ctx, t, db)
	if got, err := QueryOf[User](db).Where("name", "=", "Ana").Exists(ctx); err != nil || !got {
		t.Fatalf("Exists() with a match = %v, %v, want true, nil", got, err)
	}

	if got, err := QueryOf[User](db).Where("name", "=", "Zed").Exists(ctx); err != nil || got {
		t.Fatalf("Exists() without a match = %v, %v, want false, nil", got, err)
	}

	post := Post{UserID: users[0].ID, Title: "only"}
	if err := Create(ctx, db, &post); err != nil {
		t.Fatal(err)
	}

	if got, err := QueryOf[Post](db).Exists(ctx); err != nil || !got {
		t.Fatalf("Exists() with a live post = %v, %v, want true, nil", got, err)
	}

	if err := Delete(ctx, db, &post); err != nil {
		t.Fatal(err)
	}

	if got, err := QueryOf[Post](db).Exists(ctx); err != nil || got {
		t.Fatalf("Exists() with only a soft-deleted post = %v, %v, want false, nil", got, err)
	}
}
