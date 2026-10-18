package auth_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/AlexGalhardo/vukem"
	"github.com/AlexGalhardo/vukem/auth"
	"github.com/AlexGalhardo/vukem/auth/hash"
	"github.com/AlexGalhardo/vukem/database"
	"github.com/AlexGalhardo/vukem/errs"
	"github.com/AlexGalhardo/vukem/internal/testutil"
	"github.com/AlexGalhardo/vukem/schema"
	"github.com/AlexGalhardo/vukem/web"
)

func TestSecurity_A07_RevokedTokenRejected(t *testing.T) {
	t.Parallel()

	ta := newApp(t, hash.Fake())
	u := ta.user(t, "ana@example.com", "pw", true)
	ctx := t.Context()
	now := time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)
	ta.tokens.Now = func() time.Time { return now }

	plain, tok, err := ta.tokens.Create(ctx, u.AuthID(), "cli", []string{"*"}, 0)
	if err != nil {
		t.Fatal(err)
	}

	ta.Client(t).AcceptJSON().WithToken(plain).GetJSON("/api/me").AssertOK()

	now = now.Add(time.Hour)
	if err := ta.tokens.Revoke(ctx, tok.ID); err != nil {
		t.Fatal(err)
	}

	revokedAt := now
	now = now.Add(time.Hour)

	if _, err := ta.tokens.Find(ctx, plain); err == nil {
		t.Fatal("Find(revoked token) = nil error, want rejected")
	}

	ta.Client(t).AcceptJSON().WithToken(plain).GetJSON("/api/me").AssertUnauthorized()

	// The row is kept for the audit trail and listed with its revocation
	// time; a rejected use does not touch last_used_at.
	list, err := ta.tokens.List(ctx, u.AuthID())
	if err != nil || len(list) != 1 {
		t.Fatalf("List() = %+v, %v, want the revoked token", list, err)
	}

	if got := list[0]; got.ID != tok.ID || got.RevokedAt == nil || !got.RevokedAt.Equal(revokedAt) ||
		got.LastUsedAt == nil || !got.LastUsedAt.Equal(revokedAt.Add(-time.Hour)) {
		t.Errorf("List()[0] = %+v, want revoked at %v, last used at %v", got, revokedAt, revokedAt.Add(-time.Hour))
	}

	// Revoking again keeps the first revocation time.
	if ok, err := ta.tokens.RevokeFor(ctx, u.AuthID(), tok.ID); err != nil || ok {
		t.Errorf("RevokeFor(already revoked) = %v, %v, want false, nil", ok, err)
	}

	if list, _ := ta.tokens.List(ctx, u.AuthID()); len(list) != 1 || !list[0].RevokedAt.Equal(revokedAt) {
		t.Errorf("List() after second revoke = %+v, want revoked at %v", list, revokedAt)
	}

	// RevokeAll is a soft revoke too.
	other, otherTok, _ := ta.tokens.Create(ctx, u.AuthID(), "other", nil, 0)
	if err := ta.tokens.RevokeAll(ctx, u.AuthID()); err != nil {
		t.Fatal(err)
	}

	if _, err := ta.tokens.Find(ctx, other); err == nil {
		t.Error("Find(token after RevokeAll) = nil error, want rejected")
	}

	list, _ = ta.tokens.List(ctx, u.AuthID())
	for _, got := range list {
		if got.RevokedAt == nil {
			t.Errorf("List() after RevokeAll: token %s not revoked", got.ID)
		}

		if got.ID == tok.ID && !got.RevokedAt.Equal(revokedAt) {
			t.Errorf("RevokeAll moved the first revocation to %v, want %v", got.RevokedAt, revokedAt)
		}
	}

	if len(list) != 2 || list[0].ID != otherTok.ID {
		t.Errorf("List() after RevokeAll = %+v, want both tokens, newest first", list)
	}
}

