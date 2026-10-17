package factories

import (
	"github.com/AlexGalhardo/vukem/examples/api/app/models"
	"github.com/AlexGalhardo/vukem/factory"
)

// Task builds models.Task with fake data; give it an owner with State:
// factories.Task.State(func(t *models.Task) { t.UserID = u.ID }).Count(10).Create(ctx, db).
var Task = factory.New(func(f *factory.Faker) models.Task {
	return models.Task{
		Title:  f.Words(3),
		Status: factory.Pick(f, models.TaskTodo, models.TaskDoing, models.TaskDone),
	}
})
