package cache

// SetPruneBatch shrinks the Prune batch so tests can cross batches.
func SetPruneBatch(d *Database, n int) { d.pruneBatch = n }

// MaxUnwrap is the number of stores Layers yields at most.
const MaxUnwrap = maxUnwrap
