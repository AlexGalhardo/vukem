package session_test

import (
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite" // SQLite driver for SQLStore

	"github.com/AlexGalhardo/vukem/internal/testutil"
	"github.com/AlexGalhardo/vukem/session"
	"github.com/AlexGalhardo/vukem/session/sessiontest"
)

func TestStoreConformance(t *testing.T) {
	t.Parallel()

	db, err := sql.Open("sqlite", "file:"+filepath.Join(testutil.TempDir(t), "s.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	sq, err := session.NewSQLStore(db)
	if err != nil {
		t.Fatal(err)
	}
	if err := sq.CreateTable(t.Context()); err != nil {
		t.Fatal(err)
	}

	for name, store := range map[string]session.ListStore{"memory": session.NewMemoryStore(), "sql": sq} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			sessiontest.Run(t, store)
		})
	}
}
