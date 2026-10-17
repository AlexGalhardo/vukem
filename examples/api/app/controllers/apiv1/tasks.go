package apiv1

import (
	"github.com/AlexGalhardo/vukem/api"
	"github.com/AlexGalhardo/vukem/auth"
	"github.com/AlexGalhardo/vukem/database"
	"github.com/AlexGalhardo/vukem/examples/api/app/models"
	"github.com/AlexGalhardo/vukem/examples/api/app/requests"
	"github.com/AlexGalhardo/vukem/examples/api/app/resources"
	"github.com/AlexGalhardo/vukem/orm"
	"github.com/AlexGalhardo/vukem/web"
)

// Tasks is a typed JSON resource: every task belongs to the token's user.
type Tasks struct {
	db *database.DB
}

// NewTasks returns the tasks controller.
func NewTasks(db *database.DB) *Tasks { return &Tasks{db: db} }

// Index lists the user's tasks: ?status=, ?q=, ?sort=, ?page= and ?per_page=.
func (t *Tasks) Index(c *web.Ctx, in requests.ListTasks) (resources.TaskPageJSON, error) {
	q, err := t.owned(c)
	if err != nil {
		return resources.TaskPageJSON{}, err
	}

	if in.Status != "" {
		q = q.Where("status", in.Status)
	}

	if in.Q != "" {
		q = q.Where("title", "LIKE", "%"+in.Q+"%")
	}

	// api.Query allowlists the sort columns and caps per_page.
	req, err := api.Query(c, q, api.Sorts("title", "created_at", "-id"), api.PerPage(20, 100))
	if err != nil {
		return resources.TaskPageJSON{}, err
	}

	page, err := req.Paginate(c.Context())
	if err != nil {
		return resources.TaskPageJSON{}, err
	}

	return resources.TaskPage(page), nil
}

// Show returns one task.
func (t *Tasks) Show(c *web.Ctx, in requests.TaskID) (resources.TaskJSON, error) {
	task, err := t.find(c, in.Task)
	if err != nil {
		return resources.TaskJSON{}, err
	}

	return resources.Task(task), nil
}

// Store creates a task for the user.
func (t *Tasks) Store(c *web.Ctx, in requests.StoreTask) (resources.TaskJSON, error) {
	u, err := auth.User[models.User](c, "api")
	if err != nil {
		return resources.TaskJSON{}, err
	}

	task := models.Task{UserID: u.ID, Title: in.Title, Status: in.Status}
	if task.Status == "" {
		task.Status = models.TaskTodo
	}

	if err := orm.Create(c.Context(), t.db, &task); err != nil {
		return resources.TaskJSON{}, err
	}

	return resources.Task(task), nil
}

// Update replaces a task's title and status.
func (t *Tasks) Update(c *web.Ctx, in requests.UpdateTask) (resources.TaskJSON, error) {
	task, err := t.find(c, in.Task)
	if err != nil {
		return resources.TaskJSON{}, err
	}

	task.Title, task.Status = in.Title, in.Status
	if err := orm.Update(c.Context(), t.db, &task); err != nil {
		return resources.TaskJSON{}, err
	}

	return resources.Task(task), nil
}

// Destroy deletes a task.
func (t *Tasks) Destroy(c *web.Ctx, in requests.TaskID) (struct{}, error) {
	task, err := t.find(c, in.Task)
	if err != nil {
		return struct{}{}, err
	}

	return struct{}{}, orm.Delete(c.Context(), t.db, &task)
}

// owned scopes the query to the authenticated user, so another user's task
// is a 404 rather than a 403 that would confirm it exists.
func (t *Tasks) owned(c *web.Ctx) (orm.Query[models.Task], error) {
	u, err := auth.User[models.User](c, "api")
	if err != nil {
		return orm.Query[models.Task]{}, err
	}

	return orm.QueryOf[models.Task](t.db).Where("user_id", u.ID), nil
}

func (t *Tasks) find(c *web.Ctx, id int64) (models.Task, error) {
	q, err := t.owned(c)
	if err != nil {
		return models.Task{}, err
	}

	return q.Find(c.Context(), id)
}
