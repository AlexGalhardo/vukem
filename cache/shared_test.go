package cache_test

import (
	"fmt"
	"testing"

	"github.com/google/go-cmp/cmp"

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

// selfWrapper unwraps to itself, a broken decorator that would loop.
type selfWrapper struct{ cache.Store }

func (s *selfWrapper) Unwrap() cache.Store { return s }

// deep wraps st n times with Unwrap.
func deep(st cache.Store, n int) cache.Store {
	for range n {
		st = unwrapper{st}
	}

	return st
}

// Unwrap chains are followed for a bounded number of hops: a store that
// unwraps to itself, or one buried deeper than the limit, is not shared.
func TestSharedBoundsUnwrap(t *testing.T) {
	t.Parallel()

	_, db := openSQLiteStore(t)

	for name, tt := range map[string]struct {
		store cache.Store
		want  bool
	}{
		"self reference": {store: &selfWrapper{Store: db}, want: false},
		"31 hops":        {store: deep(db, 31), want: true},
		"40 hops":        {store: deep(db, 40), want: false},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if got := cache.Shared(tt.store); got != tt.want {
				t.Errorf("Shared(%s) = %v, want %v", name, got, tt.want)
			}
		})
	}
}

func TestLayers(t *testing.T) {
	t.Parallel()

	mem := cache.NewMemory(0)

	var got []string
	for st := range cache.Layers(unwrapper{opaque{mem}}) {
		got = append(got, fmt.Sprintf("%T", st))
	}

	want := []string{"cache_test.unwrapper", "cache_test.opaque"}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("Layers() (-want +got):\n%s", diff)
	}

	n := 0
	for range cache.Layers(&selfWrapper{Store: mem}) {
		n++
	}

	if n != cache.MaxUnwrap {
		t.Errorf("Layers(self reference) yielded %d stores, want %d", n, cache.MaxUnwrap)
	}
}
