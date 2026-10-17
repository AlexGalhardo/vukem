package session

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/AlexGalhardo/vukem/internal/testutil"
	"github.com/AlexGalhardo/vukem/web"
)

func openSQLite(t *testing.T) *sql.DB {
	t.Helper()

	db, err := sql.Open("sqlite", "file:"+filepath.Join(testutil.TempDir(t), "s.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	return db
}

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// A read of the sessions table yields no usable cookie: rows are keyed by
// SHA-256(id), and the stored key does not load a session.
func TestSecurity_A04_SessionIDsStoredHashed(t *testing.T) {
	t.Parallel()

	db := openSQLite(t)

	store, err := NewSQLStore(db)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CreateTable(t.Context()); err != nil {
		t.Fatal(err)
	}

	m, clk := newManager(t, Config{Store: store})
	store.setClock(clk.Now)
	c := newClient(t, appHandler(m, &bytes.Buffer{}))

	c.do("GET", "/put?v=alice", nil)
	id := c.cookies[m.CookieName()].Value

	var stored string
	if err := db.QueryRowContext(t.Context(), "SELECT id FROM sessions").Scan(&stored); err != nil {
		t.Fatal(err)
	}

	if stored == id || stored != sha256Hex(id) {
		t.Fatalf("stored id = %q, want SHA-256 of the cookie value %q", stored, id)
	}

	if _, found, err := store.Load(t.Context(), stored); err != nil || found {
		t.Fatalf("Load(stored hash) = found %v, %v; want not found", found, err)
	}

	if got := c.do("GET", "/get", nil).Body.String(); got != "alice" {
		t.Fatalf("get with cookie = %q, want alice", got)
	}
}

// Rows written before ids were hashed keep working until they expire; the
// first load re-keys the row, so the clear id leaves the table.
func TestSQLStoreMigratesClearIDs(t *testing.T) {
	t.Parallel()

	db := openSQLite(t)
	ctx := t.Context()

	if _, err := db.ExecContext(ctx,
		"CREATE TABLE sessions (id TEXT PRIMARY KEY, data TEXT NOT NULL, expires_at BIGINT NOT NULL)"); err != nil {
		t.Fatal(err)
	}

	const live, expired = "ABCDEFGHIJKLMNOPQRSTUVWXYZ", "ZYXWVUTSRQPONMLKJIHGFEDCBA"

	now := time.Now()
	if _, err := db.ExecContext(ctx, "INSERT INTO sessions (id, data, expires_at) VALUES (?, ?, ?), (?, ?, ?)",
		live, `{"v":1}`, now.Add(time.Hour).Unix(), expired, `{"v":2}`, now.Add(-time.Minute).Unix()); err != nil {
		t.Fatal(err)
	}

	store, err := NewSQLStore(db)
	if err != nil {
		t.Fatal(err)
	}

	for range 2 { // idempotent upgrade
		if err := store.CreateTable(ctx); err != nil {
			t.Fatalf("CreateTable() on legacy table = %v", err)
		}
	}

	for range 2 {
		data, found, err := store.Load(ctx, live)
		if err != nil || !found || string(data) != `{"v":1}` {
			t.Fatalf("Load(legacy) = %s %v %v", data, found, err)
		}
	}

	if _, found, _ := store.Load(ctx, expired); found {
		t.Fatal("expired legacy row loaded")
	}

	var ids []string

	rows, err := db.QueryContext(ctx, "SELECT id FROM sessions WHERE expires_at > ? ORDER BY id", now.Unix())
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()

	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}

	if diff := cmp.Diff([]string{sha256Hex(live)}, ids); diff != "" {
		t.Fatalf("live ids after migration (-want +got):\n%s", diff)
	}

	if err := store.Delete(ctx, live); err != nil {
		t.Fatal(err)
	}
	if _, found, _ := store.Load(ctx, live); found {
		t.Fatal("session loaded after Delete")
	}
}

// RevokeOthers ends every other session of the user and nothing else.
func TestSecurity_A07_RevokeOtherSessions(t *testing.T) {
	t.Parallel()

	for name, store := range storesUnderTest(t) {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			m, clk := newManager(t, Config{Store: store})
			if sq, ok := store.(*SQLStore); ok {
				sq.setClock(clk.Now)
			}
			if ms, ok := store.(*MemoryStore); ok {
				ms.setClock(clk.Now)
			}

			h := appHandler(m, &bytes.Buffer{})
			current, other, stranger := newClient(t, h), newClient(t, h), newClient(t, h)

			keep := current.do("GET", "/as?u=7", nil).Body.String()
			other.do("GET", "/as?u=7", nil)
			stranger.do("GET", "/as?u=8", nil)

			for _, c := range []*client{current, other, stranger} {
				c.do("GET", "/put?v=in", nil)
			}

			err := m.RevokeOthers(t.Context(), "7", keep)

			if store == nil {
				if !errors.Is(err, ErrRevokeUnsupported) {
					t.Fatalf("RevokeOthers() on cookie sessions = %v, want ErrRevokeUnsupported", err)
				}

				return
			}

			if err != nil {
				t.Fatalf("RevokeOthers() = %v", err)
			}

			got := map[string]string{
				"current":  current.do("GET", "/get", nil).Body.String(),
				"other":    other.do("GET", "/get", nil).Body.String(),
				"stranger": stranger.do("GET", "/get", nil).Body.String(),
			}
			want := map[string]string{"current": "in", "other": "", "stranger": "in"}

			if diff := cmp.Diff(want, got); diff != "" {
				t.Fatalf("sessions after RevokeOthers (-want +got):\n%s", diff)
			}

			if err := m.RevokeOthers(t.Context(), "", ""); err == nil {
				t.Fatal("RevokeOthers() without a user id = nil error")
			}

			// An empty keepID ends every session of the user.
			if err := m.RevokeOthers(t.Context(), "7", ""); err != nil {
				t.Fatal(err)
			}
			if got := current.do("GET", "/get", nil).Body.String(); got != "" {
				t.Fatalf("current session after RevokeOthers(keep \"\") = %q, want signed out", got)
			}
		})
	}
}

