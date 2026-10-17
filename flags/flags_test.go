package flags_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"maps"
	"strings"
	"sync"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/AlexGalhardo/vukem/cache"
	"github.com/AlexGalhardo/vukem/config"
	"github.com/AlexGalhardo/vukem/flags"
)

var (
	checkout = flags.Bool("new_checkout", false).Describe("Serve the new checkout")
	adminMFA = flags.Bool("admin_two_factor", true).Lock()
	limit    = flags.Int("upload_limit_mb", 10)
	variant  = flags.String("home_variant", "classic")
)

var errDown = errors.New("store down")

// memStore is an in-memory Store that counts loads and can fail.
type memStore struct {
	mu    sync.Mutex
	data  map[string]map[string]string
	loads int
	fail  bool
}

func newMemStore() *memStore { return &memStore{data: map[string]map[string]string{}} }

func (s *memStore) Load(_ context.Context, scope string) (map[string]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.loads++
	if s.fail {
		return nil, errDown
	}

	return maps.Clone(s.data[scope]), nil
}

func (s *memStore) Save(_ context.Context, scope, name, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.fail {
		return errDown
	}

	if s.data[scope] == nil {
		s.data[scope] = map[string]string{}
	}

	s.data[scope][name] = value

	return nil
}

func (s *memStore) Delete(_ context.Context, scope, name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.fail {
		return errDown
	}

	delete(s.data[scope], name)

	return nil
}

func (s *memStore) put(scope, name, value string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.data[scope] == nil {
		s.data[scope] = map[string]string{}
	}

	s.data[scope][name] = value
}

func (s *memStore) breakDown() { s.mu.Lock(); s.fail = true; s.mu.Unlock() }

func (s *memStore) loadCount() int { s.mu.Lock(); defer s.mu.Unlock(); return s.loads }

func all() []flags.AnyFlag { return []flags.AnyFlag{checkout, adminMFA, limit, variant} }

func newFlags(t *testing.T, cfg flags.Config) *flags.Flags {
	t.Helper()

	if cfg.Flags == nil {
		cfg.Flags = all()
	}

	f, err := flags.New(cfg)
	if err != nil {
		t.Fatalf("New() = %v", err)
	}

	return f
}

func TestNewRejectsMisconfiguration(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		flags []flags.AnyFlag
		env   map[string]string
		want  string
	}{
		{"invalid name", []flags.AnyFlag{flags.Bool("New-Checkout", false)}, nil, "invalid name"},
		{"empty name", []flags.AnyFlag{flags.Bool("", false)}, nil, "invalid name"},
		{"duplicate", []flags.AnyFlag{checkout, flags.Bool("new_checkout", true)}, nil, "duplicate"},
		{"string default too long", []flags.AnyFlag{flags.String("long", strings.Repeat("x", 2000))}, nil, "default"},
		{"bad bool env", all(), map[string]string{"FLAG_NEW_CHECKOUT": "maybe"}, "FLAG_NEW_CHECKOUT"},
		{"bad int env", all(), map[string]string{"FLAG_UPLOAD_LIMIT_MB": "ten"}, "FLAG_UPLOAD_LIMIT_MB"},
		{"unknown env key", all(), map[string]string{"FLAG_NEW_CHEKOUT": "true"}, "names no registered flag"},
		{"lowercase env key", all(), map[string]string{"FLAG_new_checkout": "true"}, "names no registered flag"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := flags.New(flags.Config{Flags: tt.flags, Env: config.MapSource(tt.env)})
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("New() = %v, want an error containing %q", err, tt.want)
			}
		})
	}
}

