//go:build integration

package postgres

import (
	"strings"
	"testing"
	"time"

	"github.com/AlexGalhardo/vukem/auth"
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

	if _, err := tokens.Find(ctx, plain); err == nil || !strings.Contains(err.Error(), "revoked_at") {
		t.Fatalf("Find() without revoked_at = %v, want an error naming the column", err)
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

	if n, err := tokens.Prune(ctx, now); err != nil || n != 0 {
		t.Fatalf("Prune(at revocation) = %d, %v, want 0", n, err)
	}

	if n, err := tokens.Prune(ctx, now.Add(time.Second)); err != nil || n != 1 {
		t.Fatalf("Prune(after revocation) = %d, %v, want 1", n, err)
	}
}
