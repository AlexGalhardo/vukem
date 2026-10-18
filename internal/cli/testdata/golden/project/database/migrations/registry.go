// Package migrations holds the schema migrations.
package migrations

import "github.com/AlexGalhardo/vukem/schema"

// All lists the migrations in order; vukem make:migration appends here.
func All() []schema.Migration {
	return []schema.Migration{
		m20261016120000CreatePostsTable(),
		m20261016120000AddSlugToPostsTable(),
		m20261016120000BackfillData(),
	}
}
