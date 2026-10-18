package schema

import (
	"errors"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/AlexGalhardo/vukem/database"
)

// TestRecord: a migration's SQL can be read for any dialect without a
// database, so DDL shipped twice (a migration and a store's boot-time
// CREATE TABLE) can be diffed in a test and reviewed by hand.
func TestRecord(t *testing.T) {
	t.Parallel()

	up := func(s *Schema) error {
		if has, err := s.HasTable("jobs"); err != nil || has {
			return err
		}

		return s.Raw("CREATE TABLE IF NOT EXISTS " + s.SQLDialect().Quote("jobs") + " (id TEXT PRIMARY KEY)")
	}

	for _, tc := range []struct {
		dialect database.Dialect
		want    []string
	}{
		{database.SQLite, []string{`CREATE TABLE IF NOT EXISTS "jobs" (id TEXT PRIMARY KEY)`}},
		{database.Postgres, []string{`CREATE TABLE IF NOT EXISTS "jobs" (id TEXT PRIMARY KEY)`}},
		{database.MySQL, []string{"CREATE TABLE IF NOT EXISTS `jobs` (id TEXT PRIMARY KEY)"}},
	} {
		got, err := Record(tc.dialect, up)
		if err != nil {
			t.Fatalf("Record(%s) = %v", tc.dialect.Name(), err)
		}

		if diff := cmp.Diff(tc.want, got); diff != "" {
			t.Errorf("Record(%s) statements (-want +got):\n%s", tc.dialect.Name(), diff)
		}
	}
}

// TestRecordReturnsUpError: a migration that cannot build its SQL reports
// the error instead of a partial statement list.
func TestRecordReturnsUpError(t *testing.T) {
	t.Parallel()

	fail := errors.New("boom")

	got, err := Record(database.SQLite, func(*Schema) error { return fail })
	if !errors.Is(err, fail) {
		t.Errorf("Record() error = %v, want %v", err, fail)
	}

	if got != nil {
		t.Errorf("Record() statements = %v, want nil", got)
	}
}
