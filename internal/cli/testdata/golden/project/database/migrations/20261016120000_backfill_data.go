package migrations

import "github.com/AlexGalhardo/golang-framework/schema"

func m20261016120000BackfillData() schema.Migration {
	return schema.Migration{
		ID:   "20261016120000_backfill_data",
		Up:   func(s *schema.Schema) error { return nil },
		Down: func(s *schema.Schema) error { return nil },
	}
}