func TestResolutionOrder(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	store := newMemStore()
	f := newFlags(t, flags.Config{
		Store: store,
		Env:   config.MapSource(map[string]string{"FLAG_UPLOAD_LIMIT_MB": "20", "FLAG_HOME_VARIANT": "bold"}),
	})

	if f.Enabled(ctx, checkout) || flags.Get(ctx, f, limit) != 20 || flags.Get(ctx, f, variant) != "bold" {
		t.Fatalf("defaults = %v %d %q, want false 20 bold",
			f.Enabled(ctx, checkout), flags.Get(ctx, f, limit), flags.Get(ctx, f, variant))
	}

	store.put(flags.Global, "new_checkout", "true")
	store.put(flags.Global, "upload_limit_mb", "30")
	store.put("user:7", "upload_limit_mb", "50")

	if !f.Enabled(ctx, checkout) || flags.Get(ctx, f, limit) != 30 {
		t.Errorf("global = %v %d, want true 30", f.Enabled(ctx, checkout), flags.Get(ctx, f, limit))
	}

	user := flags.WithScope(ctx, "user:7")
	if got := flags.Get(user, f, limit); got != 50 {
		t.Errorf("Get(user:7) = %d, want the scoped 50", got)
	}

	if !f.Enabled(user, checkout) {
		t.Error("a scope without its own value lost the global one")
	}

	if got := flags.Get(flags.WithScope(ctx, "user:8"), f, limit); got != 30 {
		t.Errorf("Get(user:8) = %d, want the global 30", got)
	}
}

func TestStoredValueOfWrongKindIgnored(t *testing.T) {
	t.Parallel()

	store := newMemStore()
	store.put(flags.Global, "upload_limit_mb", "lots")

	f := newFlags(t, flags.Config{Store: store})
	if got := flags.Get(t.Context(), f, limit); got != 10 {
		t.Errorf("Get() with a stored non-integer = %d, want the default 10", got)
	}
}

func TestUnregisteredFlagReturnsCodeDefault(t *testing.T) {
	t.Parallel()

	var logs bytes.Buffer

	store := newMemStore()
	store.put(flags.Global, "ghost", "true")

	f := newFlags(t, flags.Config{Store: store, Log: slog.New(slog.NewTextHandler(&logs, nil))})

	if f.Enabled(t.Context(), flags.Bool("ghost", false)) {
		t.Error("an unregistered flag read its stored value")
	}

	// Same name, other kind: still not the registered flag.
	if got := flags.Get(t.Context(), f, flags.String("new_checkout", "x")); got != "x" {
		t.Errorf("Get() of a flag registered with another kind = %q, want its code default", got)
	}

	if !strings.Contains(logs.String(), "not registered") {
		t.Errorf("logs = %q, want an error about the unregistered flag", logs.String())
	}
}

func TestSetPutReset(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	store := newMemStore()
	f := newFlags(t, flags.Config{Store: store})

	ch, err := f.Set(ctx, flags.Global, "new_checkout", "1")
	if err != nil {
		t.Fatal(err)
	}

	if diff := cmp.Diff(flags.Change{Flag: "new_checkout", Old: "false", New: "true"}, ch); diff != "" {
		t.Errorf("Set() change (-want +got):\n%s", diff)
	}

	if ch, err = flags.Put(ctx, f, "tenant:1", limit, 99); err != nil ||
		ch != (flags.Change{Flag: "upload_limit_mb", Scope: "tenant:1", Old: "10", New: "99"}) {
		t.Errorf("Put() = %+v, %v", ch, err)
	}

	if got := flags.Get(flags.WithScope(ctx, "tenant:1"), f, limit); got != 99 {
		t.Errorf("Get(tenant:1) = %d, want 99", got)
	}

	if ch, err = f.Reset(ctx, flags.Global, "new_checkout"); err != nil || ch.Old != "true" || ch.New != "false" {
		t.Errorf("Reset() = %+v, %v", ch, err)
	}

	for _, tt := range []struct {
		name, flag, value string
		want              error
	}{
		{"unknown", "ghost", "true", flags.ErrUnknown},
		{"not a bool", "new_checkout", "yes please", flags.ErrInvalid},
		{"not an int", "upload_limit_mb", "1.5", flags.ErrInvalid},
		{"too long", "home_variant", strings.Repeat("x", 1025), flags.ErrInvalid},
	} {
		if _, err := f.Set(ctx, flags.Global, tt.flag, tt.value); !errors.Is(err, tt.want) {
			t.Errorf("Set(%s) = %v, want %v", tt.name, err, tt.want)
		}
	}

	if _, err := f.Set(ctx, strings.Repeat("s", 192), "new_checkout", "true"); !errors.Is(err, flags.ErrInvalid) {
		t.Errorf("Set() with a long scope = %v, want ErrInvalid", err)
	}

	noStore := newFlags(t, flags.Config{})
	if _, err := noStore.Set(ctx, flags.Global, "new_checkout", "true"); !errors.Is(err, flags.ErrNoStore) {
		t.Errorf("Set() without a store = %v, want ErrNoStore", err)
	}

	store.breakDown()

	if _, err := f.Set(ctx, flags.Global, "new_checkout", "true"); !errors.Is(err, errDown) {
		t.Errorf("Set() with the store down = %v, want its error", err)
	}
}

