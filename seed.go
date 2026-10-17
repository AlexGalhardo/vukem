package vukem

import (
	"context"
	"flag"
	"fmt"
	"strings"

	"github.com/AlexGalhardo/vukem/database"
	"github.com/AlexGalhardo/vukem/schema"
)

// Seeder fills the database with data (demo content, reference tables).
// `vukem db:seed` runs every seeder given to WithSeeders, in order.
type Seeder interface {
	Seed(ctx context.Context, db *database.DB) error
}

// SeederFunc adapts a function to Seeder.
type SeederFunc func(ctx context.Context, db *database.DB) error

// Seed implements Seeder.
func (f SeederFunc) Seed(ctx context.Context, db *database.DB) error { return f(ctx, db) }

// AppSeeder is a seeder that needs the booted App: configuration
// (app.Source), the logger, storage disks or services added by providers.
// It opens the database itself with app.DB() when it needs it.
type AppSeeder interface {
	SeedApp(ctx context.Context, app *App) error
}

// AppSeederFunc adapts a function to AppSeeder.
type AppSeederFunc func(ctx context.Context, app *App) error

// SeedApp implements AppSeeder.
func (f AppSeederFunc) SeedApp(ctx context.Context, app *App) error { return f(ctx, app) }

// dbSeeder runs a Seeder as an AppSeeder, so both kinds share one ordered
// list.
type dbSeeder struct{ s Seeder }

func (d dbSeeder) SeedApp(ctx context.Context, app *App) error {
	db, err := app.DB()
	if err != nil {
		return err
	}

	return d.s.Seed(ctx, db)
}

// WithSeeders registers the seeders run by `db:seed` (generated apps pass
// seeders.All()).
func WithSeeders(seeders ...Seeder) Option {
	return func(o *options) error {
		for _, s := range seeders {
			o.seeders = append(o.seeders, dbSeeder{s})
		}

		return nil
	}
}

// WithAppSeeders registers seeders that receive the App instead of only
// the database. They run with the WithSeeders ones, in option order.
func WithAppSeeders(seeders ...AppSeeder) Option {
	return func(o *options) error { o.seeders = append(o.seeders, seeders...); return nil }
}

// Seed runs every registered seeder in order; the first error stops it.
// db:seed boots the app first, so seeders see services providers open in
// Boot; call Boot before Seed when calling it directly.
func (a *App) Seed(ctx context.Context) error {
	for _, s := range a.seeders {
		if err := s.SeedApp(ctx, a); err != nil {
			return fmt.Errorf("seed %s: %w", seederName(s), err)
		}

		fmt.Fprintln(a.stdout, "Seeded:", seederName(s))
	}

	return nil
}

func seederName(s AppSeeder) string {
	var v any = s
	if d, ok := s.(dbSeeder); ok {
		v = d.s
	}

	name := fmt.Sprintf("%T", v)

	return name[strings.LastIndex(name, ".")+1:]
}

func seedCommands() []Command {
	var seedForce, wipeForce *bool

	return []Command{
		CommandFunc{
			CommandName: "db:seed",
			Desc:        "Run the database seeders",
			DefineFlags: func(fs *flag.FlagSet) { seedForce = fs.Bool("force", false, "allow in production") },
			Fn: func(ctx context.Context, app *App, _ []string) error {
				if app.Config.IsProduction() && !*seedForce {
					return errForceRequired
				}

				if len(app.seeders) == 0 {
					fmt.Fprintln(app.stdout, "No seeders registered (vukem make:seeder).")
					return nil
				}

				return app.Seed(ctx)
			},
		},
		CommandFunc{
			CommandName: "db:wipe",
			Desc:        "Drop every table",
			DefineFlags: func(fs *flag.FlagSet) { wipeForce = fs.Bool("force", false, "allow in production") },
			Fn: func(ctx context.Context, app *App, _ []string) error {
				if app.Config.IsProduction() && !*wipeForce {
					return errForceRequired
				}

				db, err := app.DB()
				if err != nil {
					return err
				}

				if err := schema.DropAll(ctx, db); err != nil {
					return err
				}

				fmt.Fprintln(app.stdout, "Dropped all tables.")

				return nil
			},
		},
	}
}
