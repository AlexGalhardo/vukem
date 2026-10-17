package tests

import (
	"strings"
	"testing"

	"github.com/AlexGalhardo/vukem/examples/api/bootstrap"
	"github.com/AlexGalhardo/vukem/vukemtest"
)

func TestTasksAPI(t *testing.T) {
	t.Parallel()

	app := vukemtest.New(t, bootstrap.New)
	ada := app.Client(t).WithToken(apiUser(t, app, "ada@example.com"))

	ada.PostJSON("/api/v1/tasks", map[string]any{"title": "Write the tutorial"}).
		AssertCreated().
		AssertMatchesOpenAPI().
		AssertJSONPath("status", "todo")
	ada.PostJSON("/api/v1/tasks", map[string]any{"title": "Review the API", "status": "doing"}).AssertCreated()
	ada.PostJSON("/api/v1/tasks", map[string]any{"title": "Ship it", "status": "done"}).AssertCreated()
	ada.PostJSON("/api/v1/tasks", map[string]any{"title": "x", "status": "later"}).
		AssertJSONValidationErrors("title", "status")

	ada.GetJSON("/api/v1/tasks?sort=title&per_page=2").AssertOK().
		AssertMatchesOpenAPI().
		AssertJSONCount("data", 2).
		AssertJSONPath("data.0.title", "Review the API").
		AssertJSONPath("meta.total", 3).
		AssertJSONPath("meta.last_page", 2)
	ada.GetJSON("/api/v1/tasks?status=done").AssertOK().
		AssertJSONCount("data", 1).
		AssertJSONPath("data.0.title", "Ship it")
	ada.GetJSON("/api/v1/tasks?q=tutorial").AssertOK().AssertJSONCount("data", 1)
	ada.GetJSON("/api/v1/tasks?sort=user_id").AssertJSONValidationErrors("sort")

	ada.PutJSON("/api/v1/tasks/1", map[string]any{"title": "Write the tutorials", "status": "done"}).
		AssertOK().
		AssertMatchesOpenAPI().
		AssertJSONPath("status", "done")
	ada.GetJSON("/api/v1/tasks/1").AssertOK().AssertJSONPath("title", "Write the tutorials")

	// Tasks are private: another user sees none and gets 404 on Ada's.
	bob := app.Client(t).WithToken(apiUser(t, app, "bob@example.com"))
	bob.GetJSON("/api/v1/tasks").AssertOK().AssertJSONCount("data", 0)
	bob.GetJSON("/api/v1/tasks/1").AssertProblem(404)
	bob.Delete("/api/v1/tasks/1").AssertProblem(404)

	app.Client(t).GetJSON("/api/v1/tasks").AssertUnauthorized()

	ada.Delete("/api/v1/tasks/1").AssertNoContent()
	vukemtest.AssertDatabaseCount(t, app, "tasks", 2)
}

func TestTasksRateLimit(t *testing.T) {
	t.Parallel()

	app := vukemtest.New(t, bootstrap.New)
	c := app.Client(t).WithToken(apiUser(t, app, "busy@example.com"))

	c.GetJSON("/api/v1/tasks").AssertOK().AssertHeader("RateLimit-Limit", "60")

	for range 59 {
		c.GetJSON("/api/v1/tasks")
	}

	r := c.GetJSON("/api/v1/tasks").AssertStatus(429)
	if r.Header("Retry-After") == "" {
		t.Fatal("429 without Retry-After")
	}
}

func TestOpenAPIDocument(t *testing.T) {
	t.Parallel()

	app := vukemtest.New(t, bootstrap.New)

	body := app.Client(t).GetJSON("/openapi.json").AssertOK().Body()
	for _, want := range []string{`"/api/v1/tasks"`, `"/api/v1/tasks/{task}"`, `"tasks.index"`, `"bearer"`} {
		if !strings.Contains(body, want) {
			t.Errorf("openapi.json lacks %s", want)
		}
	}
}
