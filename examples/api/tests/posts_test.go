package tests

import (
	"testing"

	"github.com/AlexGalhardo/vukem/examples/api/bootstrap"
	"github.com/AlexGalhardo/vukem/vukemtest"
)

func TestPostsAPI(t *testing.T) {
	t.Parallel()

	app := vukemtest.New(t, bootstrap.New)
	token := apiUser(t, app, "writer@example.com")
	writer := app.Client(t).WithToken(token)

	writer.PostJSON("/api/v1/posts", map[string]any{"title": "Hello API", "body": "First."}).
		AssertCreated().
		AssertHeader("Location", "/api/v1/posts/1").
		AssertJSONPath("title", "Hello API")

	writer.PostJSON("/api/v1/posts", map[string]any{"title": "Second post"}).AssertCreated()
	writer.PostJSON("/api/v1/posts", map[string]any{"title": "x"}).AssertJSONValidationErrors("title")

	// Anyone can read; pages come in an envelope.
	reader := app.Client(t)
	reader.GetJSON("/api/v1/posts?sort=title&per_page=1").AssertOK().
		AssertJSONCount("data", 1).
		AssertJSONPath("data.0.title", "Hello API").
		AssertJSONPath("meta.total", 2).
		AssertJSONPath("links.next", "/api/v1/posts?page=2&per_page=1&sort=title")
	reader.GetJSON("/api/v1/posts?sort=password").AssertStatus(400)
	reader.GetJSON("/api/v1/posts/99").AssertProblem(404)

	// Writes need a token.
	reader.PostJSON("/api/v1/posts", map[string]any{"title": "Nope"}).AssertUnauthorized()

	writer.PatchJSON("/api/v1/posts/1", map[string]any{"title": "Edited"}).AssertOK().AssertJSONPath("title", "Edited")
	writer.Delete("/api/v1/posts/1").AssertNoContent()
	vukemtest.AssertDatabaseCount(t, app, "posts", 1)
}

// A token carries only the abilities it was issued with: write routes
// refuse one that was not granted "write".
func TestWriteRoutesNeedTheWriteAbility(t *testing.T) {
	t.Parallel()

	app := vukemtest.New(t, bootstrap.New)
	full := app.Client(t).WithToken(apiUser(t, app, "dev@example.com"))
	full.PostJSON("/api/v1/posts", map[string]any{"title": "Mine post"}).AssertCreated()
	full.PostJSON("/api/v1/tasks", map[string]any{"title": "Mine task"}).AssertCreated()

	for _, abilities := range [][]string{{"read"}, {}} {
		r := app.Client(t).PostJSON("/api/v1/tokens", map[string]any{
			"email": "dev@example.com", "password": "correct horse battery", "device_name": "limited",
			"abilities": abilities,
		}).AssertCreated()

		tok, _ := r.JSONPath("token")
		s, _ := tok.(string)
		c := app.Client(t).WithToken(s)

		c.PostJSON("/api/v1/posts", map[string]any{"title": "Denied post"}).AssertStatus(403)
		c.PatchJSON("/api/v1/posts/1", map[string]any{"title": "Denied edit"}).AssertStatus(403)
		c.Delete("/api/v1/posts/1").AssertStatus(403)
		c.PostJSON("/api/v1/tasks", map[string]any{"title": "Denied task"}).AssertStatus(403)
		c.PutJSON("/api/v1/tasks/1", map[string]any{"title": "Denied edit"}).AssertStatus(403)
		c.Delete("/api/v1/tasks/1").AssertStatus(403)
	}

	vukemtest.AssertDatabaseCount(t, app, "posts", 1)
	vukemtest.AssertDatabaseHas(t, app, "posts", map[string]any{"title": "Mine post"})
	vukemtest.AssertDatabaseHas(t, app, "tasks", map[string]any{"title": "Mine task"})
}
