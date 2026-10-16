package migrations

import "github.com/AlexGalhardo/vukem/schema"

func m20261016120000AddSlugToPostsTable() schema.Migration {
	return schema.Migration{
		ID: "20261016120000_add_slug_to_posts_table",
		Up: func(s *schema.Schema) error {
			return s.Table("posts", func(t *schema.Table) {
			})
		},
		Down: func(s *schema.Schema) error {
			return s.Table("posts", func(t *schema.Table) {
			})
		},
	}
}
