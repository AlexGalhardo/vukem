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

// WithSeeders registers the seeders run by `db:seed` (generated apps pass
// seeders.All()).
func WithSeeders(seeders ...Seeder) Option {
	return func(o *options) error { o.seeders = append(o.seeders, seeders...); return nil }
}

// Seed runs every registered seeder in order; the first error stops it.
func (a *App) Seed(ctx context.Context) error {
	db, err := a.DB()
	if err != nil {
		return err
	}

	for _, s := range a.seeders {
		if err := s.Seed(ctx, db); err != nil {
			return fmt.Errorf("seed %s: %w", seederName(s), err)
		}

		fmt.Fprintln(a.stdout, "Seeded:", seederName(s))
	}

	return nil
}

func seederName(s Seeder) string {
	name := fmt.Sprintf("%T", s)
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
