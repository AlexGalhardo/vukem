package resources

import (
	"time"

	"github.com/AlexGalhardo/vukem/api"
	"github.com/AlexGalhardo/vukem/examples/api/app/models"
	"github.com/AlexGalhardo/vukem/orm"
)

// TaskJSON is the wire format of models.Task.
type TaskJSON struct {
	ID        int64     `json:"id"         validate:"required"`
	Title     string    `json:"title"      validate:"required"`
	Status    string    `json:"status"     validate:"required,oneof=todo doing done"`
	CreatedAt time.Time `json:"created_at" validate:"required"`
	UpdatedAt time.Time `json:"updated_at" validate:"required"`
}

// TaskPageJSON is one page of tasks: data plus pagination meta.
type TaskPageJSON struct {
	Data []TaskJSON `json:"data" validate:"required"`
	Meta api.Meta   `json:"meta" validate:"required"`
}

// Task transforms one model.
func Task(m models.Task) TaskJSON {
	return TaskJSON{ID: m.ID, Title: m.Title, Status: m.Status, CreatedAt: m.CreatedAt, UpdatedAt: m.UpdatedAt}
}

// TaskPage transforms an offset page of models.
func TaskPage(p orm.Page[models.Task]) TaskPageJSON {
	total := p.Total

	return TaskPageJSON{
		Data: api.Map(p.Items, Task),
		Meta: api.Meta{Page: p.Page, PerPage: p.PerPage, Total: &total, LastPage: p.LastPage},
	}
}
