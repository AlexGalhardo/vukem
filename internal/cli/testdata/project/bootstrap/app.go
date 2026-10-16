package bootstrap

import (
	"example.com/blog/database/migrations"

	lumek "github.com/AlexGalhardo/golang-framework"
)

// New builds the application.
func New(opts ...lumek.Option) (*lumek.App, error) {
	app, err := lumek.New(append([]lumek.Option{
		lumek.WithProviders(Providers()...),
		lumek.WithMigrations(migrations.All()),
	}, opts...)...)
	if err != nil {
		return nil, err
	}

	app.AddCommand(Commands()...)

	return app, nil
}
