package cache_test

import (
	"testing"

	"github.com/AlexGalhardo/vukem/cache"
)

// declared is a store that states whether it is shared.
type declared struct {
	cache.Store
	shared bool
}

func (d declared) Shared() bool { return d.shared }

// unwrapper decorates a store (metrics, tracing) and exposes it with Unwrap.
type unwrapper struct{ cache.Store }

func (u unwrapper) Unwrap() cache.Store { return u.Store }

// opaque decorates a store and says nothing about it.
type opaque struct{ cache.Store }

func TestShared(t *testing.T) {
	t.Parallel()

	_, db := openSQLiteStore(t)
	mem := cache.NewMemory(0)

	for name, tt := range map[string]struct {
		store cache.Store
		want  bool
	}{
		"nil":                      {store: nil, want: false},
		"memory":                   {store: mem, want: false},
		"database":                 {store: db, want: true},
		"declared shared":          {store: declared{Store: mem, shared: true}, want: true},
		"declared local":           {store: declared{Store: db, shared: false}, want: false},
		"unwrap memory":            {store: unwrapper{mem}, want: false},
		"unwrap database":          {store: unwrapper{db}, want: true},
		"unwrap twice database":    {store: unwrapper{unwrapper{db}}, want: true},
		"unwrap declared shared":   {store: unwrapper{declared{Store: mem, shared: true}}, want: true},
		"opaque database":          {store: opaque{db}, want: false},
		"unwrap opaque database":   {store: unwrapper{opaque{db}}, want: false},
		"declaration wins unwrap":  {store: declaredUnwrapper{unwrapper{mem}}, want: true},
		"embedded memory promoted": {store: struct{ *cache.Memory }{mem}, want: false},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if got := cache.Shared(tt.store); got != tt.want {
				t.Errorf("Shared(%T) = %v, want %v", tt.store, got, tt.want)
			}
		})
	}
}

// declaredUnwrapper both forwards Unwrap and declares itself shared; the
// declaration is the answer.
type declaredUnwrapper struct{ unwrapper }

func (declaredUnwrapper) Shared() bool { return true }

// A store that neither declares Shared nor exposes Unwrap (a metrics
// wrapper around the memory cache) is not trusted to be shared: checks
// that need every server to see the same entries fail closed.
func TestSecurity_A02_UnknownCacheStoreIsNotShared(t *testing.T) {
	t.Parallel()

	_, db := openSQLiteStore(t)

	for name, st := range map[string]cache.Store{
		"wrapped memory":   opaque{cache.NewMemory(0)},
		"wrapped database": opaque{db},
	} {
		if cache.Shared(st) {
			t.Errorf("Shared(%s) = true, want false (undeclared stores fail closed)", name)
		}
	}
}
