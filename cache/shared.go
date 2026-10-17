package cache

import "iter"

// Sharer is implemented by a Store that states whether every server of the
// app sees the same entries: Shared reports true for a store backed by a
// database or Redis, false for one kept in this process ([Memory]).
//
// Features that coordinate servers through the cache (OnOneServer tasks,
// RATE_LIMIT_STORE=cache) ask [Shared]. A store wrapper (metrics, tracing)
// forwards the answer of the store it wraps, either by implementing Sharer
// or by exposing that store with an Unwrap() Store method.
//
// The SQLite [Database] store reports true, but a SQLite file is shared
// only by the processes on one host: servers on several hosts need
// PostgreSQL, MySQL or Redis.
type Sharer interface {
	Shared() bool
}

// Shared reports whether st is shared by every server of the app. The
// first store in [Layers] that implements [Sharer] answers; when none
// does (an undeclared store, nil, or an Unwrap chain longer than the
// limit), st is not shared, so a check that needs a shared store fails
// closed instead of trusting an unknown one.
func Shared(st Store) bool {
	for l := range Layers(st) {
		if s, ok := l.(Sharer); ok {
			return s.Shared()
		}
	}

	return false
}

// maxUnwrap bounds Unwrap chains, so a wrapper that unwraps to itself
// cannot loop forever.
const maxUnwrap = 32

// Layers yields st and then each store underneath it, following wrappers
// (metrics, tracing) that expose the store they wrap with an
// Unwrap() Store method. It stops at a store without Unwrap, at nil, or
// after 32 stores, whichever comes first; callers looking for a
// capability treat a chain cut by the limit as not having it.
func Layers(st Store) iter.Seq[Store] {
	return func(yield func(Store) bool) {
		for range maxUnwrap {
			if st == nil || !yield(st) {
				return
			}

			u, ok := st.(interface{ Unwrap() Store })
			if !ok {
				return
			}

			st = u.Unwrap()
		}
	}
}

// Shared implements [Sharer]: memory entries live in this process only.
func (*Memory) Shared() bool { return false }

// Shared implements [Sharer]: every server reading the same database sees
// the same entries.
func (*Database) Shared() bool { return true }
