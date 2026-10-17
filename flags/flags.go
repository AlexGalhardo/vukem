// Package flags provides feature flags: typed values declared in code with a
// default, overridden by FLAG_<NAME> configuration and by values stored in
// the database (globally or for one scope, such as a user or a tenant).
//
//	var NewCheckout = flags.Bool("new_checkout", false).Describe("Serve the new checkout")
//	var AdminTwoFactor = flags.Bool("admin_two_factor", true).Lock()
//
//	app, err := vukem.New(vukem.WithFlags(NewCheckout, AdminTwoFactor))
//	ff, err := app.Flags()
//	if ff.Enabled(ctx, NewCheckout) { ... }
//
// A value is resolved, lowest precedence first, from the code default, the
// FLAG_<NAME> variable, the stored global value and the stored value for
// the scope in the context ([WithScope]). Locked flags always have their
// code default. Reads fail closed: when the store or the cache errors, the
// default (the code default, or FLAG_<NAME> when set) is returned and a
// warning is logged; a read never fails a request.
//
// See https://github.com/AlexGalhardo/vukem/blob/main/docs/subsystems.md#feature-flags.
package flags

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/AlexGalhardo/vukem/cache"
	"github.com/AlexGalhardo/vukem/config"
)

// Value is the set of types a flag can hold.
type Value interface{ bool | int | string }

// Kind names the type of a flag's value.
type Kind string

// Flag kinds.
const (
	KindBool   Kind = "bool"
	KindInt    Kind = "int"
	KindString Kind = "string"
)

// Global is the scope of values that apply to everyone.
const Global = ""

// DefaultTTL is how long stored values stay cached when Config.TTL is zero.
// Writes forget the cached values, so a change shows at once on every
// instance sharing the cache; the TTL bounds staleness for instances that
// do not (a memory cache per process).
const DefaultTTL = 30 * time.Second

// EnvPrefix prefixes the configuration key that overrides a flag's code
// default: FLAG_ plus the upper-cased name (FLAG_NEW_CHECKOUT).
const EnvPrefix = "FLAG_"

const (
	// maxScope matches the scope column (191 keeps a MySQL utf8mb4 index
	// under 767 bytes).
	maxScope = 191
	maxValue = 1024
)

var nameRe = regexp.MustCompile(`^[a-z][a-z0-9_]{0,99}$`)

// Errors returned by Set, Put and Reset.
var (
	ErrUnknown = errors.New("flags: unknown flag")
	ErrLocked  = errors.New("flags: flag is locked")
	ErrInvalid = errors.New("flags: invalid value")
	ErrNoStore = errors.New("flags: no store configured")
)

// Flag is a typed feature flag. Declare it once (a package-level value is
// fine: it is immutable) and register it with vukem.WithFlags.
type Flag[T Value] struct {
	name   string
	def    T
	locked bool
	desc   string
}

// Bool declares a flag that is on or off.
func Bool(name string, def bool) Flag[bool] { return Flag[bool]{name: name, def: def} }

// Int declares a flag holding an integer (a limit, a percentage).
func Int(name string, def int) Flag[int] { return Flag[int]{name: name, def: def} }

// String declares a flag holding a short string (a variant name).
func String(name, def string) Flag[string] { return Flag[string]{name: name, def: def} }

// Lock returns a copy of f that always has its code default: FLAG_<NAME>
// is a boot error, stored values are ignored and writes fail with
// ErrLocked. Lock security-critical flags so nobody can switch them off
// from the admin panel or the environment.
func (f Flag[T]) Lock() Flag[T] { f.locked = true; return f }

// Describe returns a copy of f with a description shown in the admin panel.
func (f Flag[T]) Describe(text string) Flag[T] { f.desc = text; return f }

// Name returns the flag name.
func (f Flag[T]) Name() string { return f.name }

// Default returns the code default.
func (f Flag[T]) Default() T { return f.def }

