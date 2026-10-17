package database

import (
	"context"
	"database/sql"
	"time"
)

// Query is one statement run through a DB or Tx, reported to a
// QueryObserver after it returns.
type Query struct {
	// SQL is the statement text as passed by the caller.
	SQL string
	// Args are the bound values; observers must not log or display them
	// (they hold user data and secrets).
	Args []any
	// Duration is the time until the driver returned (for queries: until
	// the rows were ready, not until they were read).
	Duration time.Duration
	// Err is the statement error, if any.
	Err error
}

// QueryObserver is notified of every statement run through a DB opened
// with WithQueryObserver (and its transactions). It runs on the caller's
// goroutine, so it must be fast and safe for concurrent use. Statements
// run directly on DB.SQL() bypass it.
type QueryObserver interface {
	ObserveQuery(ctx context.Context, q Query)
}

// WithQueryObserver reports every statement to obs (development tooling
// such as devtools, tracing). It can be passed several times: every
// observer is notified, in the order given. Without one the hot path costs
// one nil check.
func WithQueryObserver(obs QueryObserver) Option {
	return func(o *openOptions) { o.obs = chainObservers(o.obs, obs) }
}

// SetQueryObserver replaces every observer of a DB built with FromSQL or
// Open with obs (nil removes them). Call it before the DB is shared between
// goroutines; AddQueryObserver keeps the existing ones.
func (db *DB) SetQueryObserver(obs QueryObserver) { db.obs = obs }

// AddQueryObserver adds obs after the DB's current observers (devtools
// installed by App.DB keeps working next to a tracing observer). Call it
// before the DB is shared between goroutines.
func (db *DB) AddQueryObserver(obs QueryObserver) { db.obs = chainObservers(db.obs, obs) }

// queryObservers notifies each observer in order.
type queryObservers []QueryObserver

func (obs queryObservers) ObserveQuery(ctx context.Context, q Query) {
	for _, o := range obs {
		o.ObserveQuery(ctx, q)
	}
}

// chainObservers returns cur followed by next, skipping nils, without
// nesting fan-outs.
func chainObservers(cur, next QueryObserver) QueryObserver {
	switch {
	case next == nil:
		return cur
	case cur == nil:
		return next
	}

	var out queryObservers
	if multi, ok := cur.(queryObservers); ok {
		out = append(out, multi...)
	} else {
		out = append(out, cur)
	}

	return append(out, next)
}

func observeRows(
	ctx context.Context, obs QueryObserver,
	run func(context.Context, string, ...any) (*sql.Rows, error), query string, args []any,
) (*sql.Rows, error) {
	if obs == nil {
		return run(ctx, query, args...)
	}

	start := time.Now()
	rows, err := run(ctx, query, args...)
	obs.ObserveQuery(ctx, Query{SQL: query, Args: args, Duration: time.Since(start), Err: err})

	return rows, err
}

func observeRow(
	ctx context.Context, obs QueryObserver,
	run func(context.Context, string, ...any) *sql.Row, query string, args []any,
) *sql.Row {
	if obs == nil {
		return run(ctx, query, args...)
	}

	start := time.Now()
	row := run(ctx, query, args...)
	obs.ObserveQuery(ctx, Query{SQL: query, Args: args, Duration: time.Since(start), Err: row.Err()})

	return row
}
