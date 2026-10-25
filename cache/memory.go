package cache

import (
	"bytes"
	"container/list"
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"sync"
	"time"
)

// Memory is an in-process LRU store with TTLs. MaxEntries bounds memory
// (least recently used entries are evicted). A lock taken with
// [Cache.NewLock] with a TTL directly on a Memory store is not evicted
// before it expires or is released while held locks fill at most half of
// MaxEntries, so flooding the cache with other keys cannot hand a held lock
// to another owner; other entries, counters included, are evicted as usual.
type Memory struct {
	max int
	now func() time.Time

	mu    sync.Mutex
	ll    *list.List
	items map[string]*list.Element
}

type entry struct {
	key     string
	value   []byte
	expires time.Time // zero: never
	lock    bool      // held lock: skipped by eviction until it expires
}

// NewMemory returns a memory store holding at most maxEntries (0 = 10000).
func NewMemory(maxEntries int) *Memory {
	if maxEntries <= 0 {
		maxEntries = 10000
	}

	return &Memory{max: maxEntries, now: time.Now, ll: list.New(), items: map[string]*list.Element{}}
}

func (m *Memory) live(key string) (*entry, bool) {
	el, ok := m.items[key]
	if !ok {
		return nil, false
	}

	e, _ := el.Value.(*entry)
	if !e.expires.IsZero() && !m.now().Before(e.expires) {
		m.ll.Remove(el)
		delete(m.items, key)

		return nil, false
	}

	m.ll.MoveToFront(el)

	return e, true
}

func (m *Memory) set(key string, value []byte, ttl time.Duration, lock bool) {
	var exp time.Time
	if ttl > 0 {
		exp = m.now().Add(ttl)
	}

	if el, ok := m.items[key]; ok {
		e, _ := el.Value.(*entry)
		e.value, e.expires, e.lock = value, exp, lock
		m.ll.MoveToFront(el)

		return
	}

	m.items[key] = m.ll.PushFront(&entry{key: key, value: value, expires: exp, lock: lock})

	now := m.now()

	// At most max/2 held locks are skipped per call, so the store never
	// exceeds max and a flood of locks (names derived from input) cannot
	// grow it: past that budget, locks are evicted like any entry. Locks
	// without a TTL are never skipped, since they would never leave.
	for skipped := 0; m.ll.Len() > m.max; {
		last := m.ll.Back()
		e, _ := last.Value.(*entry)

		if e.lock && skipped < m.max/2 && !e.expires.IsZero() && now.Before(e.expires) {
			m.ll.MoveToFront(last)
			skipped++

			continue
		}

		m.ll.Remove(last)
		delete(m.items, e.key)
	}
}

// Get implements Store.
func (m *Memory) Get(_ context.Context, key string) ([]byte, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	e, ok := m.live(key)
	if !ok {
		return nil, false, nil
	}

	return bytes.Clone(e.value), true, nil
}

// Set implements Store.
func (m *Memory) Set(_ context.Context, key string, value []byte, ttl time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.set(key, bytes.Clone(value), ttl, false)

	return nil
}

// Add implements Store.
func (m *Memory) Add(_ context.Context, key string, value []byte, ttl time.Duration) (bool, error) {
	return m.add(key, value, ttl, false), nil
}

// addLock is Add for [Lock]: the entry survives eviction while it lives.
func (m *Memory) addLock(key string, value []byte, ttl time.Duration) bool {
	return m.add(key, value, ttl, true)
}

func (m *Memory) add(key string, value []byte, ttl time.Duration, lock bool) bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.live(key); ok {
		return false
	}

	m.set(key, bytes.Clone(value), ttl, lock)

	return true
}

// Delete implements Store.
func (m *Memory) Delete(_ context.Context, keys ...string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, k := range keys {
		if el, ok := m.items[k]; ok {
			m.ll.Remove(el)
			delete(m.items, k)
		}
	}

	return nil
}

// DeleteIf implements Store.
func (m *Memory) DeleteIf(_ context.Context, key string, value []byte) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	e, ok := m.live(key)
	if !ok || !bytes.Equal(e.value, value) {
		return false, nil
	}

	m.ll.Remove(m.items[key])
	delete(m.items, key)

	return true, nil
}

// Increment implements Store.
func (m *Memory) Increment(_ context.Context, key string, delta int64, ttl time.Duration) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var n int64

	e, ok := m.live(key)
	if ok {
		if err := json.Unmarshal(e.value, &n); err != nil {
			return 0, fmt.Errorf("cache: %s is not an integer", key)
		}

		n += delta
		e.value = []byte(strconv.FormatInt(n, 10))

		return n, nil
	}

	m.set(key, []byte(strconv.FormatInt(delta, 10)), ttl, false)

	return delta, nil
}

// Flush implements Store.
func (m *Memory) Flush(context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.ll.Init()
	m.items = map[string]*list.Element{}

	return nil
}

// Len returns the number of stored entries (expired ones included until
// they are touched).
func (m *Memory) Len() int {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.ll.Len()
}
