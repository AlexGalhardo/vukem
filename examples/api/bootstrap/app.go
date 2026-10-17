// Package bootstrap wires the application together. There is no hidden
// registration: everything the app uses is listed here.
package bootstrap

import (
	vukem "github.com/AlexGalhardo/vukem"
	"github.com/AlexGalhardo/vukem/examples/api/app/jobs"
	"github.com/AlexGalhardo/vukem/examples/api/app/listeners"
	"github.com/AlexGalhardo/vukem/examples/api/database/migrations"
	"github.com/AlexGalhardo/vukem/examples/api/database/seeders"
	"github.com/AlexGalhardo/vukem/examples/api/resources"
	"github.com/AlexGalhardo/vukem/examples/api/routes"
)

// New builds the application; cmd/app and the tests call it.
func New(opts ...vukem.Option) (*vukem.App, error) {
	base := []vukem.Option{
		vukem.WithProviders(Providers()...),
		vukem.WithMigrations(migrations.All()),
		vukem.WithSeeders(seeders.All()...),
		vukem.WithListeners(listeners.All()...),
		vukem.WithJobs(jobs.All()...),
		vukem.WithSchedule(routes.Schedule),
		vukem.WithViews(resources.Views()),
	}

	for _, fn := range routes.All() {
		base = append(base, vukem.WithRoutes(fn))
	}

	app, err := vukem.New(append(base, opts...)...)
	if err != nil {
		return nil, err
	}

	app.AddCommand(Commands()...)

	return app, nil
}
