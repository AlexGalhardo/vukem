package migrations

import "github.com/AlexGalhardo/vukem/schema"

// Tables used by vukem/auth: remember-me tokens, API access tokens and
// password reset tokens. Only SHA-256 hashes of secrets are stored.
func m20261016184614CreateAuthTables() schema.Migration {
	return schema.Migration{
		ID: "20261016184614_create_auth_tables",
		Up: func(s *schema.Schema) error {
			if err := s.Create("remember_tokens", func(t *schema.Table) {
				t.String("id").Unique()
				t.String("user_id").Index()
				t.String("hash")
				t.Timestamp("expires_at")
			}); err != nil {
				return err
			}

			if err := s.Create("access_tokens", func(t *schema.Table) {
				t.String("id").Unique()
				t.String("user_id").Index()
				t.String("name")
				t.String("hash")
				t.Text("abilities")
				t.Timestamp("last_used_at").Nullable()
				t.Timestamp("expires_at").Nullable()
				t.Timestamp("created_at")
			}); err != nil {
				return err
			}

			return s.Create("password_resets", func(t *schema.Table) {
				t.String("email").Unique()
				t.String("hash")
				t.Timestamp("created_at")
			})
		},
		Down: func(s *schema.Schema) error {
			for _, table := range []string{"password_resets", "access_tokens", "remember_tokens"} {
				if err := s.DropIfExists(table); err != nil {
					return err
				}
			}

			return nil
		},
	}
}
