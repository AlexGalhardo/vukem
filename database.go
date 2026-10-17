package vukem

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/AlexGalhardo/vukem/database"
	"github.com/AlexGalhardo/vukem/schema"
)

// DatabaseConfig is loaded from DB_* variables.
type DatabaseConfig struct {
	URL     string `env:"DATABASE_URL" default:"file:storage/database.sqlite"`
	MaxOpen int    `env:"DB_MAX_OPEN"  default:"10"`
	MaxIdle int    `env:"DB_MAX_IDLE"  default:"5"`
}

// WithMigrations registers the application's migrations used by the migrate
// commands (migrate, migrate:rollback, migrate:reset, migrate:fresh,
// migrate:status).
func WithMigrations(migrations []schema.Migration) Option {
	return func(o *options) error { o.migrations = migrations; return nil }
}

// WithDatabaseDrivers makes extra drivers (e.g. postgres.Driver()) available
// to DB().
func WithDatabaseDrivers(drivers ...database.Driver) Option {
	return func(o *options) error { o.drivers = append(o.drivers, drivers...); return nil }
}

// WithQueryObservers reports every statement run through DB() (and its
// transactions) to each observer, e.g. a tracing exporter. They run after
// devtools' observer when devtools is on, so neither replaces the other.
func WithQueryObservers(obs ...database.QueryObserver) Option {
	return func(o *options) error { o.queryObs = append(o.queryObs, obs...); return nil }
}

// DB opens the default database from DATABASE_URL on first use, with
// app.Crypt as its encrypter (orm.Encrypted columns), adds a "db" health
// check and closes it on shutdown.
func (a *App) DB() (*database.DB, error) {
	a.dbOnce.Do(func() {
		cfg, err := configLoad[DatabaseConfig](a)
		if err != nil {
			a.dbErr = err
			return
		}

		opts := []database.Option{
			database.WithConfig(database.Config{MaxOpen: cfg.MaxOpen, MaxIdle: cfg.MaxIdle, Log: a.Log}),
		}
		for _, d := range a.drivers {
			opts = append(opts, database.WithDriver(d))
		}

		// A nil *crypt.Encrypter in the interface would defeat orm's
		// ErrNoEncrypter check.
		if a.Crypt != nil {
			opts = append(opts, database.WithEncrypter(a.Crypt))
		}

		for _, obs := range a.queryObservers() {
			opts = append(opts, database.WithQueryObserver(obs))
		}

		db, err := database.Open(context.Background(), cfg.URL, opts...)
		if err != nil {
			a.dbErr = err
			return
		}

		a.db = db
		a.AddHealthCheck("db", db.Ping)
		a.OnShutdown(func(context.Context) error { return db.Close() })
	})

	return a.db, a.dbErr
}

// Migrator returns a migrator over DB() with the registered migrations.
func (a *App) Migrator() (*schema.Migrator, error) {
	db, err := a.DB()
	if err != nil {
		return nil, err
	}

	return schema.NewMigrator(db, a.migrations)
}

var errForceRequired = errors.New("refusing to run a destructive command in production without --force")

func migrateCommands() []Command {
	return []Command{
		CommandFunc{
			CommandName: "migrate",
			Desc:        "Run pending migrations",
			Fn: func(ctx context.Context, app *App, _ []string) error {
				m, err := app.Migrator()
				if err != nil {
					return err
				}

				ran, err := m.Up(ctx)
				for _, id := range ran {
					fmt.Fprintln(app.stdout, "Migrated:", id)
				}

				if err == nil && len(ran) == 0 {
					fmt.Fprintln(app.stdout, "Nothing to migrate.")
				}

				return err
			},
		},
		rollbackCommand(),
		destructiveCommand("migrate:reset", "Roll back every migration", "Rolled back:",
			func(ctx context.Context, m *schema.Migrator) ([]string, error) { return m.Reset(ctx) }),
		destructiveCommand("migrate:fresh", "Drop all tables and re-run every migration", "Migrated:",
			func(ctx context.Context, m *schema.Migrator) ([]string, error) { return m.Fresh(ctx) }),
		statusCommand(),
	}
}

func rollbackCommand() Command {
	var step *int

	return CommandFunc{
		CommandName: "migrate:rollback",
		Desc:        "Roll back the last batch (or --step N migrations)",
		DefineFlags: func(fs *flag.FlagSet) { step = fs.Int("step", 0, "number of migrations to roll back") },
		Fn: func(ctx context.Context, app *App, _ []string) error {
			m, err := app.Migrator()
			if err != nil {
				return err
			}

			back, err := m.Rollback(ctx, *step)
			for _, id := range back {
				fmt.Fprintln(app.stdout, "Rolled back:", id)
			}

			return err
		},
	}
}

func destructiveCommand(
	name, desc, verb string,
	run func(context.Context, *schema.Migrator) ([]string, error),
) Command {
	var force *bool

	return CommandFunc{
		CommandName: name,
		Desc:        desc,
		DefineFlags: func(fs *flag.FlagSet) { force = fs.Bool("force", false, "allow in production") },
		Fn: func(ctx context.Context, app *App, _ []string) error {
			if app.Config.IsProduction() && !*force {
				return errForceRequired
			}

			m, err := app.Migrator()
			if err != nil {
				return err
			}

			ids, err := run(ctx, m)
			for _, id := range ids {
				fmt.Fprintln(app.stdout, verb, id)
			}

			return err
		},
	}
}

func statusCommand() Command {
	var asJSON *bool

	return CommandFunc{
		CommandName: "migrate:status",
		Desc:        "Show which migrations ran",
		DefineFlags: func(fs *flag.FlagSet) { asJSON = fs.Bool("json", false, "print JSON") },
		Fn: func(ctx context.Context, app *App, _ []string) error {
			m, err := app.Migrator()
			if err != nil {
				return err
			}

			st, err := m.Status(ctx)
			if err != nil {
				return err
			}

			if *asJSON {
				return json.NewEncoder(app.stdout).Encode(st)
			}

			tw := tabwriter.NewWriter(app.stdout, 0, 4, 2, ' ', 0)
			fmt.Fprintln(tw, "MIGRATION\tSTATUS\tBATCH")

			for _, s := range st {
				status := "Pending"

				switch {
				case s.Missing:
					status = "Ran (missing from code)"
				case s.Changed:
					status = "Ran (CHANGED since applied)"
				case s.Ran:
					status = "Ran"
				}

				batch := ""
				if s.Batch > 0 {
					batch = strconv.Itoa(s.Batch)
				}

				fmt.Fprintf(tw, "%s\t%s\t%s\n", s.ID, status, batch)
			}

			return tw.Flush()
		},
	}
}

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

// NamedAppSeeder returns an AppSeeder printed and reported as name (by
// db:seed and in errors) instead of its Go type, which is the same for
// every AppSeederFunc.
func NamedAppSeeder(name string, fn AppSeederFunc) AppSeeder { return namedAppSeeder{name, fn} }

type namedAppSeeder struct {
	name string
	fn   AppSeederFunc
}

func (n namedAppSeeder) SeedApp(ctx context.Context, app *App) error { return n.fn(ctx, app) }

func (n namedAppSeeder) Name() string { return n.name }

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

// seederName is the seeder's Name() when it has one (Seeder or
// AppSeeder), else its type name.
func seederName(s AppSeeder) string {
	var v any = s
	if d, ok := s.(dbSeeder); ok {
		v = d.s
	}

	if n, ok := v.(interface{ Name() string }); ok {
		return n.Name()
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
