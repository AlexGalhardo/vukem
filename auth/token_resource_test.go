package auth_test

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/AlexGalhardo/vukem/auth"
	"github.com/AlexGalhardo/vukem/auth/hash"
	"github.com/AlexGalhardo/vukem/database"
	"github.com/AlexGalhardo/vukem/internal/testutil"
	"github.com/AlexGalhardo/vukem/schema"
)

func TestTokensCreateWithResourceRoundTrips(t *testing.T) {
	t.Parallel()

	ta := newApp(t, hash.Fake())
	u := ta.user(t, "ana@example.com", "pw", true)
	ctx := t.Context()

	plain, tok, err := ta.tokens.CreateWith(ctx, u.AuthID(), "ci", []string{"read"}, time.Hour,
		auth.CreateOptions{ID: "key-1", Resource: "project-9"})
	if err != nil {
		t.Fatalf("CreateWith() = %v", err)
	}

	if tok.ID != "key-1" || tok.Resource != "project-9" {
		t.Errorf("CreateWith() token = %+v, want id key-1 and resource project-9", tok)
	}

	got, err := ta.tokens.Find(ctx, plain)
	if err != nil || got.Resource != "project-9" {
		t.Fatalf("Find() = %+v, %v, want resource project-9", got, err)
	}

	// Without options it behaves like Create: generated id, no resource.
	plain2, tok2, err := ta.tokens.CreateWith(ctx, u.AuthID(), "plain", nil, time.Hour, auth.CreateOptions{})
	if err != nil || tok2.ID == "" || tok2.Resource != "" {
		t.Fatalf("CreateWith(empty) = %+v, %v", tok2, err)
	}

	if got, err := ta.tokens.Find(ctx, plain2); err != nil || got.Resource != "" {
		t.Fatalf("Find() = %+v, %v, want no resource", got, err)
	}

	list, err := ta.tokens.List(ctx, u.AuthID())
	if err != nil || len(list) != 2 {
		t.Fatalf("List() = %v, %v, want 2 tokens", list, err)
	}

	for _, l := range list {
		if want := map[string]string{"key-1": "project-9"}[l.ID]; l.Resource != want {
			t.Errorf("List() %s resource = %q, want %q", l.ID, l.Resource, want)
		}
	}
}

func TestTokensCreateWithRefusesBadOptions(t *testing.T) {
	t.Parallel()

	ta := newApp(t, hash.Fake())
	u := ta.user(t, "ana@example.com", "pw", true)

	for _, o := range []auth.CreateOptions{{ID: "a_b"}, {Resource: strings.Repeat("x", 256)}} {
		if _, _, err := ta.tokens.CreateWith(t.Context(), u.AuthID(), "x", nil, time.Hour, o); err == nil {
			t.Errorf("CreateWith(%+v) = nil, want an error", o)
		}
	}
}

func TestTokensResourceNeedsMigration(t *testing.T) {
	t.Parallel()

	ctx := t.Context()

	db, err := database.Open(ctx, "file:"+filepath.Join(testutil.TempDir(t), "tokens.db"))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = db.Close() })

	// The table before the resource column existed.
	base := schema.Migration{ID: "1_access_tokens", Up: func(s *schema.Schema) error {
		return s.Create("access_tokens", func(t *schema.Table) {
			t.String("id").Unique()
			t.String("user_id").Index()
			t.String("name")
			t.String("hash")
			t.Text("abilities")
			t.Timestamp("last_used_at").Nullable()
			t.Timestamp("expires_at").Nullable()
			t.Timestamp("revoked_at").Nullable()
			t.Timestamp("created_at")
		})
	}}

	migrate := func(ms ...schema.Migration) {
		t.Helper()

		m, err := schema.NewMigrator(db, ms)
		if err != nil {
			t.Fatal(err)
		}

		if _, err := m.Up(ctx); err != nil {
			t.Fatal(err)
		}
	}

	migrate(base)

	old := auth.NewTokens(db)

	plain, _, err := old.Create(ctx, "1", "cli", nil, time.Hour)
	if err != nil {
		t.Fatal(err)
	}

	if got, err := old.Find(ctx, plain); err != nil || got.Resource != "" {
		t.Fatalf("Find() on a table without resource = %+v, %v, want the token", got, err)
	}

	if _, err := old.List(ctx, "1"); err != nil {
		t.Fatalf("List() on a table without resource = %v", err)
	}

	_, _, err = old.CreateWith(ctx, "1", "x", nil, time.Hour, auth.CreateOptions{Resource: "p"})
	if err == nil || !strings.Contains(err.Error(), "TokensResourceMigration") {
		t.Fatalf("CreateWith(resource) without the column = %v, want an error naming TokensResourceMigration", err)
	}

	up := auth.TokensResourceMigration("2_access_tokens_resource")
	migrate(base, up)
	migrate(base, up, auth.TokensResourceMigration("3_again"))

	fresh := auth.NewTokens(db)

	plain2, _, err := fresh.CreateWith(ctx, "1", "x", nil, time.Hour, auth.CreateOptions{Resource: "p"})
	if err != nil {
		t.Fatalf("CreateWith() after the migration = %v", err)
	}

	if got, err := fresh.Find(ctx, plain2); err != nil || got.Resource != "p" {
		t.Fatalf("Find() = %+v, %v, want resource p", got, err)
	}

	if got, err := fresh.Find(ctx, plain); err != nil || got.Resource != "" {
		t.Fatalf("Find(old token) = %+v, %v, want no resource", got, err)
	}
}

func TestTokensResourceAppearsAfterMigrationWithoutRestart(t *testing.T) {
	t.Parallel()

	ctx := t.Context()

	db, err := database.Open(ctx, "file:"+filepath.Join(testutil.TempDir(t), "tokens.db"))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = db.Close() })

	base := schema.Migration{ID: "1_access_tokens", Up: func(s *schema.Schema) error {
		return s.Create("access_tokens", func(t *schema.Table) {
			t.String("id").Unique()
			t.String("user_id").Index()
			t.String("name")
			t.String("hash")
			t.Text("abilities")
			t.Timestamp("last_used_at").Nullable()
			t.Timestamp("expires_at").Nullable()
			t.Timestamp("revoked_at").Nullable()
			t.Timestamp("created_at")
		})
	}}

	migrate := func(ms ...schema.Migration) {
		t.Helper()

		m, err := schema.NewMigrator(db, ms)
		if err != nil {
			t.Fatal(err)
		}

		if _, err := m.Up(ctx); err != nil {
			t.Fatal(err)
		}
	}

	migrate(base)

	long := auth.NewTokens(db)

	first, _, err := long.Create(ctx, "1", "a", nil, time.Hour)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := long.Find(ctx, first); err != nil {
		t.Fatal(err)
	}

	// Another process migrates and issues a resource token; this Tokens was
	// never restarted.
	migrate(base, auth.TokensResourceMigration("2_resource"))

	plain, _, err := auth.NewTokens(db).CreateWith(ctx, "1", "b", nil, time.Hour, auth.CreateOptions{Resource: "p"})
	if err != nil {
		t.Fatal(err)
	}

	if got, err := long.Find(ctx, plain); err != nil || got.Resource != "p" {
		t.Fatalf("Find() = %+v, %v, want resource p without a restart", got, err)
	}
}
