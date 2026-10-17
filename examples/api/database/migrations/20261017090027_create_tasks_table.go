package migrations

import "github.com/AlexGalhardo/vukem/schema"

func m20261017090027CreateTasksTable() schema.Migration {
	return schema.Migration{
		ID: "20261017090027_create_tasks_table",
		Up: func(s *schema.Schema) error {
			return s.Create("tasks", func(t *schema.Table) {
				t.ID()
				t.ForeignID("user_id").Constrained("users").CascadeOnDelete()
				t.String("title")
				t.Enum("status", "todo", "doing", "done").Default("todo")
				t.Timestamps()
				t.Index("user_id", "status")
			})
		},
		Down: func(s *schema.Schema) error { return s.DropIfExists("tasks") },
	}
}