// Locked reports whether the flag is locked.
func (f Flag[T]) Locked() bool { return f.locked }

// Description returns the description.
func (f Flag[T]) Description() string { return f.desc }

// Kind returns the kind of the flag's value.
func (f Flag[T]) Kind() Kind { return kindOf[T]() }

func (f Flag[T]) definition() definition {
	return definition{name: f.name, desc: f.desc, kind: f.Kind(), def: encode(f.def), locked: f.locked}
}

// AnyFlag is a Flag of any type, for registration and allowlists.
type AnyFlag interface {
	Name() string
	Description() string
	Locked() bool
	Kind() Kind
	definition() definition
}

type definition struct {
	name, desc string
	kind       Kind
	def        string // encoded code default
	locked     bool
}

func kindOf[T Value]() Kind {
	var zero T

	switch any(zero).(type) {
	case bool:
		return KindBool
	case int:
		return KindInt
	default:
		return KindString
	}
}

func encode[T Value](v T) string {
	switch x := any(v).(type) {
	case bool:
		return strconv.FormatBool(x)
	case int:
		return strconv.Itoa(x)
	case string:
		return x
	}

	return "" // unreachable: T is bool, int or string
}

func decode[T Value](s string) T {
	var out T

	switch p := any(&out).(type) {
	case *bool:
		*p, _ = strconv.ParseBool(s)
	case *int:
		*p, _ = strconv.Atoi(s)
	case *string:
		*p = s
	}

	return out
}

// normalize checks raw against kind and returns its canonical form.
func normalize(kind Kind, raw string) (string, error) {
	switch kind {
	case KindBool:
		b, err := strconv.ParseBool(strings.TrimSpace(raw))
		if err != nil {
			return "", fmt.Errorf("%w: %q is not a boolean", ErrInvalid, raw)
		}

		return strconv.FormatBool(b), nil
	case KindInt:
		n, err := strconv.Atoi(strings.TrimSpace(raw))
		if err != nil {
			return "", fmt.Errorf("%w: %q is not an integer", ErrInvalid, raw)
		}

		return strconv.Itoa(n), nil
	default:
		if len(raw) > maxValue {
			return "", fmt.Errorf("%w: longer than %d bytes", ErrInvalid, maxValue)
		}

		return raw, nil
	}
}

// Store keeps stored values by scope (Global is "").
type Store interface {
	// Load returns the stored values of scope by flag name.
	Load(ctx context.Context, scope string) (map[string]string, error)
	// Save stores the value of name in scope.
	Save(ctx context.Context, scope, name, value string) error
	// Delete removes the value of name in scope; a missing row is no error.
	Delete(ctx context.Context, scope, name string) error
}

// Config configures New.
type Config struct {
	// Flags are the registered flags; reading any other flag returns its
	// code default and logs an error.
	Flags []AnyFlag
	// Store keeps the stored values. Nil uses code defaults and
	// FLAG_<NAME> only, and writes fail with ErrNoStore.
	Store Store
	// Cache caches the stored values for TTL. Nil reads the store every
	// time.
	Cache *cache.Cache
	// TTL is how long stored values stay cached (DefaultTTL when zero).
	TTL time.Duration
	// Env resolves FLAG_<NAME> overrides (app.Source). When it also lists
	// its keys (config.Source does, for .env files), a FLAG_ key that
	// names no flag is an error.
	Env config.Lookuper
	// Log receives the warnings of failed reads (slog.Default when nil).
	Log *slog.Logger
}

// Flags resolves and changes feature flags. It is safe for concurrent use.
type Flags struct {
	defs     []definition
	byName   map[string]definition
	defaults map[string]string // code default, or FLAG_<NAME>
	store    Store
	cache    *cache.Cache
	ttl      time.Duration
	log      *slog.Logger
}

