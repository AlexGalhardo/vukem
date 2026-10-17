package migrations

import (
	"github.com/AlexGalhardo/vukem/auth"
	"github.com/AlexGalhardo/vukem/schema"
)

// Adds access_tokens.revoked_at (soft revoke, vukem v0.26.0) to the table
// created by the earlier auth migration.
func m20261017200000AccessTokensRevokedAt() schema.Migration {
	return auth.TokensRevokedAtMigration("20261017200000_access_tokens_revoked_at")
}
