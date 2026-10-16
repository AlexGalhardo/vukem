package migrations

import "github.com/AlexGalhardo/vukem/schema"

func m20261016184616CreateCommentsTable() schema.Migration {
	return schema.Migration{
		ID: "20261016184616_create_comments_table",
		Up: func(s *schema.Schema) error {
			return s.Create("comments", func(t *schema.Table) {
				t.ID()
				t.ForeignID("post_id").Constrained("posts").CascadeOnDelete()
				t.ForeignID("user_id").Constrained("users").CascadeOnDelete()
				t.Text("body")
				t.Timestamps()
			})
		},
		Down: func(s *schema.Schema) error { return s.DropIfExists("comments") },
	}
}
