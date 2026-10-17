package dbtest_test

import (
	"testing"

	"github.com/AlexGalhardo/vukem/database"
	"github.com/AlexGalhardo/vukem/database/dbtest"
)

// A driver module runs the suite from a test against a throwaway database.
func ExampleRun() {
	_ = func(t *testing.T) {
		t.Helper()

		db, err := database.Open(t.Context(), ":memory:")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = db.Close() })

		dbtest.Run(t, db)
	}
}
