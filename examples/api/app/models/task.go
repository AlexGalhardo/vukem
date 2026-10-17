package models

import "github.com/AlexGalhardo/vukem/orm"

// Task statuses.
const (
	TaskTodo  = "todo"
	TaskDoing = "doing"
	TaskDone  = "done"
)

// Task is a row of the tasks table, owned by the user who created it.
type Task struct {
	orm.Model

	UserID int64  `json:"user_id"`
	Title  string `json:"title"`
	Status string `json:"status"`
}
