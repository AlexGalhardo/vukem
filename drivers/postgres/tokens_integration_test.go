//go:build integration

package postgres

import (
	"strings"
	"testing"
	"time"

	"github.com/AlexGalhardo/vukem/auth"
	"github.com/AlexGalhardo/vukem/database"
	"github.com/AlexGalhardo/vukem/schema"
)

// TestTokensSoftRevoke runs auth.Tokens soft revoke and its upgrade
// migration on PostgreSQL.
func TestTokensSoftRevoke(t *testing.T) {
	db := open(t)
	ctx := t.Context()

	drop := func() { _, _ = db.ExecContext(ctx, "DROP TABLE IF EXISTS access_tokens") }
	drop()
	t.Cleanup(func() { _, _ = db.ExecContext(t.Context(), "DROP TABLE IF EXISTS access_tokens") })

	s := schema.New(ctx, db)

	// The layout created by install:auth before revoked_at.
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

	now := time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)
	tokens := auth.NewTokens(db)
	tokens.Now = func() time.Time { return now }

	plain, tok, err := tokens.Create(ctx, "1", "cli", []string{"*"}, 0)
	if err != nil {
		t.Fatal(err)
	}

	// SQLSTATE 42703 is recognised as the missing column (the guard then
	// logs the migration to add).
	if _, err := tokens.Find(ctx, plain); err == nil ||
		!strings.Contains(err.Error(), "access_tokens has no revoked_at column") {
		t.Fatalf("Find() without revoked_at = %v, want the missing column recognised", err)
	}

	// Revoking still works before the upgrade, also inside a transaction
	// (a failed UPDATE would abort it): the rows are deleted instead.
	_, legacy, _ := tokens.Create(ctx, "3", "legacy", nil, 0)
	if err := db.Tx(ctx, func(tx *database.Tx) error {
		return auth.NewTokens(tx).RevokeAll(ctx, "3")
	}); err != nil {
		t.Fatalf("RevokeAll() in a transaction on a legacy table = %v", err)
	}

	var n int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM access_tokens WHERE id = $1", legacy.ID).
		Scan(&n); err != nil ||
		n != 0 {
		t.Fatalf("legacy token rows after RevokeAll = %d, %v, want 0", n, err)
	}

	upgrade := auth.TokensRevokedAtMigration("2_access_tokens_revoked_at")
	for range 2 { // the second run finds the column and skips
		if err := upgrade.Up(s); err != nil {
			t.Fatalf("upgrade = %v", err)
		}
	}

	if has, err := s.HasColumn("access_tokens", "revoked_at"); err != nil || !has {
		t.Fatalf("HasColumn(access_tokens, revoked_at) = %v, %v, want true", has, err)
	}

	if _, err := tokens.Find(ctx, plain); err != nil {
		t.Fatalf("Find() after the upgrade = %v", err)
	}

	if ok, err := tokens.RevokeFor(ctx, "2", tok.ID); err != nil || ok {
		t.Fatalf("RevokeFor(other user) = %v, %v, want false, nil", ok, err)
	}

	now = now.Add(time.Hour)
	if ok, err := tokens.RevokeFor(ctx, "1", tok.ID); err != nil || !ok {
		t.Fatalf("RevokeFor(owner) = %v, %v, want true, nil", ok, err)
	}

	if _, err := tokens.Find(ctx, plain); err == nil {
		t.Fatal("Find(revoked) = nil error, want rejected")
	}

	list, err := tokens.List(ctx, "1")
	if err != nil || len(list) != 1 || list[0].RevokedAt == nil || !list[0].RevokedAt.Equal(now) {
		t.Fatalf("List() = %+v, %v, want the token revoked at %v", list, err, now)
	}

	// A cutoff past now still keeps tokens that have not expired.
	_, live, _ := tokens.Create(ctx, "1", "live", nil, time.Hour)

	if n, err := tokens.Prune(ctx, now); err != nil || n != 0 {
		t.Fatalf("Prune(at revocation) = %d, %v, want 0", n, err)
	}

	if n, err := tokens.Prune(ctx, now.AddDate(1, 0, 0)); err != nil || n != 1 {
		t.Fatalf("Prune(next year) = %d, %v, want 1 (the revoked token)", n, err)
	}

	if list, _ := tokens.List(ctx, "1"); len(list) != 1 || list[0].ID != live.ID {
		t.Fatalf("List() after Prune = %+v, want only the live token", list)
	}
}
