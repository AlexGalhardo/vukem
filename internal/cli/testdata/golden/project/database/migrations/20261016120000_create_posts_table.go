package migrations

import "github.com/AlexGalhardo/golang-framework/schema"

func m20261016120000CreatePostsTable() schema.Migration {
	return schema.Migration{
		ID: "20261016120000_create_posts_table",
		Up: func(s *schema.Schema) error {
			return s.Create("posts", func(t *schema.Table) {
				t.ID()
				t.Timestamps()
			})
		},
		Down: func(s *schema.Schema) error { return s.DropIfExists("posts") },
	}
}
