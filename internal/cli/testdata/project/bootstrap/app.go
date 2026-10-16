package bootstrap

import (
	"example.com/blog/database/migrations"
	"example.com/blog/database/seeders"

	vukem "github.com/AlexGalhardo/vukem"
)

// New builds the application.
func New(opts ...vukem.Option) (*vukem.App, error) {
	app, err := vukem.New(append([]vukem.Option{
		vukem.WithProviders(Providers()...),
		vukem.WithMigrations(migrations.All()),
		vukem.WithSeeders(seeders.All()...),
	}, opts...)...)
	if err != nil {
		return nil, err
	}

	app.AddCommand(Commands()...)

	return app, nil
}
