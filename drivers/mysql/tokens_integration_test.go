//go:build integration

package mysql

import (
	"strings"
	"testing"

	"github.com/AlexGalhardo/vukem/auth"
	"github.com/AlexGalhardo/vukem/database"
	"github.com/AlexGalhardo/vukem/schema"
)

// TestTokensLegacyTable checks that error 1054 is recognised as the
// missing revoked_at column, that revoking deletes until the upgrade, and
// that the upgrade migration runs on MySQL and MariaDB.
func TestTokensLegacyTable(t *testing.T) {
	each(t, func(t *testing.T, db *database.DB) {
		t.Helper()

		ctx := t.Context()
		_, _ = db.ExecContext(ctx, "DROP TABLE IF EXISTS access_tokens")
		t.Cleanup(func() { _, _ = db.ExecContext(t.Context(), "DROP TABLE IF EXISTS access_tokens") })

		s := schema.New(ctx, db)
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
			t.Fatal(err)
		}

		tokens := auth.NewTokens(db)

		plain, tok, err := tokens.Create(ctx, "1", "cli", nil, 0)
		if err != nil {
			t.Fatal(err)
		}

		if _, err := tokens.Find(ctx, plain); err == nil ||
			!strings.Contains(err.Error(), "access_tokens has no revoked_at column") {
			t.Fatalf("Find() without revoked_at = %v, want the missing column recognised", err)
		}

		if err := db.Tx(ctx, func(tx *database.Tx) error {
			return auth.NewTokens(tx).RevokeAll(ctx, "2")
		}); err != nil {
			t.Fatalf("RevokeAll() in a transaction on a legacy table = %v", err)
		}

		if err := auth.TokensRevokedAtMigration("2_access_tokens_revoked_at").Up(s); err != nil {
			t.Fatalf("upgrade = %v", err)
		}

		if _, err := tokens.Find(ctx, plain); err != nil {
			t.Fatalf("Find() after the upgrade = %v", err)
		}

		if ok, err := tokens.RevokeFor(ctx, "1", tok.ID); err != nil || !ok {
			t.Fatalf("RevokeFor(owner) = %v, %v, want true, nil", ok, err)
		}

		if list, err := tokens.List(ctx, "1"); err != nil || len(list) != 1 || list[0].RevokedAt == nil {
			t.Fatalf("List() = %+v, %v, want the token marked revoked", list, err)
		}
	})
}
