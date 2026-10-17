//go:build vukem_nosqlite

package database

import (
	"strings"
	"testing"
)

// Built with -tags vukem_nosqlite, the binary has no SQLite driver: opening
// a SQLite URL fails with an error that names the tag.
func TestOpenSQLiteWithoutDriver(t *testing.T) {
	t.Parallel()

	for _, url := range []string{":memory:", "file:storage/app.db", "sqlite:app.db"} {
		db, err := Open(t.Context(), url)
		if err == nil {
			_ = db.Close()
			t.Fatalf("Open(%q) = nil error, want one naming vukem_nosqlite", url)
		}

		if !strings.Contains(err.Error(), "vukem_nosqlite") {
			t.Errorf("Open(%q) error = %v, want it to name vukem_nosqlite", url, err)
		}
	}
}
