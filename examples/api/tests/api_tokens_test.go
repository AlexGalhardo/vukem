package tests

import (
	"testing"

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
	if err := orm.Create(t.Context(), acc.DB, &u); err != nil {
		t.Fatal(err)
	}

	r := app.Client(t).PostJSON("/api/v1/tokens", map[string]any{
		"email": email, "password": "correct horse battery", "device_name": "cli",
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
