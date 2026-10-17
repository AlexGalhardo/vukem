package session

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"regexp"
	"sync"
	"sync/atomic"
	"time"
)

// Store persists server-side sessions.
type Store interface {
	// Load returns the data saved for id; found is false when the session
	// does not exist or expired.
	Load(ctx context.Context, id string) (data []byte, found bool, err error)
	// Save stores data for id until expiry.
	Save(ctx context.Context, id string, data []byte, expiry time.Time) error
	// Delete removes id.
	Delete(ctx context.Context, id string) error
}

// ---------------------------------------------------------------- memory

// MemoryStore keeps sessions in process memory (development and tests).
type MemoryStore struct {
	mu    sync.Mutex
	items map[string]memItem
	clk   storeClock
}

type memItem struct {
	data   []byte
	expiry time.Time
}

// NewMemoryStore returns an empty MemoryStore.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{items: map[string]memItem{}}
}

// Load implements Store.
func (m *MemoryStore) Load(_ context.Context, id string) ([]byte, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	it, ok := m.items[id]
	if !ok {
		return nil, false, nil
	}

	if !m.now().Before(it.expiry) {
		delete(m.items, id)
		return nil, false, nil
	}

	return it.data, true, nil
}

// Save implements Store.
func (m *MemoryStore) Save(_ context.Context, id string, data []byte, expiry time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Opportunistic cleanup keeps memory bounded without a goroutine.
	now := m.now()
	for k, it := range m.items {
		if !now.Before(it.expiry) {
			delete(m.items, k)
		}
	}

	m.items[id] = memItem{data: append([]byte(nil), data...), expiry: expiry}

	return nil
}

// Delete implements Store.
func (m *MemoryStore) Delete(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	delete(m.items, id)

	return nil
}

// Len returns the number of stored sessions (tests).
func (m *MemoryStore) Len() int {
	m.mu.Lock()
	defer m.mu.Unlock()

	return len(m.items)
}

// --------------------------------------------------------------- database

// SQLStore keeps sessions in a SQL table (SQLite, PostgreSQL or MySQL):
//
//	CREATE TABLE sessions (id TEXT PRIMARY KEY, data TEXT NOT NULL, expires_at BIGINT NOT NULL)
type SQLStore struct {
	db          *sql.DB
	table       string
	placeholder func(n int) string
	clk         storeClock
	mysql       bool

	// Queries are built once from the validated table name.
	qCreate, qLoad, qSave, qDelete, qPrune string
}

// SQLOption configures NewSQLStore.
type SQLOption func(*SQLStore)

// WithTable sets the table name (default "sessions").
func WithTable(name string) SQLOption { return func(s *SQLStore) { s.table = name } }

// WithDollarPlaceholders uses $1, $2... (PostgreSQL) instead of ?.
func WithDollarPlaceholders() SQLOption {
	return func(s *SQLStore) { s.placeholder = func(n int) string { return fmt.Sprintf("$%d", n) } }
}

// WithMySQL uses MySQL/MariaDB DDL and upserts. The id column is
// VARBINARY so session ids compare exactly (MySQL's default collations are
// case-insensitive).
func WithMySQL() SQLOption { return func(s *SQLStore) { s.mysql = true } }

var tableNameRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// NewSQLStore returns a SQLStore. The table name is validated because it is
// interpolated into SQL (A05).
func NewSQLStore(db *sql.DB, opts ...SQLOption) (*SQLStore, error) {
	s := &SQLStore{db: db, table: "sessions", placeholder: func(int) string { return "?" }}
	for _, o := range opts {
		o(s)
	}

	if !tableNameRe.MatchString(s.table) {
		return nil, fmt.Errorf("session: invalid table name %q", s.table)
	}

	// The table name matched tableNameRe above; values always use placeholders.
	t, p := s.table, s.placeholder
	s.qCreate = "CREATE TABLE IF NOT EXISTS " + t + " (id TEXT PRIMARY KEY, data TEXT NOT NULL, expires_at BIGINT NOT NULL)"
	s.qLoad = "SELECT data FROM " + t + " WHERE id = " + p(1) + " AND expires_at > " + p(2)
	s.qSave = "INSERT INTO " + t + " (id, data, expires_at) VALUES (" + p(1) + ", " + p(2) + ", " + p(3) + ")" +
		" ON CONFLICT (id) DO UPDATE SET data = excluded.data, expires_at = excluded.expires_at"

	if s.mysql {
		s.qCreate = "CREATE TABLE IF NOT EXISTS " + t +
			" (id VARBINARY(255) NOT NULL PRIMARY KEY, data LONGTEXT NOT NULL, expires_at BIGINT NOT NULL)"
		s.qSave = "INSERT INTO " + t + " (id, data, expires_at) VALUES (" + p(1) + ", " + p(2) + ", " + p(3) + ")" +
			" ON DUPLICATE KEY UPDATE data = VALUES(data), expires_at = VALUES(expires_at)"
	}
	s.qDelete = "DELETE FROM " + t + " WHERE id = " + p(1)
	s.qPrune = "DELETE FROM " + t + " WHERE expires_at <= " + p(1)

	return s, nil
}

