// Package bootstrap wires the application together. There is no hidden
// registration: everything the app uses is listed here.
package bootstrap

import (
	vukem "github.com/AlexGalhardo/vukem"

	"example.com/blog/database/migrations"
	"example.com/blog/database/seeders"
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