// New validates cfg: flag names (lowercase letters, digits and
// underscores, starting with a letter), duplicates and FLAG_<NAME> values.
// Misconfiguration is returned here, at boot.
func New(cfg Config) (*Flags, error) {
	f := &Flags{
		byName:   map[string]definition{},
		defaults: map[string]string{},
		store:    cfg.Store,
		cache:    cfg.Cache,
		ttl:      cfg.TTL,
		log:      cfg.Log,
	}

	if f.ttl == 0 {
		f.ttl = DefaultTTL
	}

	if f.log == nil {
		f.log = slog.Default()
	}

	var errs []error

	for _, af := range cfg.Flags {
		d := af.definition()

		switch {
		case !nameRe.MatchString(d.name):
			errs = append(errs, fmt.Errorf("flags: invalid name %q (lowercase letters, digits and _)", d.name))
			continue
		case f.byName[d.name].name != "":
			errs = append(errs, fmt.Errorf("flags: duplicate flag %q", d.name))
			continue
		}

		if _, err := normalize(d.kind, d.def); err != nil {
			errs = append(errs, fmt.Errorf("flags: default of %q: %w", d.name, err))
			continue
		}

		f.defs = append(f.defs, d)
		f.byName[d.name] = d
		f.defaults[d.name] = d.def

		if cfg.Env == nil {
			continue
		}

		raw, ok := cfg.Env.Lookup(envKey(d.name))
		if !ok || raw == "" {
			continue
		}

		if d.locked {
			errs = append(errs, fmt.Errorf("flags: %s is set but flag %q is locked", envKey(d.name), d.name))
			continue
		}

		v, err := normalize(d.kind, raw)
		if err != nil {
			errs = append(errs, fmt.Errorf("flags: %s: %w", envKey(d.name), err))
			continue
		}

		f.defaults[d.name] = v
	}

	if lister, ok := cfg.Env.(interface{ Keys() []string }); ok {
		known := map[string]bool{}
		for _, d := range f.defs {
			known[envKey(d.name)] = true
		}

		for _, k := range lister.Keys() {
			if strings.HasPrefix(k, EnvPrefix) && !known[k] {
				errs = append(errs, fmt.Errorf("flags: %s names no registered flag", k))
			}
		}
	}

	if err := errors.Join(errs...); err != nil {
		return nil, err
	}

	return f, nil
}

func envKey(name string) string { return EnvPrefix + strings.ToUpper(name) }

// All returns the registered flags in registration order.
func (f *Flags) All() []AnyFlag {
	out := make([]AnyFlag, len(f.defs))
	for i, d := range f.defs {
		out[i] = d
	}

	return out
}

// definition implements AnyFlag, so All can return registered definitions.
func (d definition) Name() string           { return d.name }
func (d definition) Description() string    { return d.desc }
func (d definition) Locked() bool           { return d.locked }
func (d definition) Kind() Kind             { return d.kind }
func (d definition) definition() definition { return d }

type scopeKey struct{}

// WithScope returns a context whose flag reads also apply the values
// stored for scope (for example "user:42" or "tenant:7"); stored scope
// values win over global ones. Set it once per request in middleware,
// after authentication. An empty scope reads global values only.
func WithScope(ctx context.Context, scope string) context.Context {
	return context.WithValue(ctx, scopeKey{}, scope)
}

func scopeFrom(ctx context.Context) string {
	s, _ := ctx.Value(scopeKey{}).(string)
	return s
}

// Enabled reports whether a boolean flag is on for the scope in ctx.
func (f *Flags) Enabled(ctx context.Context, flag Flag[bool]) bool { return Get(ctx, f, flag) }

// Get returns the value of flag for the scope in ctx. It never fails: on a
// store or cache error it returns the default and logs a warning; for a
// flag that was not registered it returns the code default and logs an
// error.
func Get[T Value](ctx context.Context, f *Flags, flag Flag[T]) T {
	d, ok := f.lookup(ctx, flag.name, flag.Kind())
	if !ok {
		return flag.def
	}

	return decode[T](f.resolve(ctx, d, scopeFrom(ctx)))
}

