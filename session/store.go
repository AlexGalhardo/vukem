package session

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"
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

// UserStore is a Store that records which user owns each session, so all
// sessions of a user can be ended at once (password reset, 2FA enable).
// SQLStore and MemoryStore implement it; cookie sessions cannot.
type UserStore interface {
	Store
	// SaveUser is Save that also records userID ("" for a guest session).
	SaveUser(ctx context.Context, id, userID string, data []byte, expiry time.Time) error
	// RevokeOthers deletes every session of userID except keepID ("" keeps
	// none).
	RevokeOthers(ctx context.Context, userID, keepID string) error
}

// ErrRevokeUnsupported is returned by Manager.RevokeOthers when the
// sessions live in cookies or in a store that is not a UserStore: there is
// no server-side record to delete.
var ErrRevokeUnsupported = errors.New(
	"session: revoking sessions needs a server-side UserStore (SESSION_DRIVER=database)",
)

var errNoUser = errors.New("session: RevokeOthers needs a user id")

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
	userID string
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

// Save implements Store; the recorded user is kept.
func (m *MemoryStore) Save(_ context.Context, id string, data []byte, expiry time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.put(id, m.items[id].userID, data, expiry)

	return nil
}

// SaveUser implements UserStore.
func (m *MemoryStore) SaveUser(_ context.Context, id, userID string, data []byte, expiry time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.put(id, userID, data, expiry)

	return nil
}

