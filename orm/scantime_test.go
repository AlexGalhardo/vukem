package orm

import (
	"database/sql"
	"testing"
	"time"
)

// textTimes maps to a table whose timestamp columns are declared TEXT (or
// carry no type), so SQLite returns them as text, as it does for MIN/MAX.
type textTimes struct {
	ID    int64
	At    time.Time
	Maybe *time.Time
	Opt   sql.NullTime
	Gen   sql.Null[time.Time]
	Soft  NullTime
}

func (textTimes) Table() string { return "text_times" }

func TestSQLiteTextTimestampsScanIntoTimeFields(t *testing.T) {
	t.Parallel()

	db, ctx := setup(t)

	if _, err := db.ExecContext(ctx, `CREATE TABLE text_times (id INTEGER PRIMARY KEY, at TEXT, maybe, opt TEXT,
		gen TEXT, soft TEXT)`); err != nil {
		t.Fatal(err)
	}

	if _, err := db.ExecContext(ctx, `INSERT INTO text_times VALUES
		(1, '2026-01-02 03:04:05+00:00', '2026-01-02T03:04:05Z', '2026-01-02 03:04:05', '2026-01-02', NULL),
		(2, datetime('2026-01-02 03:04:05'), NULL, NULL, NULL, '2026-01-02 03:04:05.5 +0000 UTC')`); err != nil {
		t.Fatal(err)
	}

	got, err := QueryOf[textTimes](db).OrderBy("id", Asc).Get(ctx)
	if err != nil {
		t.Fatalf("Get() = %v", err)
	}

	at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	day := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)

	first, second := got[0], got[1]
	if !first.At.Equal(at) || first.Maybe == nil || !first.Maybe.Equal(at) || !first.Opt.Valid ||
		!first.Opt.Time.Equal(at) || !first.Gen.Valid || !first.Gen.V.Equal(day) || first.Soft.Valid {
		t.Errorf("row 1 = %+v", first)
	}

	if !second.At.Equal(at) || second.Maybe != nil || second.Opt.Valid || second.Gen.Valid ||
		!second.Soft.Valid || !second.Soft.V.Equal(at.Add(500*time.Millisecond)) {
		t.Errorf("row 2 = %+v", second)
	}

	maybes, err := QueryOf[textTimes](db).OrderBy("id", Asc).Pluck[*time.Time](ctx, "maybe")
	if err != nil || len(maybes) != 2 || maybes[0] == nil || !maybes[0].Equal(at) || maybes[1] != nil {
		t.Errorf("Pluck(maybe) = %v, %v", maybes, err)
	}
}