// lookup returns the registered definition of name, logging an error when
// there is none or it has another kind.
func (f *Flags) lookup(ctx context.Context, name string, kind Kind) (definition, bool) {
	d, ok := f.byName[name]
	if !ok || d.kind != kind {
		f.log.ErrorContext(ctx, "flags: flag is not registered (vukem.WithFlags)", "flag", name, "kind", kind)
		return definition{}, false
	}

	return d, true
}

// resolve returns the encoded value of d for scope through the cache.
func (f *Flags) resolve(ctx context.Context, d definition, scope string) string {
	if d.locked || f.store == nil {
		return f.defaults[d.name]
	}

	global, err := f.cached(ctx, Global)
	if err != nil {
		f.warn(ctx, err)
		return f.defaults[d.name]
	}

	var scoped map[string]string

	if scope != Global {
		if scoped, err = f.cached(ctx, scope); err != nil {
			f.warn(ctx, err)
			return f.defaults[d.name]
		}
	}

	return f.pick(ctx, d, global, scoped)
}

// pick applies the stored global and scoped values over the default,
// skipping (and logging) stored values that no longer fit the flag's kind.
func (f *Flags) pick(ctx context.Context, d definition, layers ...map[string]string) string {
	v := f.defaults[d.name]
	if d.locked {
		return v
	}

	for _, m := range layers {
		raw, ok := m[d.name]
		if !ok {
			continue
		}

		n, err := normalize(d.kind, raw)
		if err != nil {
			f.log.WarnContext(ctx, "flags: ignoring stored value", "flag", d.name, "err", err)
			continue
		}

		v = n
	}

	return v
}

func (f *Flags) warn(ctx context.Context, err error) {
	f.log.WarnContext(ctx, "flags: store unavailable, using defaults", "err", err)
}

func cacheKey(scope string) string {
	if scope == Global {
		return "flags:v1:global"
	}

	return "flags:v1:scope:" + scope
}

func (f *Flags) cached(ctx context.Context, scope string) (map[string]string, error) {
	if f.cache == nil {
		return f.store.Load(ctx, scope)
	}

	return cache.Remember(ctx, f.cache, cacheKey(scope), f.ttl, func(ctx context.Context) (map[string]string, error) {
		return f.store.Load(ctx, scope)
	})
}

// Change describes a write: the flag's resolved value in its scope before
// and after it.
type Change struct {
	Flag, Scope string
	Old, New    string
}

// Set stores the value of the flag called name in scope (Global for
// everyone). value is parsed by the flag's kind ("true", "42", "blue").
// It fails with ErrUnknown, ErrLocked, ErrInvalid or ErrNoStore, or with
// the store's error. The cached values of scope are forgotten.
func (f *Flags) Set(ctx context.Context, scope, name, value string) (Change, error) {
	return f.write(ctx, scope, name, func(d definition) error {
		v, err := normalize(d.kind, value)
		if err != nil {
			return err
		}

		return f.store.Save(ctx, scope, name, v)
	})
}

// Put stores a typed value of flag in scope; see Set.
func Put[T Value](ctx context.Context, f *Flags, scope string, flag Flag[T], value T) (Change, error) {
	return f.Set(ctx, scope, flag.name, encode(value))
}

// Reset removes the stored value of the flag called name in scope, so it
// falls back to the global value (for a scope) or the default.
func (f *Flags) Reset(ctx context.Context, scope, name string) (Change, error) {
	return f.write(ctx, scope, name, func(definition) error { return f.store.Delete(ctx, scope, name) })
}