func TestTokensPrune(t *testing.T) {
	t.Parallel()

	ta := newApp(t, hash.Fake())
	u := ta.user(t, "ana@example.com", "pw", true)
	ctx := t.Context()
	now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	ta.tokens.Now = func() time.Time { return now }

	_, oldRevoked, _ := ta.tokens.Create(ctx, u.AuthID(), "old-revoked", nil, 0)
	_, oldExpired, _ := ta.tokens.Create(ctx, u.AuthID(), "old-expired", nil, time.Hour)
	_ = ta.tokens.Revoke(ctx, oldRevoked.ID)

	now = now.Add(30 * 24 * time.Hour)
	_, recentRevoked, _ := ta.tokens.Create(ctx, u.AuthID(), "recent-revoked", nil, 0)
	_, live, _ := ta.tokens.Create(ctx, u.AuthID(), "live", nil, 0)
	_ = ta.tokens.Revoke(ctx, recentRevoked.ID)

	n, err := ta.tokens.Prune(ctx, now.Add(-24*time.Hour))
	if err != nil || n != 2 {
		t.Fatalf("Prune() = %d, %v, want 2, nil", n, err)
	}

	list, _ := ta.tokens.List(ctx, u.AuthID())

	got := map[string]bool{}
	for _, tok := range list {
		got[tok.ID] = true
	}

	if got[oldRevoked.ID] || got[oldExpired.ID] || !got[recentRevoked.ID] || !got[live.ID] || len(got) != 2 {
		t.Errorf("tokens after Prune = %+v, want only the recent revoked and live ones", list)
	}
}

func TestTokensRevokedAtMigration(t *testing.T) {
	t.Parallel()

	ctx := t.Context()

	db, err := database.Open(ctx, "file:"+filepath.Join(testutil.TempDir(t), "tokens.db"))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = db.Close() })

	// A table created by a scaffold older than revoked_at.
	legacy := schema.Migration{ID: "1_access_tokens", Up: func(s *schema.Schema) error {
		return s.Create("access_tokens", func(t *schema.Table) {
			t.String("id").Unique()
			t.String("user_id").Index()
			t.String("name")
			t.String("hash")
			t.Text("abilities")
			t.Timestamp("last_used_at").Nullable()
			t.Timestamp("expires_at").Nullable()
			t.Timestamp("created_at")
		})
	}}

	tokens := auth.NewTokens(db)

	m, err := schema.NewMigrator(db, []schema.Migration{legacy})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := m.Up(ctx); err != nil {
		t.Fatal(err)
	}

	plain, _, err := tokens.Create(ctx, "1", "cli", nil, 0)
	if err != nil {
		t.Fatal(err)
	}

	// Without the column every token is refused: fail closed.
	if _, err := tokens.Find(ctx, plain); err == nil {
		t.Fatal("Find() without revoked_at = nil error, want rejected")
	}

	upgrade := auth.TokensRevokedAtMigration("2_access_tokens_revoked_at")

	m, err = schema.NewMigrator(db, []schema.Migration{legacy, upgrade})
	if err != nil {
		t.Fatal(err)
	}

	if ran, err := m.Up(ctx); err != nil || len(ran) != 1 {
		t.Fatalf("Up() = %v, %v, want the upgrade", ran, err)
	}

	if _, err := tokens.Find(ctx, plain); err != nil {
		t.Fatalf("Find() after the upgrade = %v, want the token", err)
	}

	// On a table that already has the column (new scaffold) it is a no-op.
	if err := upgrade.Up(schema.New(ctx, db)); err != nil {
		t.Errorf("upgrade on a current table = %v, want nil", err)
	}
}

// syncBuffer is a log sink safe for concurrent handlers.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.b.String()
}

