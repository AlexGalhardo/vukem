package turso_test

import (
	"path/filepath"
	"testing"

	"github.com/AlexGalhardo/vukem/database"
	"github.com/AlexGalhardo/vukem/database/dbtest"
	"github.com/AlexGalhardo/vukem/drivers/turso"
)

// TestConformance runs the dialect suite on a local Turso file.
func TestConformance(t *testing.T) {
	if testing.Short() {
		t.Skip("loads the native Turso library")
	}

	dir := t.TempDir()

	db, err := database.Open(t.Context(), "turso://"+filepath.ToSlash(filepath.Join(dir, "app.db")),
		database.WithDriver(turso.New(turso.Options{}).Driver()))
	if err != nil {
		t.Fatal(err)
	}

	// Windows cannot remove an open file: close before the directory goes.
	t.Cleanup(func() { _ = db.Close() })

	dbtest.Run(t, db)
}
