// Package migrations holds the schema migrations.
package migrations

import "github.com/AlexGalhardo/vukem/schema"

// All lists the migrations in order; vukem make:migration appends here.
func All() []schema.Migration {
	return []schema.Migration{
		m20260101000000CreatePostsTable(),
		m20261017085831CreateUsersTable(),
		m20261017085832CreateAuthTables(),
		m20261017090027CreateTasksTable(),
	}
}
