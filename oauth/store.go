package oauth

import (
	"context"
	"errors"
	"slices"
	"sync"
	"time"
)

// Store errors. A Store returns them as is (or wrapped).
var (
	// ErrNotFound: no such code, token or grant.
	ErrNotFound = errors.New("oauth: not found")
	// ErrExpired: the code or token is past its lifetime.
	ErrExpired = errors.New("oauth: expired")
	// ErrRevoked: the grant was revoked.
	ErrRevoked = errors.New("oauth: revoked")
	// ErrReused: the code or refresh token was already used. The store has
	// revoked the grant by the time it returns this.
	ErrReused = errors.New("oauth: reused")
)

// Grant is what the user consented to: one client, one resource, some scopes.
// Codes and refresh tokens belong to a grant, and revoking it ends them all.
type Grant struct {
	ID        string
	ClientID  string
	Subject   string
	Scopes    []string
	Resource  string
	CreatedAt time.Time
	Revoked   bool
}

// Code is an authorization code. Only the SHA-256 of the code is stored.
type Code struct {
	Hash        string
	GrantID     string
	ClientID    string
	RedirectURI string
	Challenge   string // PKCE S256 code challenge
	ExpiresAt   time.Time
}

// Refresh is a refresh token. Only its SHA-256 is stored.
type Refresh struct {
	Hash      string
	GrantID   string
	ExpiresAt time.Time
}

// Store persists grants, codes and refresh tokens. RedeemCode and
// RotateRefresh must be atomic: of two concurrent calls with the same hash
// exactly one succeeds, and the other gets ErrReused.
type Store interface {
	CreateGrant(ctx context.Context, g Grant) error
	// Grant returns the grant, with Revoked set when it was revoked.
	Grant(ctx context.Context, id string) (Grant, error)
	SaveCode(ctx context.Context, c Code) error
	// RedeemCode marks the code used. It returns ErrNotFound, ErrExpired, or
	// ErrReused (together with the Code, so the caller knows the grant; the
	// store has already revoked it).
	RedeemCode(ctx context.Context, hash string, now time.Time) (Code, error)
	SaveRefresh(ctx context.Context, r Refresh) error
	// RotateRefresh consumes oldHash and stores next in the same grant. It
	// returns ErrNotFound, ErrExpired, ErrRevoked, or ErrReused after
	// revoking the grant.
	RotateRefresh(ctx context.Context, oldHash string, next Refresh, now time.Time) (grantID string, err error)
	// GrantOfRefresh returns the grant of a refresh token (used or not).
	GrantOfRefresh(ctx context.Context, hash string) (string, error)
	RevokeGrant(ctx context.Context, id string) error
}

// MemoryStore is an in-memory Store for tests and single-process apps; it
// forgets everything on restart.
type MemoryStore struct {
	mu      sync.Mutex
	grants  map[string]Grant
	codes   map[string]*memCode
	refresh map[string]*memRefresh
}

type memCode struct {
	Code
	used bool
}

type memRefresh struct {
	Refresh
	used bool
}

// NewMemoryStore returns an empty MemoryStore.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{grants: map[string]Grant{}, codes: map[string]*memCode{}, refresh: map[string]*memRefresh{}}
}

// CreateGrant implements Store.
func (m *MemoryStore) CreateGrant(_ context.Context, g Grant) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	g.Scopes = slices.Clone(g.Scopes)
	m.grants[g.ID] = g

	return nil
}

// Grant implements Store.
func (m *MemoryStore) Grant(_ context.Context, id string) (Grant, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	g, ok := m.grants[id]
	if !ok {
		return Grant{}, ErrNotFound
	}

	g.Scopes = slices.Clone(g.Scopes)

	return g, nil
}

// SaveCode implements Store.
func (m *MemoryStore) SaveCode(_ context.Context, c Code) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.codes[c.Hash] = &memCode{Code: c}

	return nil
}

func (m *MemoryStore) revoke(id string) {
	if g, ok := m.grants[id]; ok {
		g.Revoked = true
		m.grants[id] = g
	}
}

// RedeemCode implements Store.
func (m *MemoryStore) RedeemCode(_ context.Context, hash string, now time.Time) (Code, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	c, ok := m.codes[hash]
	if !ok {
		return Code{}, ErrNotFound
	}

	if c.used {
		m.revoke(c.GrantID)

		return c.Code, ErrReused
	}

	c.used = true

	if !now.Before(c.ExpiresAt) {
		return Code{}, ErrExpired
	}

	return c.Code, nil
}

// SaveRefresh implements Store.
func (m *MemoryStore) SaveRefresh(_ context.Context, r Refresh) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.refresh[r.Hash] = &memRefresh{Refresh: r}

	return nil
}

// RotateRefresh implements Store.
func (m *MemoryStore) RotateRefresh(_ context.Context, oldHash string, next Refresh, now time.Time) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	r, ok := m.refresh[oldHash]
	if !ok {
		return "", ErrNotFound
	}

	if m.grants[r.GrantID].Revoked {
		return "", ErrRevoked
	}

	if r.used {
		m.revoke(r.GrantID)

		return "", ErrReused
	}

	if !now.Before(r.ExpiresAt) {
		return "", ErrExpired
	}

	r.used = true
	next.GrantID = r.GrantID
	m.refresh[next.Hash] = &memRefresh{Refresh: next}

	return r.GrantID, nil
}

// GrantOfRefresh implements Store.
func (m *MemoryStore) GrantOfRefresh(_ context.Context, hash string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	r, ok := m.refresh[hash]
	if !ok {
		return "", ErrNotFound
	}

	return r.GrantID, nil
}

// RevokeGrant implements Store.
func (m *MemoryStore) RevokeGrant(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.revoke(id)

	return nil
}
