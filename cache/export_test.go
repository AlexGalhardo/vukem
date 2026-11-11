package cache

import "time"

// SetPruneBatch shrinks the Prune batch so tests can cross batches.
func SetPruneBatch(d *Database, n int) { d.pruneBatch = n }

// SetNow replaces the clock of d, so tests can expire entries without sleeping.
func SetNow(d *Database, now func() time.Time) { d.now = now }

// MaxUnwrap is the number of stores Layers yields at most.
const MaxUnwrap = maxUnwrap
