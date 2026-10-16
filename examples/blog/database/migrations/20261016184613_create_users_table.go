package migrations

import "github.com/AlexGalhardo/vukem/schema"

func m20261016184613CreateUsersTable() schema.Migration {
	return schema.Migration{
		ID: "20261016184613_create_users_table",
		Up: func(s *schema.Schema) error {
			return s.Create("users", func(t *schema.Table) {
				t.ID()
				t.String("name")
				t.String("email").Unique()
				t.String("password")
				t.Timestamp("email_verified_at").Nullable()
				t.Timestamps()
			})
		},
		Down: func(s *schema.Schema) error { return s.DropIfExists("users") },
	}
}
