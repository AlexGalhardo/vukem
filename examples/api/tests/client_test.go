package tests

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AlexGalhardo/vukem/examples/api/bootstrap"
	"github.com/AlexGalhardo/vukem/examples/api/internal/apiclient"
	"github.com/AlexGalhardo/vukem/openapi"
	"github.com/AlexGalhardo/vukem/vukemtest"
)

// TestGeneratedClientIsCurrent fails when the routes change without
// re-running vukem codegen:client -lang go.
func TestGeneratedClientIsCurrent(t *testing.T) {
	t.Parallel()

	// The document title comes from APP_NAME, as in .env.example.
	app := vukemtest.New(t, bootstrap.New, vukemtest.Options{Env: map[string]string{"APP_NAME": "api"}})

	doc, err := app.OpenAPI()
	if err != nil {
		t.Fatal(err)
	}

	want, err := openapi.GoClient(doc, "apiclient")
	if err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(filepath.Join("..", "internal", "apiclient", "client.go"))
	if err != nil {
		t.Fatal(err)
	}

	if strings.ReplaceAll(string(got), "\r\n", "\n") != string(want) {
		t.Fatal("internal/apiclient/client.go is stale: run vukem codegen:client -lang go")
	}
}

func TestGeneratedClientRoundTrip(t *testing.T) {
	t.Parallel()

	app := vukemtest.New(t, bootstrap.New)
	token := apiUser(t, app, "client@example.com")

	srv := httptest.NewServer(app.Handler())
	t.Cleanup(srv.Close)

	c := apiclient.New(srv.URL)
	c.Header = func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+token) }

	created, err := c.TasksStore(t.Context(), apiclient.TasksStoreBody{Title: "From the Go client"})
	if err != nil {
		t.Fatal(err)
	}

	if created.Status != apiclient.TaskJSONStatusTodo {
		t.Errorf("TasksStore().Status = %q, want todo", created.Status)
	}

	page, err := c.TasksIndex(t.Context(), apiclient.TasksIndexQuery{})
	if err != nil {
		t.Fatal(err)
	}

	if len(page.Data) != 1 || page.Data[0].ID != created.ID {
		t.Errorf("TasksIndex().Data = %+v, want the created task", page.Data)
	}

	_, err = c.TasksStore(t.Context(), apiclient.TasksStoreBody{Title: "x"})
	if apiErr, ok := errors.AsType[*apiclient.Error](err); !ok || apiErr.Status != http.StatusUnprocessableEntity {
		t.Errorf("TasksStore(short title) error = %v, want a 422 *apiclient.Error", err)
	}
}