func TestSecurity_A04_TokenGuardStoreFailureIs503(t *testing.T) {
	t.Parallel()

	logs := &syncBuffer{}
	ta := newApp(t, hash.Fake(), vukem.WithLogger(slog.New(slog.NewTextHandler(logs, nil))))
	u := ta.user(t, "ana@example.com", "pw", true)
	ctx := t.Context()

	plain, _, err := ta.tokens.Create(ctx, u.AuthID(), "cli", []string{"*"}, 0)
	if err != nil {
		t.Fatal(err)
	}

	// Unknown, wrong and revoked tokens stay 401.
	ta.Client(t).WithToken("nope.nope").Get("/v1/me").AssertUnauthorized()

	db, _ := ta.DB()

	// An access_tokens table from before revoked_at: the lookup fails, which
	// is an infrastructure failure (503), not a bad token (401).
	if _, err := db.ExecContext(ctx, "ALTER TABLE access_tokens DROP COLUMN revoked_at"); err != nil {
		t.Fatal(err)
	}

	for range 2 {
		res := ta.Client(t).WithHeader("Accept", "text/html").WithToken(plain).Get("/v1/me")
		if res.Status() != http.StatusServiceUnavailable ||
			!strings.HasPrefix(res.Header("Content-Type"), "application/problem+json") {
			t.Fatalf("GET /v1/me without revoked_at = %d %q, want 503 problem+json", res.Status(),
				res.Header("Content-Type"))
		}

		if strings.Contains(res.Body(), "revoked_at") {
			t.Errorf("503 body leaks the database error: %s", res.Body())
		}
	}

	if n := strings.Count(logs.String(), "auth.TokensRevokedAtMigration"); n != 1 {
		t.Errorf("logs name auth.TokensRevokedAtMigration %d times, want once:\n%s", n, logs)
	}
}

// errUsers answers every lookup with err.
type errUsers struct{ err error }

func (p errUsers) ByID(context.Context, string) (User, error) { return User{}, p.err }

func (p errUsers) ByCredentials(context.Context, map[string]string) (User, error) {
	return User{}, p.err
}

func TestTokenGuardUserLookupErrors(t *testing.T) {
	t.Parallel()

	ta := newApp(t, hash.Fake())
	u := ta.user(t, "ana@example.com", "pw", true)

	plain, _, err := ta.tokens.Create(t.Context(), u.AuthID(), "cli", nil, 0)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name   string
		err    error
		status int
	}{
		{"user deleted", auth.ErrUserNotFound, http.StatusUnauthorized},
		{"wrapped sql.ErrNoRows", fmt.Errorf("load: %w", sql.ErrNoRows), http.StatusUnauthorized},
		{"database down", errors.New("connection refused"), http.StatusServiceUnavailable},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			g := auth.NewTokenGuard[User]("api", errUsers{tt.err}, ta.tokens)
			r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
			r.Header.Set("Authorization", "Bearer "+plain)

			_, err := g.User(web.NewCtx(httptest.NewRecorder(), r, nil))

			var he *errs.HTTPError
			if !errors.As(err, &he) || he.Status != tt.status {
				t.Errorf("User() = %v, want status %d", err, tt.status)
			}
		})
	}
}

func TestTokenGuardBearerSchemeCaseInsensitive(t *testing.T) {
	t.Parallel()

	ta := newApp(t, hash.Fake())
	u := ta.user(t, "ana@example.com", "pw", true)

	plain, _, err := ta.tokens.Create(t.Context(), u.AuthID(), "cli", []string{"*"}, 0)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		authz string
		want  int
	}{
		{"Bearer " + plain, http.StatusNoContent},
		{"bearer " + plain, http.StatusNoContent},
		{"BEARER  " + plain + " ", http.StatusNoContent},
		{"Bearer" + plain, http.StatusUnauthorized},
		{"Bearer ", http.StatusUnauthorized},
		{"Basic " + plain, http.StatusUnauthorized},
	}

	for _, tt := range tests {
		if got := ta.Client(t).WithHeader("Authorization", tt.authz).Get("/v1/me").Status(); got != tt.want {
			t.Errorf("GET /v1/me with Authorization %.10q... = %d, want %d", tt.authz, got, tt.want)
		}
	}
}

