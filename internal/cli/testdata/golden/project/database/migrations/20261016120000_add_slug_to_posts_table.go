package migrations

import "github.com/AlexGalhardo/vukem/schema"

func m20261016120000AddSlugToPostsTable() schema.Migration {
	return schema.Migration{
		ID: "20261016120000_add_slug_to_posts_table",
		Up: func(s *schema.Schema) error {
			// Name it t and add the change: t.String("slug").Nullable().
			return s.Table("posts", func(_ *schema.Table) {
			})
		},
		Down: func(s *schema.Schema) error {
			// Name it t and undo the change: t.DropColumn("slug").
			return s.Table("posts", func(_ *schema.Table) {
			})
		},
	}
}
