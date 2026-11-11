package orm

import (
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/AlexGalhardo/vukem/database"
)

func TestRaw(t *testing.T) {
	t.Parallel()

	db, ctx := qaSetup(t)

	type row struct {
		Title string
		Views int64
	}

	rows, err := Raw[row](ctx, db, `SELECT title, views FROM qa_books WHERE views > ? ORDER BY views DESC`, 20)
	if err != nil || !cmp.Equal(rows, []row{{"Old", 999}, {"Go", 100}, {"SQL", 50}}) {
		t.Errorf("Raw struct = %+v, %v", rows, err)
	}

	ids, err := Raw[int64](ctx, db, `SELECT id FROM qa_authors ORDER BY id`)
	if err != nil || !cmp.Equal(ids, []int64{1, 2, 3}) {
		t.Errorf("Raw scalar = %v, %v", ids, err)
	}

	var streamed int

	for _, err := range RawEach[int64](ctx, db, `SELECT id FROM qa_authors`) {
		if err != nil {
			t.Fatal(err)
		}

		streamed++
	}

	if streamed != 3 {
		t.Errorf("RawEach streamed %d rows, want 3", streamed)
	}
}

func TestRawErrors(t *testing.T) {
	t.Parallel()

	db, ctx := qaSetup(t)

	type row struct{ Title string }

	tests := []struct {
		name  string
		query string
		args  []any
		want  string
	}{
		{"extra result column", `SELECT title, views FROM qa_books`, nil, `result column "views" has no field`},
		{"unterminated literal", `SELECT 'x FROM qa_books`, nil, "orm: raw:"},
		{"scalar from two columns", `SELECT 1, 2`, nil, "one column"},
	}

	for _, tt := range tests {
		var err error
		if tt.name == "scalar from two columns" {
			_, err = Raw[int64](ctx, db, tt.query, tt.args...)
		} else {
			_, err = Raw[row](ctx, db, tt.query, tt.args...)
		}

		if err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("%s: err = %v, want it to contain %q", tt.name, err, tt.want)
		}
	}

	// The query text stays out of the error beyond the truncated prefix.
	long := `SELECT '` + strings.Repeat("secret", 40)
	if _, err := Raw[row](ctx, db, long); err == nil || strings.Contains(err.Error(), strings.Repeat("secret", 20)) {
		t.Errorf("unterminated long query error = %v; it must not echo the whole query", err)
	}
}

func TestRawMixedPlaceholdersOnPostgres(t *testing.T) {
	t.Parallel()

	_, err := Raw[int64](t.Context(), fakeEx{database.Postgres}, `SELECT id FROM t WHERE a = ? AND b = $1`, 1, 2)
	if err == nil || !strings.Contains(err.Error(), "orm: raw:") {
		t.Errorf("mixed placeholders = %v, want an orm: raw: error", err)
	}
}

func TestRawPostgresPlaceholders(t *testing.T) {
	t.Parallel()

	got, err := database.RebindChecked(database.Postgres, `SELECT id FROM t WHERE a = ? AND b = ?`)
	if err != nil || got != `SELECT id FROM t WHERE a = $1 AND b = $2` {
		t.Errorf("RebindChecked = %q, %v", got, err)
	}
}

func TestRawTimeOnSQLite(t *testing.T) {
	t.Parallel()

	db, ctx := setup(t)
	if err := Create(ctx, db, &User{Name: "Ana", Email: "ana@x.io"}); err != nil {
		t.Fatal(err)
	}

	type row struct {
		Name      string
		CreatedAt time.Time
	}

	rows, err := Raw[row](ctx, db, `SELECT name, created_at FROM users`)
	if err != nil || len(rows) != 1 || rows[0].CreatedAt.IsZero() {
		t.Errorf("Raw with a time = %+v, %v", rows, err)
	}
}
