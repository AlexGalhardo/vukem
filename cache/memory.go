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
// [Cache.NewLock] directly on a Memory store is never evicted before it
// expires or is released, so flooding the cache cannot hand a held lock to
// another owner. Held locks may fill at most half of MaxEntries: past that,
// acquiring a new lock fails (fails closed) instead of evicting a held one.
// Other entries, counters included, are evicted as usual.
type Memory struct {
	max int
	now func() time.Time

	mu    sync.Mutex
	ll    *list.List
	items map[string]*list.Element
	locks int // entries with lock set, live or not yet purged
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
		m.remove(el)

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
		switch {
		case lock && !e.lock:
			m.locks++
		case !lock && e.lock:
			m.locks--
		}

		e.value, e.expires, e.lock = value, exp, lock
		m.ll.MoveToFront(el)

		return
	}

	m.items[key] = m.ll.PushFront(&entry{key: key, value: value, expires: exp, lock: lock})
	if lock {
		m.locks++
	}

	now := m.now()

	// Held locks are skipped. addLock keeps them to at most half of max, so
	// whenever the store is over max a non-lock entry is left to evict; the
	// skipped bound only guards the loop.
	for skipped := 0; m.ll.Len() > m.max && skipped < m.ll.Len(); {
		last := m.ll.Back()
		e, _ := last.Value.(*entry)

		if e.lock && (e.expires.IsZero() || now.Before(e.expires)) {
			m.ll.MoveToFront(last)
			skipped++

			continue
		}

		m.remove(last)
	}
}

// remove drops el from the list and the index, keeping the lock count.
func (m *Memory) remove(el *list.Element) {
	e, _ := el.Value.(*entry)
	if e.lock {
		m.locks--
	}

	m.ll.Remove(el)
	delete(m.items, e.key)
}

// lockBudget is the number of held locks the store accepts.
func (m *Memory) lockBudget() int { return max(1, m.max/2) }

// purgeExpiredLocks drops expired lock entries so they stop counting
// against the budget. It walks the whole list, so it runs only when the
// budget looks full.
func (m *Memory) purgeExpiredLocks() {
	now := m.now()

	for el := m.ll.Front(); el != nil; {
		next := el.Next()
		if e, _ := el.Value.(*entry); e.lock && !e.expires.IsZero() && !now.Before(e.expires) {
			m.remove(el)
		}

		el = next
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
// It reports false, as for a lock held by someone else, when held locks
// already fill the budget.
func (m *Memory) addLock(key string, value []byte, ttl time.Duration) bool {
	return m.add(key, value, ttl, true)
}

func (m *Memory) add(key string, value []byte, ttl time.Duration, lock bool) bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.live(key); ok {
		return false
	}

	if lock && m.locks >= m.lockBudget() {
		m.purgeExpiredLocks()

		if m.locks >= m.lockBudget() {
			return false
		}
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
			m.remove(el)
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

	m.remove(m.items[key])

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
	m.locks = 0

	return nil
}

// Len returns the number of stored entries (expired ones included until
// they are touched).
func (m *Memory) Len() int {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.ll.Len()
}
