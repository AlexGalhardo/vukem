// Package session provides server-side and cookie sessions with flash
// messages, old input, regeneration (anti-fixation) and idle/absolute
// timeouts. Sessions are loaded lazily and written only when modified.
//
// A [Manager] built by [New] wraps handlers with [Manager.Middleware];
// handlers reach the [Session] through web.Ctx.Session. Data lives in an
// AES-GCM encrypted cookie by default, or server-side in a [Store]:
// [MemoryStore], [SQLStore] or drivers/redis. Apps get a manager from
// app.Sessions(), configured by SESSION_* variables.
//
// See https://github.com/AlexGalhardo/vukem/blob/main/docs/http.md.
package session

import (
	"bytes"
	"encoding/json"
	"slices"
	"strings"
	"sync"
	"time"
)

const (
	keyCreated   = "_created"
	keyLast      = "_last"
	keyFlashNew  = "_flash.new"
	keyFlashOld  = "_flash.old"
	internalPref = "_"
)

// Session is one user's session. It is safe for concurrent use within a
// request.
type Session struct {
	mu   sync.Mutex
	id   string
	data map[string]any

	loaded      bool
	load        func(*Session)
	dirty       bool
	regenerated bool
	oldID       string
	storedID    string // id the session was loaded under (server-side stores)
	destroyed   bool
}

func newSession(id string, data map[string]any) *Session {
	if data == nil {
		data = map[string]any{}
	}

	return &Session{id: id, data: data, loaded: true}
}

func (s *Session) ensure() {
	if !s.loaded {
		s.loaded = true
		if s.load != nil {
			s.load(s)
		}
	}
}

// ID returns the session id ("" until the session is saved for cookie
// sessions).
func (s *Session) ID() string {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.ensure()

	return s.id
}

// Get returns a value or nil.
func (s *Session) Get(key string) any {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.ensure()

	return s.data[key]
}

// GetString returns a string value or "".
func (s *Session) GetString(key string) string {
	v, _ := s.Get(key).(string)
	return v
}

// GetInt64 returns an integer value stored by Put (JSON numbers included).
func (s *Session) GetInt64(key string) (int64, bool) {
	switch v := s.Get(key).(type) {
	case int64:
		return v, true
	case int:
		return int64(v), true
	case json.Number:
		n, err := v.Int64()
		return n, err == nil
	case float64:
		return int64(v), v == float64(int64(v))
	default:
		return 0, false
	}
}

// Has reports whether key is set.
func (s *Session) Has(key string) bool { return s.Get(key) != nil }

// Put stores a JSON-compatible value.
func (s *Session) Put(key string, value any) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.ensure()
	s.data[key] = value
	s.dirty = true
}

// Forget removes keys.
func (s *Session) Forget(keys ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.ensure()

	for _, k := range keys {
		delete(s.data, k)
	}

	s.dirty = true
}

// Pull returns and removes a value.
func (s *Session) Pull(key string) any {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.ensure()

	v := s.data[key]
	if v != nil {
		delete(s.data, key)
		s.dirty = true
	}

	return v
}

// Flash stores a value for the next request only.
func (s *Session) Flash(key string, value any) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.ensure()
	s.data[key] = value
	s.data[keyFlashNew] = appendUnique(stringList(s.data[keyFlashNew]), key)
	s.dirty = true
}

// Reflash keeps the current flash data for one more request.
func (s *Session) Reflash() {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.ensure()

	newKeys := stringList(s.data[keyFlashNew])
	for _, k := range stringList(s.data[keyFlashOld]) {
		newKeys = appendUnique(newKeys, k)
	}

	s.data[keyFlashNew] = newKeys
	delete(s.data, keyFlashOld)
	s.dirty = true
}

// All returns a copy of the user data (internal keys excluded).
func (s *Session) All() map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.ensure()

	out := map[string]any{}
	for k, v := range s.data {
		if strings.HasPrefix(k, internalPref) && k != "_old" && k != "_errors" && k != "_token" {
			continue
		}

		out[k] = v
	}

	return out
}

// Regenerate issues a new id and keeps the data. Call it after login and
// any privilege change (session fixation, A07).
func (s *Session) Regenerate() {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.ensure()

	if !s.regenerated {
		s.oldID = s.id
	}

	s.id = newID()
	s.regenerated = true
	s.dirty = true
}

// Invalidate clears all data and issues a new id (logout).
func (s *Session) Invalidate() {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.ensure()

	if !s.regenerated {
		s.oldID = s.id
	}

	s.id = newID()
	s.data = map[string]any{}
	s.regenerated = true
	s.destroyed = true
	s.dirty = true
}

// ageFlash drops the flash data of the previous request and promotes this
// request's flash keys; it runs right before saving.
func (s *Session) ageFlash() {
	for _, k := range stringList(s.data[keyFlashOld]) {
		if !slices.Contains(stringList(s.data[keyFlashNew]), k) {
			delete(s.data, k)
		}
	}

	if newKeys := stringList(s.data[keyFlashNew]); len(newKeys) > 0 {
		s.data[keyFlashOld] = newKeys
	} else {
		delete(s.data, keyFlashOld)
	}

	delete(s.data, keyFlashNew)
}

// hasFlash reports whether the session carries flash data that must be aged
// even if nothing else changed.
func (s *Session) hasFlash() bool {
	_, ok := s.data[keyFlashOld]
	return ok
}

func stringList(v any) []string {
	switch l := v.(type) {
	case []string:
		return slices.Clone(l)
	case []any:
		out := make([]string, 0, len(l))
		for _, x := range l {
			if str, ok := x.(string); ok {
				out = append(out, str)
			}
		}

		return out
	default:
		return nil
	}
}

func appendUnique(list []string, v string) []string {
	if slices.Contains(list, v) {
		return list
	}

	return append(list, v)
}

// encode/decode serialise session data as JSON; numbers decode as
// json.Number to keep integers exact.
func encode(data map[string]any) ([]byte, error) { return json.Marshal(data) }

func decode(b []byte) (map[string]any, error) {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()

	var data map[string]any
	if err := dec.Decode(&data); err != nil {
		return nil, err
	}

	if data == nil {
		data = map[string]any{}
	}

	return data, nil
}

func unixOf(v any) (time.Time, bool) {
	var n int64

	switch x := v.(type) {
	case json.Number:
		var err error
		if n, err = x.Int64(); err != nil {
			return time.Time{}, false
		}
	case int64:
		n = x
	case float64:
		n = int64(x)
	default:
		return time.Time{}, false
	}

	return time.Unix(n, 0), true
}
