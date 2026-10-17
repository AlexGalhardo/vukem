package dbtest

import (
	"path/filepath"
	"testing"

	"github.com/AlexGalhardo/vukem/database"
	"github.com/AlexGalhardo/vukem/internal/testutil"
)

func TestSQLiteConformance(t *testing.T) {
	t.Parallel()

	for name, url := range map[string]string{
		"memory": ":memory:",
		"file":   "file:" + filepath.ToSlash(filepath.Join(testutil.TempDir(t), "conformance.db")),
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