func (f *Flags) write(ctx context.Context, scope, name string, apply func(definition) error) (Change, error) {
	d, ok := f.byName[name]

	switch {
	case !ok:
		return Change{}, fmt.Errorf("%w: %q", ErrUnknown, name)
	case d.locked:
		return Change{}, fmt.Errorf("%w: %q", ErrLocked, name)
	case f.store == nil:
		return Change{}, ErrNoStore
	case len(scope) > maxScope:
		return Change{}, fmt.Errorf("%w: scope longer than %d bytes", ErrInvalid, maxScope)
	}

	old, err := f.fresh(ctx, d, scope)
	if err != nil {
		return Change{}, fmt.Errorf("flags: read %q: %w", name, err)
	}

	if err := apply(d); err != nil {
		return Change{}, fmt.Errorf("flags: write %q: %w", name, err)
	}

	if f.cache != nil {
		// The TTL bounds staleness if this fails.
		if err := f.cache.Forget(ctx, cacheKey(scope)); err != nil {
			f.log.WarnContext(ctx, "flags: forget cached values", "scope", scope, "err", err)
		}
	}

	now, err := f.fresh(ctx, d, scope)
	if err != nil {
		return Change{}, fmt.Errorf("flags: read %q: %w", name, err)
	}

	return Change{Flag: name, Scope: scope, Old: old, New: now}, nil
}

// fresh resolves d for scope from the store, bypassing the cache.
func (f *Flags) fresh(ctx context.Context, d definition, scope string) (string, error) {
	global, err := f.store.Load(ctx, Global)
	if err != nil {
		return "", err
	}

	if scope == Global {
		return f.pick(ctx, d, global), nil
	}

	scoped, err := f.store.Load(ctx, scope)
	if err != nil {
		return "", err
	}

	return f.pick(ctx, d, global, scoped), nil
}

// State is a flag as the admin panel shows it.
type State struct {
	Name        string
	Description string
	Kind        Kind
	Locked      bool
	// Default is the code default, or FLAG_<NAME> when set.
	Default string
	// Value is the resolved value in the scope.
	Value string
	// Stored reports whether the scope has a stored value (ignored for
	// locked flags).
	Stored bool
}

// States returns every registered flag resolved for scope, read from the
// store without the cache. Unlike reads, it returns the store's error.
func (f *Flags) States(ctx context.Context, scope string) ([]State, error) {
	layers := []map[string]string{}

	if f.store != nil {
		global, err := f.store.Load(ctx, Global)
		if err != nil {
			return nil, fmt.Errorf("flags: load: %w", err)
		}

		layers = append(layers, global)

		if scope != Global {
			scoped, err := f.store.Load(ctx, scope)
			if err != nil {
				return nil, fmt.Errorf("flags: load %q: %w", scope, err)
			}

			layers = append(layers, scoped)
		}
	}

	out := make([]State, 0, len(f.defs))

	for _, d := range f.defs {
		stored := false
		if n := len(layers); n > 0 {
			_, stored = layers[n-1][d.name] // the scope's own layer
		}

		out = append(out, State{
			Name: d.name, Description: d.desc, Kind: d.kind, Locked: d.locked,
			Default: f.defaults[d.name], Value: f.pick(ctx, d, layers...), Stored: stored && !d.locked,
		})
	}

	return out, nil
}

// Expose returns the values of the allowlisted flags for the scope in ctx,
// keyed by name, for views and Inertia props. Only the flags passed are
// included: never expose every flag to the browser. Flags that were not
// registered are left out (and logged).
func (f *Flags) Expose(ctx context.Context, allow ...AnyFlag) map[string]any {
	out := make(map[string]any, len(allow))
	scope := scopeFrom(ctx)

	for _, af := range allow {
		d, ok := f.lookup(ctx, af.Name(), af.Kind())
		if !ok {
			continue
		}

		v := f.resolve(ctx, d, scope)

		switch d.kind {
		case KindBool:
			out[d.name] = decode[bool](v)
		case KindInt:
			out[d.name] = decode[int](v)
		default:
			out[d.name] = v
		}
	}

	return out
}
