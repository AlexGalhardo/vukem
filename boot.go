package vukem

import (
	"context"
	"fmt"

	"github.com/AlexGalhardo/vukem/router"
)

// Boot runs every provider's Boot hook once, in order, then the
// WithRoutesAfterBoot functions. Boot is not retryable: later calls return
// the first call's error (nil after a successful boot). A KindWarmup app
// never boots: Boot returns [ErrWarmupRun].
func (a *App) Boot(ctx context.Context) error {
	if a.Kind == KindWarmup {
		return ErrWarmupRun
	}

	a.bootMu.Lock()
	defer a.bootMu.Unlock()

	a.mu.Lock()
	booted := a.booted
	a.booted = true
	a.mu.Unlock()

	if booted {
		return a.bootErr
	}

	a.bootErr = a.boot(ctx)

	return a.bootErr
}

func (a *App) boot(ctx context.Context) error {
	if err := a.checkMailConfig(); err != nil {
		return err
	}

	if err := a.prepareDatabaseQueue(ctx); err != nil {
		return err
	}

	for _, p := range a.providers {
		b, ok := p.(Booter)
		if !ok || !runsIn(p, a.Kind) {
			continue
		}

		if err := b.Boot(ctx, a); err != nil {
			return fmt.Errorf("vukem: boot %T: %w", p, err)
		}
	}

	return a.addBootRoutes()
}

// addBootRoutes runs the WithRoutesAfterBoot functions once.
func (a *App) addBootRoutes() error {
	a.mu.Lock()
	fns := a.bootRoutes
	a.bootRoutes = nil
	a.mu.Unlock()

	if len(fns) == 0 {
		return nil
	}

	if a.Router.Compiled() {
		return ErrRoutesCompiled
	}

	for _, fn := range fns {
		if err := a.runBootRoutes(fn); err != nil {
			return err
		}
	}

	return nil
}

// runBootRoutes runs fn. In warmup a panic (typically a nil client that
// only Boot sets) becomes an error naming the cause, so codegen:routes
// reports it instead of crashing.
func (a *App) runBootRoutes(fn func(r *router.Router, app *App) error) (err error) {
	if a.Kind == KindWarmup {
		defer func() {
			if v := recover(); v != nil {
				err = fmt.Errorf("vukem: routes after boot panicked: %v "+
					"(route registration must not use booted services during warmup; "+
					"check app.Kind == vukem.KindWarmup)", v)
			}
		}()
	}

	if err := fn(a.Router, a); err != nil {
		return fmt.Errorf("vukem: routes after boot: %w", err)
	}

	return nil
}

// pendingBootRoutes reports whether WithRoutesAfterBoot functions have not
// run yet.
func (a *App) pendingBootRoutes() bool {
	a.mu.Lock()
	defer a.mu.Unlock()

	return len(a.bootRoutes) > 0
}
