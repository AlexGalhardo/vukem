package graphql

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/AlexGalhardo/vukem/cache"
)

// Hash returns the persisted-query id of a document: the lowercase hex
// SHA-256 of its exact text (Apollo APQ "sha256Hash").
func Hash(query string) string {
	sum := sha256.Sum256([]byte(query))
	return hex.EncodeToString(sum[:])
}

// QueryStore holds persisted documents by hash.
type QueryStore interface {
	Get(ctx context.Context, hash string) (query string, ok bool, err error)
	Put(ctx context.Context, hash, query string) error
}

// PersistedQueries configures persisted queries on a Handler.
//
// Without AllowlistOnly this is Automatic Persisted Queries: a client
// sends {"extensions":{"persistedQuery":{"version":1,"sha256Hash":…}}},
// gets PERSISTED_QUERY_NOT_FOUND once, then sends the full query with the
// hash, which is verified and stored.
//
// With AllowlistOnly the store is a fixed allowlist (build it with
// NewAllowlist from the documents your clients ship): only listed hashes
// or documents run, nothing is ever registered. Use it in production for
// first-party APIs.
type PersistedQueries struct {
	// Store defaults to NewMemoryStore(DefaultPersistedEntries).
	Store QueryStore
	// AllowlistOnly rejects every document that is not already stored.
	AllowlistOnly bool
}

// DefaultPersistedEntries bounds the default in-memory APQ store.
const DefaultPersistedEntries = 1000

// MemoryStore is a bounded in-memory QueryStore; the oldest entries are
// evicted first.
type MemoryStore struct {
	mu    sync.Mutex
	m     map[string]string
	order []string
	max   int
}

// NewMemoryStore returns a store holding at most max documents (0 means
// DefaultPersistedEntries).
func NewMemoryStore(maxEntries int) *MemoryStore {
	if maxEntries <= 0 {
		maxEntries = DefaultPersistedEntries
	}

	return &MemoryStore{m: map[string]string{}, max: maxEntries}
}

// NewAllowlist returns an unbounded store preloaded with documents, keyed
// by Hash, for PersistedQueries{AllowlistOnly: true}.
func NewAllowlist(queries ...string) *MemoryStore {
	s := &MemoryStore{m: make(map[string]string, len(queries)), max: -1}
	for _, q := range queries {
		s.m[Hash(q)] = q
	}

	return s
}

// Get implements QueryStore.
func (s *MemoryStore) Get(_ context.Context, hash string) (string, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	q, ok := s.m[hash]

	return q, ok, nil
}

// Put implements QueryStore.
func (s *MemoryStore) Put(_ context.Context, hash, query string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.m[hash]; ok {
		return nil
	}

	if s.max > 0 && len(s.order) >= s.max {
		delete(s.m, s.order[0])
		s.order = s.order[1:]
	}

	s.m[hash] = query
	s.order = append(s.order, hash)

	return nil
}

// CacheStore keeps persisted documents in a Vukem cache (Redis, database,
// memory), shared by every instance of the app.
type CacheStore struct {
	c   *cache.Cache
	ttl time.Duration
}

// NewCacheStore returns a store on c; ttl 0 keeps entries forever.
func NewCacheStore(c *cache.Cache, ttl time.Duration) *CacheStore {
	return &CacheStore{c: c, ttl: ttl}
}

func apqKey(hash string) string { return "graphql:apq:" + hash }

// Get implements QueryStore.
func (s *CacheStore) Get(ctx context.Context, hash string) (string, bool, error) {
	q, ok, err := cache.Get[string](ctx, s.c, apqKey(hash))
	if err != nil {
		return "", false, fmt.Errorf("graphql: persisted query store: %w", err)
	}

	return q, ok, nil
}

// Put implements QueryStore.
func (s *CacheStore) Put(ctx context.Context, hash, query string) error {
	if err := s.c.Put(ctx, apqKey(hash), query, s.ttl); err != nil {
		return fmt.Errorf("graphql: persisted query store: %w", err)
	}

	return nil
}

func requestError(code, msg string) *Error {
	return &Error{Message: msg, Extensions: map[string]any{"code": code}}
}

// resolve fills req.Query from the store or registers it. It fails
// closed: a store error rejects the request.
func (p *PersistedQueries) resolve(ctx context.Context, req *Request) *Error {
	hash, has, perr := persistedHash(req.Extensions)
	if perr != nil {
		return perr
	}

	if p == nil {
		if has {
			return requestError(CodePersistedNotSupported, "PersistedQueryNotSupported")
		}

		return nil
	}

	store := p.Store

	switch {
	case has && req.Query == "":
		q, ok, err := store.Get(ctx, hash)
		if err != nil {
			return requestError(CodeInternal, internalMessage)
		}

		if !ok {
			if p.AllowlistOnly {
				return requestError(CodePersistedRequired, "This persisted query is not in the allowlist.")
			}

			return requestError(CodePersistedNotFound, "PersistedQueryNotFound")
		}

		req.Query = q
	case has:
		if Hash(req.Query) != hash {
			return requestError(CodeBadRequest, "provided sha does not match query")
		}

		if p.AllowlistOnly {
			return p.allowed(ctx, hash)
		}

		if err := store.Put(ctx, hash, req.Query); err != nil {
			return requestError(CodeInternal, internalMessage)
		}
	case p.AllowlistOnly:
		return p.allowed(ctx, Hash(req.Query))
	}

	return nil
}

func (p *PersistedQueries) allowed(ctx context.Context, hash string) *Error {
	_, ok, err := p.Store.Get(ctx, hash)
	if err != nil {
		return requestError(CodeInternal, internalMessage)
	}

	if !ok {
		return requestError(CodePersistedRequired, "Only persisted queries from the allowlist are accepted.")
	}

	return nil
}

func persistedHash(ext map[string]any) (string, bool, *Error) {
	raw, ok := ext["persistedQuery"]
	if !ok {
		return "", false, nil
	}

	pq, ok := raw.(map[string]any)
	if !ok {
		return "", false, requestError(CodeBadRequest, "extensions.persistedQuery must be an object")
	}

	if v, _ := toInt(pq["version"]); v != 1 {
		return "", false, requestError(CodeBadRequest, "Unsupported persisted query version")
	}

	hash, _ := pq["sha256Hash"].(string)
	if len(hash) != sha256.Size*2 {
		return "", false, requestError(
			CodeBadRequest,
			"extensions.persistedQuery.sha256Hash must be a SHA-256 hex digest",
		)
	}

	if _, err := hex.DecodeString(hash); err != nil {
		return "", false, requestError(
			CodeBadRequest,
			"extensions.persistedQuery.sha256Hash must be a SHA-256 hex digest",
		)
	}

	return strings.ToLower(hash), true, nil
}