// RevokeOthers implements UserStore.
func (m *MemoryStore) RevokeOthers(_ context.Context, userID, keepID string) error {
	if userID == "" {
		return errNoUser
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	for k, it := range m.items {
		if it.userID == userID && k != keepID {
			delete(m.items, k)
		}
	}

	return nil
}

// put stores an item; the caller holds mu.
func (m *MemoryStore) put(id, userID string, data []byte, expiry time.Time) {
	// Opportunistic cleanup keeps memory bounded without a goroutine.
	now := m.now()
	for k, it := range m.items {
		if !now.Before(it.expiry) {
			delete(m.items, k)
		}
	}

	m.items[id] = memItem{data: append([]byte(nil), data...), expiry: expiry, userID: userID}
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
//	CREATE TABLE sessions (id TEXT PRIMARY KEY, data TEXT NOT NULL, expires_at BIGINT NOT NULL,
//		user_id VARCHAR(255))
//
// The id column holds hex(SHA-256(session id)), never the cookie value, so
// reading the table (backup, SQL injection elsewhere, replica access) does
// not yield session cookies. Rows written before ids were hashed are
// re-keyed on their first load and otherwise expire as usual.
type SQLStore struct {
	db          *sql.DB
	table       string
	placeholder func(n int) string
	clk         storeClock
	mysql       bool

	// Queries are built once from the validated table name.
	qCreate, qLoad, qSave, qSaveUser, qRekey, qDelete, qPrune, qRevoke string
	qHasUser, qAddUser, qIndex                                         string
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
	s.qCreate = "CREATE TABLE IF NOT EXISTS " + t +
		" (id TEXT PRIMARY KEY, data TEXT NOT NULL, expires_at BIGINT NOT NULL, user_id VARCHAR(255))"
	s.qLoad = "SELECT data FROM " + t + " WHERE id = " + p(1) + " AND expires_at > " + p(2)
	s.qSave = "INSERT INTO " + t + " (id, data, expires_at) VALUES (" + p(1) + ", " + p(2) + ", " + p(3) + ")" +
		" ON CONFLICT (id) DO UPDATE SET data = excluded.data, expires_at = excluded.expires_at"
	s.qSaveUser = "INSERT INTO " + t + " (id, data, expires_at, user_id) VALUES (" +
		p(1) + ", " + p(2) + ", " + p(3) + ", " + p(4) + ")" +
		" ON CONFLICT (id) DO UPDATE SET data = excluded.data, expires_at = excluded.expires_at, user_id = excluded.user_id"
	s.qIndex = "CREATE INDEX IF NOT EXISTS " + t + "_user_id ON " + t + " (user_id)"

	if s.mysql {
		s.qCreate = "CREATE TABLE IF NOT EXISTS " + t +
			" (id VARBINARY(255) NOT NULL PRIMARY KEY, data LONGTEXT NOT NULL, expires_at BIGINT NOT NULL," +
			" user_id VARCHAR(255) NULL)"
		s.qSave = "INSERT INTO " + t + " (id, data, expires_at) VALUES (" + p(1) + ", " + p(2) + ", " + p(3) + ")" +
			" ON DUPLICATE KEY UPDATE data = VALUES(data), expires_at = VALUES(expires_at)"
		s.qSaveUser = "INSERT INTO " + t + " (id, data, expires_at, user_id) VALUES (" +
			p(1) + ", " + p(2) + ", " + p(3) + ", " + p(4) + ")" +
			" ON DUPLICATE KEY UPDATE data = VALUES(data), expires_at = VALUES(expires_at), user_id = VALUES(user_id)"
		s.qIndex = "CREATE INDEX " + t + "_user_id ON " + t + " (user_id)"
	}

	s.qRekey = "UPDATE " + t + " SET id = " + p(1) + " WHERE id = " + p(2)
	s.qDelete = "DELETE FROM " + t + " WHERE id = " + p(1) + " OR id = " + p(2)
	s.qPrune = "DELETE FROM " + t + " WHERE expires_at <= " + p(1)
	s.qRevoke = "DELETE FROM " + t + " WHERE user_id = " + p(1) + " AND id <> " + p(2)
	s.qHasUser = "SELECT user_id FROM " + t + " WHERE 1 = 0"
	s.qAddUser = "ALTER TABLE " + t + " ADD COLUMN user_id VARCHAR(255)"

	return s, nil
}

// CreateTable creates the sessions table if it does not exist, and adds
// the user_id column and its index to tables created by older versions.
// It is idempotent and safe to run from several servers at boot.
func (s *SQLStore) CreateTable(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, s.qCreate); err != nil {
		return err
	}

	if err := s.addUserColumn(ctx); err != nil {
		return fmt.Errorf("session: add user_id column: %w", err)
	}

	if err := s.createUserIndex(ctx); err != nil {
		return fmt.Errorf("session: index user_id: %w", err)
	}

	return nil
}

// hasUserColumn reports whether selecting user_id works: the query
// matches no row, so a missing column is the only error expected.
func (s *SQLStore) hasUserColumn(ctx context.Context) bool {
	err := s.db.QueryRowContext(ctx, s.qHasUser).Scan(new(sql.NullString))

	return err == nil || errors.Is(err, sql.ErrNoRows)
}

func (s *SQLStore) addUserColumn(ctx context.Context) error {
	if s.hasUserColumn(ctx) {
		return nil
	}

	_, err := s.db.ExecContext(ctx, s.qAddUser)
	if err != nil && s.hasUserColumn(ctx) {
		return nil // another server added it first
	}

	return err
}

// createUserIndex indexes user_id for RevokeOthers. MySQL has no CREATE
// INDEX IF NOT EXISTS: it checks first and tolerates losing a boot race
// (error 1061, duplicate key name).
func (s *SQLStore) createUserIndex(ctx context.Context) error {
	if !s.mysql {
		_, err := s.db.ExecContext(ctx, s.qIndex)
		return err
	}

	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM information_schema.statistics
		WHERE table_schema = DATABASE() AND table_name = ? AND index_name = ?`, s.table, s.table+"_user_id").
		Scan(&n); err != nil || n > 0 {
		return err
	}

	_, err := s.db.ExecContext(ctx, s.qIndex)
	if err != nil && strings.Contains(err.Error(), "Error 1061") {
		return nil
	}

	return err
}

// digestRe matches a stored key; such a value is never accepted as a
// legacy clear id, so a leaked key cannot be replayed as one.
var digestRe = regexp.MustCompile(`^[0-9a-f]{64}$`)

// storageKey is the id column value for the session id.
func storageKey(id string) string {
	sum := sha256.Sum256([]byte(id))
	return hex.EncodeToString(sum[:])
}

// legacyKey is the clear id a pre-hashing row may still use, or "" when id
// cannot be one.
func legacyKey(id string) string {
	if id == "" || digestRe.MatchString(id) {
		return ""
	}

	return id
}

// Load implements Store.
func (s *SQLStore) Load(ctx context.Context, id string) ([]byte, bool, error) {
	key := storageKey(id)

	data, found, err := s.load(ctx, key)
	if err != nil || found {
		return data, found, err
	}

	legacy := legacyKey(id)
	if legacy == "" {
		return nil, false, nil
	}

	data, found, err = s.load(ctx, legacy)
	if err != nil || !found {
		return nil, false, err
	}

	// Re-key the row so the clear id leaves the table. A concurrent load
	// that already re-keyed it matches no row, which is fine.
	//nolint:gosec // G701: query built once from a validated table name; values are bound parameters
	if _, err := s.db.ExecContext(ctx, s.qRekey, key, legacy); err != nil {
		return nil, false, fmt.Errorf("session: rekey: %w", err)
	}

	return data, true, nil
}

func (s *SQLStore) load(ctx context.Context, key string) ([]byte, bool, error) {
	var data string

	//nolint:gosec // G701: query built once from a validated table name; values are bound parameters
	err := s.db.QueryRowContext(ctx, s.qLoad, key, s.now().Unix()).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}

	if err != nil {
		return nil, false, fmt.Errorf("session: load: %w", err)
	}

	return []byte(data), true, nil
}

// Save implements Store (upsert); the recorded user is kept.
func (s *SQLStore) Save(ctx context.Context, id string, data []byte, expiry time.Time) error {
	if _, err := s.db.ExecContext(ctx, s.qSave, storageKey(id), string(data), expiry.Unix()); err != nil {
		return fmt.Errorf("session: save: %w", err)
	}

	return nil
}

// SaveUser implements UserStore (upsert).
func (s *SQLStore) SaveUser(ctx context.Context, id, userID string, data []byte, expiry time.Time) error {
	user := sql.NullString{String: userID, Valid: userID != ""}

	if _, err := s.db.ExecContext(ctx, s.qSaveUser, storageKey(id), string(data), expiry.Unix(), user); err != nil {
		return fmt.Errorf("session: save: %w", err)
	}

	return nil
}

// Delete implements Store, also removing a not yet re-keyed legacy row.
func (s *SQLStore) Delete(ctx context.Context, id string) error {
	key := storageKey(id)

	legacy := legacyKey(id)
	if legacy == "" {
		legacy = key
	}

	if _, err := s.db.ExecContext(ctx, s.qDelete, key, legacy); err != nil {
		return fmt.Errorf("session: delete: %w", err)
	}

	return nil
}

// RevokeOthers implements UserStore. Sessions saved before user_id existed
// carry no user until their next save and are not matched.
func (s *SQLStore) RevokeOthers(ctx context.Context, userID, keepID string) error {
	if userID == "" {
		return errNoUser
	}

	// With keepID "" the kept key is the digest of "", which no session has.
	if _, err := s.db.ExecContext(ctx, s.qRevoke, userID, storageKey(keepID)); err != nil {
		return fmt.Errorf("session: revoke: %w", err)
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