// CreateTable creates the sessions table if it does not exist.
func (s *SQLStore) CreateTable(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, s.qCreate)

	return err
}

// Load implements Store.
func (s *SQLStore) Load(ctx context.Context, id string) ([]byte, bool, error) {
	var data string

	//nolint:gosec // G701: query built once from a validated table name; values are bound parameters
	err := s.db.QueryRowContext(ctx, s.qLoad, id, s.now().Unix()).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}

	if err != nil {
		return nil, false, fmt.Errorf("session: load: %w", err)
	}

	return []byte(data), true, nil
}

// Save implements Store (upsert).
func (s *SQLStore) Save(ctx context.Context, id string, data []byte, expiry time.Time) error {
	if _, err := s.db.ExecContext(ctx, s.qSave, id, string(data), expiry.Unix()); err != nil {
		return fmt.Errorf("session: save: %w", err)
	}

	return nil
}

// Delete implements Store.
func (s *SQLStore) Delete(ctx context.Context, id string) error {
	if _, err := s.db.ExecContext(ctx, s.qDelete, id); err != nil {
		return fmt.Errorf("session: delete: %w", err)
	}

	return nil
}

// Prune deletes expired sessions; schedule it periodically.
func (s *SQLStore) Prune(ctx context.Context) (int64, error) {
	res, err := s.db.ExecContext(ctx, s.qPrune, s.now().Unix())
	if err != nil {
		return 0, fmt.Errorf("session: prune: %w", err)
	}

	return res.RowsAffected()
}

// ----------------------------------------------------------------- cookie

// maxCookieSize is the browser limit for a single cookie value.
const maxCookieSize = 4000

// ErrCookieTooLarge is returned when cookie session data exceeds 4 KB.
var ErrCookieTooLarge = errors.New("session: cookie session data exceeds 4 KB; use a server-side store")

// sealer encrypts and authenticates cookie payloads with AES-256-GCM.
type sealer struct {
	aead cipher.AEAD
}

func newSealer(key []byte) (*sealer, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("session: cookie key: %w", err)
	}

	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	return &sealer{aead: aead}, nil
}

// seal returns base64url(nonce || ciphertext). name is authenticated as
// additional data so a value cannot be moved between cookies.
func (s *sealer) seal(name string, plain []byte) string {
	nonce := make([]byte, s.aead.NonceSize())
	_, _ = rand.Read(nonce) // never fails since Go 1.24

	out := s.aead.Seal(nonce, nonce, plain, []byte(name))

	return base64.RawURLEncoding.EncodeToString(out)
}

func (s *sealer) open(name, value string) ([]byte, error) {
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || len(raw) < s.aead.NonceSize() {
		return nil, errors.New("session: malformed cookie")
	}

	n := s.aead.NonceSize()

	plain, err := s.aead.Open(nil, raw[:n], raw[n:], []byte(name))
	if err != nil {
		return nil, errors.New("session: cookie authentication failed")
	}

	return plain, nil
}

// storeClock is a store's time source: time.Now until session.New hands it the
// manager's Config.Now. Atomic because a store may be shared by managers or
// pruned while it serves requests.
type storeClock struct {
	fn atomic.Pointer[func() time.Time]
}

func (c *storeClock) now() time.Time {
	if fn := c.fn.Load(); fn != nil {
		return (*fn)()
	}

	return time.Now()
}

func (m *MemoryStore) now() time.Time { return m.clk.now() }

func (m *MemoryStore) setClock(now func() time.Time) { m.clk.fn.Store(&now) }

func (s *SQLStore) now() time.Time { return s.clk.now() }

func (s *SQLStore) setClock(now func() time.Time) { s.clk.fn.Store(&now) }
