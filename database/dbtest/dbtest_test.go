package dbtest

import (
	"path/filepath"
	"testing"

	"github.com/AlexGalhardo/golang-framework/database"
)

func TestSQLiteConformance(t *testing.T) {
	t.Parallel()

	for name, url := range map[string]string{
		"memory": ":memory:",
		"file":   "file:" + filepath.ToSlash(filepath.Join(t.TempDir(), "conformance.db")),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			db, err := database.Open(t.Context(), url)
			if err != nil {
				t.Fatal(err)
			}

			t.Cleanup(func() { _ = db.Close() })

			Run(t, db)
		})
	}
}
