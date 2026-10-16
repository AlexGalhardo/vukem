// Package migrations holds the schema migrations.
package migrations

import "github.com/AlexGalhardo/vukem/schema"

// All lists the migrations in order; vukem make:migration appends here.
func All() []schema.Migration {
	return []schema.Migration{
		m20261016184613CreateUsersTable(),
		m20261016184614CreateAuthTables(),
		m20261016184615CreatePostsTable(),
		m20261016184616CreateCommentsTable(),
	}
}
