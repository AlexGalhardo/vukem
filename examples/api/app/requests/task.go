package requests

// ListTasks is the query string of GET /api/v1/tasks.
type ListTasks struct {
	Status  string `json:"status"   validate:"oneof=todo doing done"                         doc:"Only tasks with this status"`
	Q       string `json:"q"        validate:"max=100"                                       doc:"Search in titles"`
	Sort    string `json:"sort"     validate:"oneof=title -title created_at -created_at -id" doc:"Sort column; - for descending"`
	Page    int    `json:"page"     validate:"min=1"`
	PerPage int    `json:"per_page" validate:"min=1,max=100"`
}

// StoreTask is the body of POST /api/v1/tasks.
type StoreTask struct {
	Title  string `json:"title"  validate:"required,min=3,max=120"`
	Status string `json:"status" validate:"oneof=todo doing done"  doc:"Defaults to todo"`
}

// UpdateTask is the body of PUT /api/v1/tasks/{task}.
type UpdateTask struct {
	Task   int64  `json:"task"   validate:"required,positive"`
	Title  string `json:"title"  validate:"required,min=3,max=120"`
	Status string `json:"status" validate:"required,oneof=todo doing done"`
}

// TaskID names one task in the path.
type TaskID struct {
	Task int64 `json:"task" validate:"required,positive"`
}
