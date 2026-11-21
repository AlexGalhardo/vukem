package telegram

import (
	"context"
	"maps"
	"sync"
	"time"
)

// State is the state of one conversation: the step it is at and the values
// collected so far.
type State struct {
	Step string
	Data map[string]string
}

// Store keeps conversation state between updates. Implement it over the app
// database or Redis for state that survives a restart or is shared by several
// instances; MemoryStore is the in-process one.
type Store interface {
	// Load returns the state under key; ok is false when there is none or it
	// expired.
	Load(ctx context.Context, key string) (s State, ok bool, err error)
	Save(ctx context.Context, key string, s State) error
	Delete(ctx context.Context, key string) error
}

// MemoryStore is an in-memory Store whose entries expire after TTL.
type MemoryStore struct {
	// TTL is how long a saved state lives (default 24 hours).
	TTL time.Duration
	// Now is the clock (default time.Now).
	Now func() time.Time

	mu sync.Mutex
	m  map[string]memEntry
}

type memEntry struct {
	s       State
	expires time.Time
}

// NewMemoryStore returns an empty MemoryStore.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{TTL: 24 * time.Hour, Now: time.Now, m: map[string]memEntry{}}
}

// Load implements Store.
func (s *MemoryStore) Load(_ context.Context, key string) (State, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	e, ok := s.m[key]
	if !ok {
		return State{}, false, nil
	}

	if !s.Now().Before(e.expires) {
		delete(s.m, key)

		return State{}, false, nil
	}

	return State{Step: e.s.Step, Data: maps.Clone(e.s.Data)}, true, nil
}

// Save implements Store.
func (s *MemoryStore) Save(_ context.Context, key string, st State) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.m[key] = memEntry{s: State{Step: st.Step, Data: maps.Clone(st.Data)}, expires: s.Now().Add(s.TTL)}

	return nil
}

// Delete implements Store.
func (s *MemoryStore) Delete(_ context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.m, key)

	return nil
}
