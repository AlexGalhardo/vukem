package vukem

import (
	"context"
	"slices"
)

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
