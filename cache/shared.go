package cache

// Sharer is implemented by a Store that states whether every server of the
// app sees the same entries: Shared reports true for a store backed by a
// database or Redis, false for one kept in this process ([Memory]).
//
// Features that coordinate servers through the cache (OnOneServer tasks,
// RATE_LIMIT_STORE=cache) ask [Shared]. A store wrapper (metrics, tracing)
// forwards the answer of the store it wraps, either by implementing Sharer
// or by exposing that store with an Unwrap() Store method.
type Sharer interface {
	Shared() bool
}

// Shared reports whether st is shared by every server of the app. A store
// that implements [Sharer] answers for itself; a store with an
// Unwrap() Store method is looked through; any other store, and nil, is
// not shared, so a check that needs a shared store fails closed instead
// of trusting an unknown one.
func Shared(st Store) bool {
	for st != nil {
		if s, ok := st.(Sharer); ok {
			return s.Shared()
		}

		u, ok := st.(interface{ Unwrap() Store })
		if !ok {
			return false
		}

		st = u.Unwrap()
	}

	return false
}

// Shared implements [Sharer]: memory entries live in this process only.
func (*Memory) Shared() bool { return false }

// Shared implements [Sharer]: every server reading the same database sees
// the same entries.
func (*Database) Shared() bool { return true }
