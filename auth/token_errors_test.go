package auth_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/AlexGalhardo/vukem/auth"
	"github.com/AlexGalhardo/vukem/auth/hash"
	"github.com/AlexGalhardo/vukem/database"
)

func TestTokensFindSeparatesInvalidTokensFromStoreFailures(t *testing.T) {
	t.Parallel()

	ta := newApp(t, hash.Fake())
	u := ta.user(t, "ana@example.com", "pw", true)
	ctx := t.Context()

	plain, tok, err := ta.tokens.Create(ctx, u.AuthID(), "cli", nil, time.Hour)
	if err != nil {
		t.Fatal(err)
	}

	revoked, rtok, err := ta.tokens.Create(ctx, u.AuthID(), "old", nil, time.Hour)
	if err != nil {
		t.Fatal(err)
	}

	if err := ta.tokens.Revoke(ctx, rtok.ID); err != nil {
		t.Fatal(err)
	}

	secret := strings.SplitN(plain, ".", 2)[1]
	for _, bad := range []string{"", "nodot", tok.ID + ".wrong", "unknown." + secret, revoked} {
		if _, err := ta.tokens.Find(ctx, bad); !errors.Is(err, auth.ErrInvalidToken) {
			t.Errorf("Find(%q) = %v, want ErrInvalidToken", bad, err)
		}
	}

	// A database without the tokens table is a failing store, not a bad token.
	db, err := database.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if _, err := auth.NewTokens(db).Find(ctx, plain); err == nil || errors.Is(err, auth.ErrInvalidToken) {
		t.Fatalf("Find on a broken store = %v, want a store error", err)
	}
}