func TestTokensRevokeOnLegacyTable(t *testing.T) {
	t.Parallel()

	logs := &syncBuffer{}
	ta := newApp(t, hash.Fake())
	ana := ta.user(t, "ana@example.com", "pw", true)
	bob := ta.user(t, "bob@example.com", "pw", true)
	ctx := t.Context()
	ta.tokens.Log = slog.New(slog.NewTextHandler(logs, nil))

	plain, tok, _ := ta.tokens.Create(ctx, ana.AuthID(), "a", nil, 0)
	_, tok2, _ := ta.tokens.Create(ctx, ana.AuthID(), "b", nil, 0)
	_, bobTok, _ := ta.tokens.Create(ctx, bob.AuthID(), "c", nil, 0)

	db, _ := ta.DB()
	if _, err := db.ExecContext(ctx, "ALTER TABLE access_tokens DROP COLUMN revoked_at"); err != nil {
		t.Fatal(err)
	}

	count := func(id string) int {
		var n int
		if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM access_tokens WHERE id = ?", id).Scan(&n); err != nil {
			t.Fatal(err)
		}

		return n
	}

	// Revoking stays possible (and safe) before the upgrade: the row is
	// deleted instead.
	if ok, err := ta.tokens.RevokeFor(ctx, bob.AuthID(), tok.ID); err != nil || ok || count(tok.ID) != 1 {
		t.Fatalf("RevokeFor(bob, ana's token) on a legacy table = %v, %v, want false, nil, row kept", ok, err)
	}

	if ok, err := ta.tokens.RevokeFor(ctx, ana.AuthID(), tok.ID); err != nil || !ok || count(tok.ID) != 0 {
		t.Fatalf("RevokeFor(ana, own token) on a legacy table = %v, %v, want true, nil, row deleted", ok, err)
	}

	if err := ta.tokens.Revoke(ctx, tok2.ID); err != nil || count(tok2.ID) != 0 {
		t.Fatalf("Revoke() on a legacy table = %v, want nil, row deleted", err)
	}

	// The password reset of install:auth runs RevokeAll in a transaction.
	if err := db.Tx(ctx, func(tx *database.Tx) error {
		return auth.NewTokens(tx).RevokeAll(ctx, bob.AuthID())
	}); err != nil || count(bobTok.ID) != 0 {
		t.Fatalf("RevokeAll() in a transaction on a legacy table = %v, want nil, row deleted", err)
	}

	if n := strings.Count(logs.String(), "auth.TokensRevokedAtMigration"); n != 1 {
		t.Errorf("logs name auth.TokensRevokedAtMigration %d times, want once:\n%s", n, logs)
	}

	// DELETE /tokens/current cannot authenticate on a legacy table: the
	// lookup fails, which is a 503, never a 500 or a 401.
	ta.Client(t).WithToken(plain).Get("/v1/me").AssertStatus(http.StatusServiceUnavailable)
}

func TestTokensPruneKeepsLiveTokens(t *testing.T) {
	t.Parallel()

	ta := newApp(t, hash.Fake())
	u := ta.user(t, "ana@example.com", "pw", true)
	ctx := t.Context()
	now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	ta.tokens.Now = func() time.Time { return now }

	plain, _, _ := ta.tokens.Create(ctx, u.AuthID(), "month", nil, 30*24*time.Hour)

	// A cutoff in the future must not delete tokens that have not expired.
	if n, err := ta.tokens.Prune(ctx, now.AddDate(1, 0, 0)); err != nil || n != 0 {
		t.Fatalf("Prune(next year) = %d, %v, want 0, nil", n, err)
	}

	if _, err := ta.tokens.Find(ctx, plain); err != nil {
		t.Fatalf("Find() after Prune(next year) = %v, want the live token", err)
	}
}
