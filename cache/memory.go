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
// (least recently used entries are evicted).
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

func (m *Memory) set(key string, value []byte, ttl time.Duration) {
	var exp time.Time
	if ttl > 0 {
		exp = m.now().Add(ttl)
	}

	if el, ok := m.items[key]; ok {
		e, _ := el.Value.(*entry)
		e.value, e.expires = value, exp
		m.ll.MoveToFront(el)

		return
	}

	m.items[key] = m.ll.PushFront(&entry{key: key, value: value, expires: exp})

	for m.ll.Len() > m.max {
		last := m.ll.Back()
		e, _ := last.Value.(*entry)
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

	m.set(key, bytes.Clone(value), ttl)

	return nil
}

// Add implements Store.
func (m *Memory) Add(_ context.Context, key string, value []byte, ttl time.Duration) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.live(key); ok {
		return false, nil
	}

	m.set(key, bytes.Clone(value), ttl)

	return true, nil
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

	m.set(key, []byte(strconv.FormatInt(delta, 10)), ttl)

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
