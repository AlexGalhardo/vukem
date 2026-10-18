package migrations

import "github.com/AlexGalhardo/vukem/schema"

func m20261016120000BackfillData() schema.Migration {
	return schema.Migration{
		ID: "20261016120000_backfill_data",
		// Name the parameter s with the first statement you write.
		Up:   func(_ *schema.Schema) error { return nil },
		Down: func(_ *schema.Schema) error { return nil },
	}
}
