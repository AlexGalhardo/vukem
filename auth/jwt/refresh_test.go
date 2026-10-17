package jwt_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/AlexGalhardo/vukem/auth"
	"github.com/AlexGalhardo/vukem/auth/jwt"
	"github.com/AlexGalhardo/vukem/database"
	"github.com/AlexGalhardo/vukem/router"
	"github.com/AlexGalhardo/vukem/schema"
	"github.com/AlexGalhardo/vukem/web"
)

func refreshStore(t *testing.T) (*jwt.RefreshTokens, *time.Time) {
	t.Helper()

	db, err := database.Open(t.Context(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = db.Close() })

	m, err := schema.NewMigrator(db, []schema.Migration{jwt.RefreshTokensMigration("1_refresh_tokens")})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := m.Up(t.Context()); err != nil {
		t.Fatal(err)
	}

	clock := now
	store := jwt.NewRefreshTokens(db)
	store.Now = func() time.Time { return clock }

	return store, &clock
}

func TestRefreshRotation(t *testing.T) {
	t.Parallel()

	store, clock := refreshStore(t)
	ctx := t.Context()

	first, err := store.Issue(ctx, "42")
	if err != nil {
		t.Fatal(err)
	}

	user, second, err := store.Rotate(ctx, first)
	if err != nil || user != "42" || second == first {
		t.Fatalf("Rotate(first) = %q, %q, %v", user, second, err)
	}

	user, third, err := store.Rotate(ctx, second)
	if err != nil || user != "42" {
		t.Fatalf("Rotate(second) = %q, %v", user, err)
	}

	// The old token comes back: theft. The whole family dies, including
	// the legitimate latest token.
	if _, _, err := store.Rotate(ctx, first); !errors.Is(err, jwt.ErrRefreshReused) || !errors.Is(err, jwt.ErrInvalid) {
		t.Fatalf("reused token = %v, want ErrRefreshReused", err)
	}

	if _, _, err := store.Rotate(ctx, third); !errors.Is(err, jwt.ErrInvalid) {
		t.Errorf("latest token after reuse = %v, want revoked", err)
	}

	other, err := store.Issue(ctx, "7")
	if err != nil {
		t.Fatal(err)
	}

	*clock = clock.Add(31 * 24 * time.Hour)

	if _, _, err := store.Rotate(ctx, other); !errors.Is(err, jwt.ErrInvalid) {
		t.Errorf("expired token = %v", err)
	}

	if _, _, err := store.Rotate(ctx, "nope"); !errors.Is(err, jwt.ErrInvalid) {
		t.Errorf("unknown token = %v", err)
	}
}

func TestRefreshRevoke(t *testing.T) {
	t.Parallel()

	store, _ := refreshStore(t)
	ctx := t.Context()

	a, _ := store.Issue(ctx, "1")
	b, _ := store.Issue(ctx, "1")
	c, _ := store.Issue(ctx, "2")

	if err := store.Revoke(ctx, a); err != nil {
		t.Fatal(err)
	}

	if _, _, err := store.Rotate(ctx, a); !errors.Is(err, jwt.ErrInvalid) {
		t.Errorf("revoked token = %v", err)
	}

	if err := store.RevokeUser(ctx, "1"); err != nil {
		t.Fatal(err)
	}

	if _, _, err := store.Rotate(ctx, b); !errors.Is(err, jwt.ErrInvalid) {
		t.Errorf("token of a revoked user = %v", err)
	}

	if _, _, err := store.Rotate(ctx, c); err != nil {
		t.Errorf("other user's token = %v", err)
	}
}

func TestSecurity_A07_RefreshConcurrentRotationSingleWinner(t *testing.T) {
	t.Parallel()

	store, _ := refreshStore(t)

	tok, err := store.Issue(t.Context(), "42")
	if err != nil {
		t.Fatal(err)
	}

	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		wins int
	)

	for range 8 {
		wg.Go(func() {
			if _, _, err := store.Rotate(context.WithoutCancel(t.Context()), tok); err == nil {
				mu.Lock()
				wins++
				mu.Unlock()
			}
		})
	}

	wg.Wait()

	if wins != 1 {
		t.Errorf("%d concurrent rotations of one token succeeded, want 1", wins)
	}
}

type user struct{ id string }

func (u user) AuthID() string     { return u.id }
func (user) AuthPassword() string { return "" }

type users map[string]user

func (us users) ByID(_ context.Context, id string) (user, error) {
	if u, ok := us[id]; ok {
		return u, nil
	}

	return user{}, auth.ErrUnauthenticated
}

func (users) ByCredentials(context.Context, map[string]string) (user, error) {
	return user{}, auth.ErrUnauthenticated
}

func TestGuard(t *testing.T) {
	t.Parallel()

	k := keys(t)[3]
	guard := jwt.NewGuard[user]("api", verifier(k), users{"42": {id: "42"}})

	r := router.New(nil)
	r.Get("/me", func(c *web.Ctx) error {
		u, err := auth.User[user](c, "api")
		if err != nil {
			return err
		}

		cl, _ := jwt.ClaimsFrom(c, "api")

		return c.String(http.StatusOK, u.id+" "+cl.Extra["role"].(string))
	}).Use(auth.Required(guard))

	h, err := r.Handler()
	if err != nil {
		t.Fatal(err)
	}

	call := func(authz string) *httptest.ResponseRecorder {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/me", http.NoBody)
		req.Header.Set("Accept", "application/json")

		if authz != "" {
			req.Header.Set("Authorization", authz)
		}

		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		return rec
	}

	good, err := jwt.Sign(k, claims())
	if err != nil {
		t.Fatal(err)
	}

	if rec := call("Bearer " + good); rec.Code != http.StatusOK || rec.Body.String() != "42 admin" {
		t.Errorf("valid token = %d %q", rec.Code, rec.Body.String())
	}

	stranger := claims()
	stranger.Subject = "99"

	ghost, _ := jwt.Sign(k, stranger)

	for name, authz := range map[string]string{
		"no header": "", "not bearer": "Basic abc", "garbage": "Bearer x.y.z", "unknown user": "Bearer " + ghost,
	} {
		if rec := call(
			authz,
		); rec.Code != http.StatusUnauthorized ||
			!strings.Contains(rec.Header().Get("WWW-Authenticate"), "Bearer") {
			t.Errorf("%s = %d %v", name, rec.Code, rec.Header())
		}
	}

	// A browser gets the same problem+json, never an HTML page.
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/me", http.NoBody)
	req.Header.Set("Accept", "text/html")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized ||
		!strings.HasPrefix(rec.Header().Get("Content-Type"), "application/problem+json") {
		t.Errorf("browser = %d %q, want 401 application/problem+json", rec.Code, rec.Header().Get("Content-Type"))
	}
}
