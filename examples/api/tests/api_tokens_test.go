package tests

import (
	"testing"
	"time"

	"github.com/AlexGalhardo/vukem/examples/api/app/accounts"
	"github.com/AlexGalhardo/vukem/examples/api/app/models"
	"github.com/AlexGalhardo/vukem/examples/api/bootstrap"
	"github.com/AlexGalhardo/vukem/orm"
	"github.com/AlexGalhardo/vukem/vukemtest"
)

// apiUser creates a user directly and returns a token for it.
func apiUser(t *testing.T, app *vukemtest.App, email string) string {
	t.Helper()

	acc, err := accounts.New(app.App)
	if err != nil {
		t.Fatal(err)
	}

	h, err := acc.Hasher.Make("correct horse battery")
	if err != nil {
		t.Fatal(err)
	}

	u := models.User{Name: "API User", Email: email, Password: h}
	u.EmailVerifiedAt.V, u.EmailVerifiedAt.Valid = time.Now().UTC(), true
	if err := orm.Create(t.Context(), acc.DB, &u); err != nil {
		t.Fatal(err)
	}

	r := app.Client(t).PostJSON("/api/v1/tokens", map[string]any{
		"email": email, "password": "correct horse battery", "device_name": "cli",
		"abilities": []string{"read", "write"},
	}).AssertCreated().AssertJSONPath("type", "Bearer")

	tok, _ := r.JSONPath("token")
	s, _ := tok.(string)

	return s
}

func TestTokenLifecycle(t *testing.T) {
	t.Parallel()

	app := vukemtest.New(t, bootstrap.New)
	token := apiUser(t, app, "dev@example.com")

	api := app.Client(t).WithToken(token)
	api.GetJSON("/api/v1/me").AssertOK().AssertJSONPath("email", "dev@example.com")

	app.Client(t).GetJSON("/api/v1/me").AssertUnauthorized()
	app.Client(t).PostJSON("/api/v1/tokens", map[string]any{
		"email": "dev@example.com", "password": "wrong", "device_name": "cli",
	}).AssertJSONValidationErrors("email")

	api.Delete("/api/v1/tokens/current").AssertNoContent()
	app.Client(t).WithToken(token).GetJSON("/api/v1/me").AssertUnauthorized()
}

func TestTokenRefusedForUnverifiedEmail(t *testing.T) {
	t.Parallel()

	app := vukemtest.New(t, bootstrap.New)

	acc, err := accounts.New(app.App)
	if err != nil {
		t.Fatal(err)
	}

	h, err := acc.Hasher.Make("correct horse battery")
	if err != nil {
		t.Fatal(err)
	}

	u := models.User{Name: "New User", Email: "new@example.com", Password: h}
	if err := orm.Create(t.Context(), acc.DB, &u); err != nil {
		t.Fatal(err)
	}

	app.Client(t).PostJSON("/api/v1/tokens", map[string]any{
		"email": "new@example.com", "password": "correct horse battery", "device_name": "cli",
	}).AssertStatus(403)
}

func TestTokenWithoutReadAbilityCannotReadMe(t *testing.T) {
	t.Parallel()

	app := vukemtest.New(t, bootstrap.New)
	apiUser(t, app, "dev@example.com")

	r := app.Client(t).PostJSON("/api/v1/tokens", map[string]any{
		"email": "dev@example.com", "password": "correct horse battery", "device_name": "write-only",
		"abilities": []string{"write"},
	}).AssertCreated()

	tok, _ := r.JSONPath("token")
	s, _ := tok.(string)

	app.Client(t).WithToken(s).GetJSON("/api/v1/me").AssertStatus(403)
}
