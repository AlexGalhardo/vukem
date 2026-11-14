package auth_test

import (
	"strings"
	"testing"
	"time"

	"github.com/AlexGalhardo/vukem/auth/hash"
)

func TestTokensCreateWithIDRoundTrips(t *testing.T) {
	t.Parallel()

	ta := newApp(t, hash.Fake())
	u := ta.user(t, "ana@example.com", "pw", true)
	ctx := t.Context()
	ta.tokens.Prefix = "vk_"

	plain, tok, err := ta.tokens.CreateWithID(ctx, "key-01HZX", u.AuthID(), "ci", []string{"read"}, time.Hour)
	if err != nil {
		t.Fatalf("CreateWithID() = %v", err)
	}

	if tok.ID != "key-01HZX" || !strings.HasPrefix(plain, "vk_key-01HZX.") {
		t.Errorf("CreateWithID() = %q, %q, want the caller id in both", plain, tok.ID)
	}

	got, err := ta.tokens.Find(ctx, plain)
	if err != nil {
		t.Fatalf("Find(%q) = %v", plain, err)
	}

	if got.ID != tok.ID || got.UserID != u.AuthID() {
		t.Errorf("Find() = %+v, want id %q for %q", got, tok.ID, u.AuthID())
	}

	if _, _, err := ta.tokens.CreateWithID(ctx, "key-01HZX", u.AuthID(), "dup", nil, time.Hour); err == nil {
		t.Error("CreateWithID() with a stored id = nil, want a primary key error")
	}
}

func TestTokensCreateWithIDRefusesIDsFindCannotSplit(t *testing.T) {
	t.Parallel()

	ta := newApp(t, hash.Fake())
	u := ta.user(t, "ana@example.com", "pw", true)

	for _, id := range []string{"", "key_1", "a.b", "a b", "ü", strings.Repeat("a", 65)} {
		if _, _, err := ta.tokens.CreateWithID(t.Context(), id, u.AuthID(), "x", nil, time.Hour); err == nil {
			t.Errorf("CreateWithID(%q) = nil, want an error", id)
		}
	}
}
