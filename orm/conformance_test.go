package orm_test

import (
	"testing"

	"github.com/AlexGalhardo/vukem/database"
	"github.com/AlexGalhardo/vukem/database/dbtest"
)

// The dialect conformance suite also exercises relations, pivots and
// soft deletes end to end; running it here keeps orm's own coverage honest.
func TestConformanceSQLite(t *testing.T) {
	t.Parallel()

	db, err := database.Open(t.Context(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = db.Close() })

	dbtest.Run(t, db)
}
