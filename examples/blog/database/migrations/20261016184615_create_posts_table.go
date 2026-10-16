package migrations

import "github.com/AlexGalhardo/vukem/schema"

func m20261016184615CreatePostsTable() schema.Migration {
	return schema.Migration{
		ID: "20261016184615_create_posts_table",
		Up: func(s *schema.Schema) error {
			return s.Create("posts", func(t *schema.Table) {
				t.ID()
				t.ForeignID("user_id").Constrained("users").CascadeOnDelete()
				t.String("title")
				t.Text("body")
				t.Timestamps()
			})
		},
		Down: func(s *schema.Schema) error { return s.DropIfExists("posts") },
	}
}