func TestCacheAndInvalidation(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	store := newMemStore()
	c := cache.New(cache.NewMemory(100), "test:")
	a := newFlags(t, flags.Config{Store: store, Cache: c})
	b := newFlags(t, flags.Config{Store: store, Cache: c}) // another instance sharing the cache

	for range 5 {
		a.Enabled(ctx, checkout)
		b.Enabled(ctx, checkout)
	}

	if n := store.loadCount(); n != 1 {
		t.Fatalf("store loads after 10 cached reads = %d, want 1", n)
	}

	if _, err := a.Set(ctx, flags.Global, "new_checkout", "true"); err != nil {
		t.Fatal(err)
	}

	if !b.Enabled(ctx, checkout) {
		t.Error("another instance sharing the cache did not see the write")
	}
}

func TestStates(t *testing.T) {
	t.Parallel()

	store := newMemStore()
	store.put(flags.Global, "new_checkout", "true")
	store.put(flags.Global, "admin_two_factor", "false")

	f := newFlags(t, flags.Config{Store: store, Env: config.MapSource(map[string]string{"FLAG_HOME_VARIANT": "bold"})})

	got, err := f.States(t.Context(), flags.Global)
	if err != nil {
		t.Fatal(err)
	}

	want := []flags.State{
		{
			Name:        "new_checkout",
			Description: "Serve the new checkout",
			Kind:        flags.KindBool,
			Default:     "false",
			Value:       "true",
			Stored:      true,
		},
		{Name: "admin_two_factor", Kind: flags.KindBool, Locked: true, Default: "true", Value: "true"},
		{Name: "upload_limit_mb", Kind: flags.KindInt, Default: "10", Value: "10"},
		{Name: "home_variant", Kind: flags.KindString, Default: "bold", Value: "bold"},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("States() (-want +got):\n%s", diff)
	}

	store.breakDown()

	if _, err := f.States(t.Context(), flags.Global); !errors.Is(err, errDown) {
		t.Errorf("States() with the store down = %v, want its error", err)
	}
}

func TestFlagAccessors(t *testing.T) {
	t.Parallel()

	if checkout.Name() != "new_checkout" || checkout.Default() || checkout.Locked() ||
		checkout.Description() != "Serve the new checkout" || checkout.Kind() != flags.KindBool {
		t.Errorf("checkout = %+v", checkout)
	}

	if !adminMFA.Locked() || limit.Kind() != flags.KindInt || variant.Kind() != flags.KindString {
		t.Error("Lock or Kind wrong")
	}

	f := newFlags(t, flags.Config{})

	var names []string
	for _, af := range f.All() {
		names = append(names, af.Name())
	}

	if diff := cmp.Diff(
		[]string{"new_checkout", "admin_two_factor", "upload_limit_mb", "home_variant"},
		names,
	); diff != "" {
		t.Errorf("All() (-want +got):\n%s", diff)
	}
}
