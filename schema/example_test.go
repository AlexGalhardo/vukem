package schema_test

import (
	"context"
	"fmt"

	"github.com/AlexGalhardo/vukem/database"
	"github.com/AlexGalhardo/vukem/schema"
)

func ExampleMigrator() {
	ctx := context.Background()

	db, _ := database.Open(ctx, ":memory:")
	defer db.Close()

	migrations := []schema.Migration{{
		ID: "20261016120000_create_users_table",
		Up: func(s *schema.Schema) error {
			return s.Create("users", func(t *schema.Table) {
				t.ID()
				t.String("email").Unique()
				t.Timestamps()
			})
		},
		Down: func(s *schema.Schema) error { return s.Drop("users") },
	}}

	m, err := schema.NewMigrator(db, migrations)
	if err != nil {
		fmt.Println(err)
		return
	}

	ran, _ := m.Up(ctx)
	fmt.Println("ran:", ran)

	has, _ := schema.New(ctx, db).HasTable("users")
	fmt.Println("users table:", has)

	rolled, _ := m.Rollback(ctx, 1)
	fmt.Println("rolled back:", rolled)
	// Output:
	// ran: [20261016120000_create_users_table]
	// users table: true
	// rolled back: [20261016120000_create_users_table]
}
