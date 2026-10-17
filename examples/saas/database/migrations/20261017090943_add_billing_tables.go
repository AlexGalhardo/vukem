package migrations

import "github.com/AlexGalhardo/vukem/schema"

func m20261017090943AddBillingTables() schema.Migration {
	return schema.Migration{
		ID: "20261017090943_add_billing_tables",
		Up: func(s *schema.Schema) error {
			err := s.Table("users", func(t *schema.Table) {
				t.String("stripe_customer_id").Nullable()
				t.UniqueIndex("stripe_customer_id")
			})
			if err != nil {
				return err
			}

			return s.Create("subscriptions", func(t *schema.Table) {
				t.ID()
				t.ForeignID("user_id").Constrained("users").CascadeOnDelete()
				t.String("stripe_subscription_id").Unique()
				t.String("status")
				t.String("price_id")
				t.Timestamp("current_period_end").Nullable()
				t.Boolean("cancel_at_period_end").Default(false)
				t.Timestamps()
				t.Index("user_id")
			})
		},
		Down: func(s *schema.Schema) error {
			if err := s.DropIfExists("subscriptions"); err != nil {
				return err
			}

			return s.Table("users", func(t *schema.Table) {
				t.DropIndex("users_stripe_customer_id_unique")
				t.DropColumn("stripe_customer_id")
			})
		},
	}
}
