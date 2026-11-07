package vukem

import (
	"context"
	"fmt"
	"slices"

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

// Provider registers services into the App. Register must not perform I/O;
// use Boot for work that needs other services.
type Provider interface {
	Register(app *App) error
}

// Booter is implemented by providers that need a boot phase. Boot runs after
// every provider registered.
type Booter interface {
	Boot(ctx context.Context, app *App) error
}

// Shutdowner is implemented by providers that release resources. Shutdown
// runs in reverse provider order.
type Shutdowner interface {
	Shutdown(ctx context.Context, app *App) error
}

// KindFilter limits a provider to some runtime kinds. Providers that do not
// implement it run everywhere.
type KindFilter interface {
	Kinds() []Kind
}

func runsIn(p Provider, k Kind) bool {
	f, ok := p.(KindFilter)
	if !ok {
		return true
	}

	return slices.Contains(f.Kinds(), k)
}

// ProviderFunc adapts a function to a Provider with only a Register phase.
type ProviderFunc func(app *App) error

// Register calls f.
func (f ProviderFunc) Register(app *App) error { return f(app) }

// Runner is a long-running component (HTTP server, queue worker, scheduler).
// Run blocks until ctx is done or the runner fails, and must return after
// releasing its resources.
type Runner interface {
	Run(ctx context.Context) error
}

// RunnerFunc adapts a function to a Runner.
type RunnerFunc func(ctx context.Context) error

// Run calls f.
func (f RunnerFunc) Run(ctx context.Context) error { return f(ctx) }

type namedRunner struct {
	name   string
	runner Runner
}
