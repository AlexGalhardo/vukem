// Package migrations holds the schema migrations.
package migrations

import "github.com/AlexGalhardo/vukem/schema"

// All lists the migrations in order; vukem make:migration appends here.
func All() []schema.Migration {
	return []schema.Migration{
		m20261017090939CreateUsersTable(),
		m20261017090940CreateAuthTables(),
		m20261017090942AddTwoFactorColumns(),
		m20261017090943AddBillingTables(),
		m20261017091250CreateProjectsTable(),
		m20261017200000AccessTokensRevokedAt(),
	}
}
