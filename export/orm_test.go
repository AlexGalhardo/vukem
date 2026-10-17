package export_test

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/AlexGalhardo/vukem/database"
	"github.com/AlexGalhardo/vukem/export"
	"github.com/AlexGalhardo/vukem/orm"
)

type Contact struct {
	ID   int64
	Name string
}

func TestCSVFromORMEach(t *testing.T) {
	t.Parallel()

	ctx := t.Context()

	db, err := database.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if _, err := db.ExecContext(ctx, "CREATE TABLE contacts (id INTEGER PRIMARY KEY, name TEXT NOT NULL)"); err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{"Ana", "=cmd|'/C calc'!A0", "João"} {
		if _, err := db.ExecContext(ctx, "INSERT INTO contacts (name) VALUES (?)", name); err != nil {
			t.Fatal(err)
		}
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/contacts.csv", nil)

	err = export.CSV(rec, req, "contacts.csv", []string{"id", "name"},
		orm.QueryOf[Contact](db).OrderBy("id", orm.Asc).Each(ctx),
		func(c Contact) []string { return []string{strconv.FormatInt(c.ID, 10), c.Name} })
	if err != nil {
		t.Fatalf("CSV() = %v", err)
	}

	if got, want := rec.Body.String(), "id,name\n1,Ana\n2,'=cmd|'/C calc'!A0\n3,João\n"; got != want {
		t.Errorf("body = %q, want %q", got, want)
	}

	// The rows were closed: the connection is free for the next query.
	if n, err := orm.QueryOf[Contact](db).Count(ctx); err != nil || n != 3 {
		t.Errorf("Count() = %d, %v; want 3, nil", n, err)
	}
}
