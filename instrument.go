package vukem

import (
	"context"
	"errors"
	"sync"

	"github.com/AlexGalhardo/vukem/database"
	"github.com/AlexGalhardo/vukem/mail"
	"github.com/AlexGalhardo/vukem/queue"
)

// Instrumentation is a set of neutral hook points that a provider
// (tracing, metrics) attaches to with App.Instrument. Core only reports to
// them and never imports an instrumentation library; drivers/otel turns
// the reports into spans and metrics. Nil fields are skipped.
type Instrumentation struct {
	// Queries is notified of every statement run through DB() and its
	// transactions, after devtools and WithQueryObservers.
	Queries database.QueryObserver
	// Jobs is notified of every job step of Queue(), next to devtools.
	Jobs queue.Observer
	// Mail wraps the transport of Mailer(). Wrappers apply in registration
	// order around the devtools-wrapped transport, so the last one
	// registered is the outermost.
	Mail func(mail.Transport) mail.Transport
}

// ErrInstrumentTooLate is returned by App.Instrument when a hook targets a
// service that was already built: it would never be called.
var ErrInstrumentTooLate = errors.New(
	"vukem: Instrument must run before DB(), Queue() or Mailer() is first used (call it from a provider's Register)",
)

type instruments struct {
	mu      sync.Mutex
	queries []database.QueryObserver
	jobs    []queue.Observer
	mail    []func(mail.Transport) mail.Transport

	dbBuilt, queueBuilt, mailBuilt bool
}

// Instrument attaches in's hooks to the database, queue and mailer. Call it
// from a provider's Register, before those services are first used; a hook
// for a service already built fails with ErrInstrumentTooLate and no hook
// of in is attached. Several calls add up.
func (a *App) Instrument(in Instrumentation) error {
	a.inst.mu.Lock()
	defer a.inst.mu.Unlock()

	if (in.Queries != nil && a.inst.dbBuilt) ||
		(in.Jobs != nil && a.inst.queueBuilt) ||
		(in.Mail != nil && a.inst.mailBuilt) {
		return ErrInstrumentTooLate
	}

	if in.Queries != nil {
		a.inst.queries = append(a.inst.queries, in.Queries)
	}

	if in.Jobs != nil {
		a.inst.jobs = append(a.inst.jobs, in.Jobs)
	}

	if in.Mail != nil {
		a.inst.mail = append(a.inst.mail, in.Mail)
	}

	return nil
}

// queryObservers freezes the query hooks and returns every observer of
// DB(): devtools, then WithQueryObservers, then Instrument's. DB() passes
// each to database.WithQueryObserver, which skips typed nils and contains
// a panicking observer without starving the others.
func (a *App) queryObservers() []database.QueryObserver {
	a.inst.mu.Lock()
	defer a.inst.mu.Unlock()

	a.inst.dbBuilt = true

	var obs []database.QueryObserver
	if a.Devtools != nil {
		obs = append(obs, a.Devtools)
	}

	obs = append(obs, a.queryObs...)

	return append(obs, a.inst.queries...)
}

// jobObserver freezes the job hooks and returns them with devtools (nil
// when there is none).
func (a *App) jobObserver() queue.Observer {
	a.inst.mu.Lock()
	defer a.inst.mu.Unlock()

	a.inst.queueBuilt = true

	var obs []queue.Observer
	if a.Devtools != nil {
		obs = append(obs, a.Devtools)
	}

	obs = append(obs, a.inst.jobs...)

	switch len(obs) {
	case 0:
		return nil
	case 1:
		return obs[0]
	default:
		return jobObservers(obs)
	}
}

// wrapMail freezes the mail hooks and applies them to t.
func (a *App) wrapMail(t mail.Transport) mail.Transport {
	a.inst.mu.Lock()
	defer a.inst.mu.Unlock()

	a.inst.mailBuilt = true

	for _, wrap := range a.inst.mail {
		t = wrap(t)
	}

	return t
}

type jobObservers []queue.Observer

func (o jobObservers) ObserveJob(ctx context.Context, e queue.JobEvent) {
	for _, obs := range o {
		obs.ObserveJob(ctx, e)
	}
}
