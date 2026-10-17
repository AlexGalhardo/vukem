package migrations

import "github.com/AlexGalhardo/vukem/schema"

func m20261017091250CreateProjectsTable() schema.Migration {
	return schema.Migration{
		ID: "20261017091250_create_projects_table",
		Up: func(s *schema.Schema) error {
			return s.Create("projects", func(t *schema.Table) {
				t.ID()
				t.ForeignID("user_id").Constrained("users").CascadeOnDelete()
				t.String("name")
				t.Text("description")
				t.Timestamps()
				t.Index("user_id")
			})
		},
		Down: func(s *schema.Schema) error { return s.DropIfExists("projects") },
	}
}
