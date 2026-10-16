package tests

import (
	"os"
	"testing"

	"github.com/AlexGalhardo/vukem/examples/blog/app/accounts"
	"github.com/AlexGalhardo/vukem/examples/blog/app/models"
	"github.com/AlexGalhardo/vukem/examples/blog/bootstrap"
	"github.com/AlexGalhardo/vukem/orm"
	"github.com/AlexGalhardo/vukem/vukemtest"
)

// newApp boots the blog on in-memory SQLite (tests run in parallel), or on
// TEST_DATABASE_URL (e.g. Postgres in CI), where tests run one at a time
// on a freshly migrated database.
func newApp(t *testing.T) *vukemtest.App {
	t.Helper()

	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Parallel()
		return vukemtest.New(t, bootstrap.New)
	}

	app := vukemtest.New(t, bootstrap.New, vukemtest.Options{Env: map[string]string{"DATABASE_URL": url}})
	app.RefreshDatabase(t)

	return app
}

// user creates a verified user with a known password.
func user(t *testing.T, app *vukemtest.App, email string) models.User {
	t.Helper()

	acc, err := accounts.New(app.App)
	if err != nil {
		t.Fatal(err)
	}

	h, err := acc.Hasher.Make("correct horse battery")
	if err != nil {
		t.Fatal(err)
	}

	u := models.User{Name: email, Email: email, Password: h}
	u.EmailVerifiedAt.Valid = true

	if err := orm.Create(t.Context(), acc.DB, &u); err != nil {
		t.Fatal(err)
	}

	return u
}