// A request that was in flight when its session was revoked must not
// write the row back: existing sessions are updated, never re-inserted.
func TestSecurity_A07_RevokedSessionNotResurrected(t *testing.T) {
	t.Parallel()

	for name, store := range storesUnderTest(t) {
		if store == nil {
			continue // cookie sessions cannot be revoked
		}

		t.Run(name, func(t *testing.T) {
			t.Parallel()

			m, clk := newManager(t, Config{Store: store})
			setStoreClock(store, clk.Now)

			mux := http.NewServeMux()
			mux.Handle("GET /stale", m.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				s := web.SessionFrom(r.Context())
				_ = s.Get("user") // loaded before the revocation
				if err := m.RevokeOthers(r.Context(), "7", ""); err != nil {
					t.Error(err)
				}
				s.Put("user", "stale write")
				_, _ = w.Write([]byte("ok"))
			})))

			login := newClient(t, appHandler(m, &bytes.Buffer{}))
			id := login.do("GET", "/as?u=7", nil).Body.String()

			stale := newClient(t, mux)
			stale.cookies = login.cookies

			rec := stale.do("GET", "/stale", nil)

			if _, found, err := store.Load(t.Context(), id); err != nil || found {
				t.Fatalf("Load(revoked id) after a stale write = found %v, %v; want gone", found, err)
			}

			for _, ck := range rec.Result().Cookies() {
				if ck.Name == m.CookieName() && ck.MaxAge >= 0 {
					t.Fatalf("stale request set session cookie %v, want it cleared", ck)
				}
			}
		})
	}
}

// A legacy row deleted (revoked, logged out) between its read and its
// re-key is not accepted.
func TestSQLStoreLegacyRowDeletedDuringRekey(t *testing.T) {
	t.Parallel()

	db := openSQLite(t)
	ctx := t.Context()

	if _, err := db.ExecContext(ctx,
		"CREATE TABLE sessions (id TEXT PRIMARY KEY, data TEXT NOT NULL, expires_at BIGINT NOT NULL)"); err != nil {
		t.Fatal(err)
	}

	const legacy = "ABCDEFGHIJKLMNOPQRSTUVWXYZ"
	if _, err := db.ExecContext(ctx, "INSERT INTO sessions (id, data, expires_at) VALUES (?, ?, ?)",
		legacy, `{"v":1}`, time.Now().Add(time.Hour).Unix()); err != nil {
		t.Fatal(err)
	}

	store, err := NewSQLStore(db)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CreateTable(ctx); err != nil {
		t.Fatal(err)
	}

	store.beforeRekey = func() {
		if _, err := db.ExecContext(ctx, "DELETE FROM sessions WHERE id = ?", legacy); err != nil {
			t.Error(err)
		}
	}

	if data, found, err := store.Load(ctx, legacy); err != nil || found {
		t.Fatalf("Load(legacy deleted during rekey) = %s %v %v, want not found", data, found, err)
	}
}

func setStoreClock(store Store, now func() time.Time) {
	switch s := store.(type) {
	case *SQLStore:
		s.setClock(now)
	case *MemoryStore:
		s.setClock(now)
	}
}
