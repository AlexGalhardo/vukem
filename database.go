package vukem

import (
	"cmp"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/AlexGalhardo/vukem/database"
	"github.com/AlexGalhardo/vukem/schema"
)

// DatabaseConfig is loaded from DB_* variables.
type DatabaseConfig struct {
	// URL says where the database is; the scheme picks the driver.
	URL string `env:"DATABASE_URL" default:"file:storage/database.sqlite"`
	// MaxOpen is the maximum number of open connections (on SQLite, the reader pool).
	MaxOpen int `env:"DB_MAX_OPEN" default:"10"`
	// MaxIdle is the number of idle connections kept ready.
	MaxIdle int `env:"DB_MAX_IDLE" default:"5"`
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

// explainQueueTableClash adds the fix to a migration error that says an
// app table named like a database-queue table already exists. Boot creates
// those tables (QUEUE_TABLE, QUEUE_FAILED_TABLE, default "jobs" and
// "failed_jobs"), so an app migration that creates a table of the same name
// fails with a bare "already exists" that does not point at the cause.
func explainQueueTableClash(err error, q QueueConfig) error {
	if err == nil || q.Connection != "database" || !strings.Contains(strings.ToLower(err.Error()), "already exists") {
		return err
	}

	for _, t := range []struct{ name, env string }{{q.Table, "QUEUE_TABLE"}, {q.FailedTable, "QUEUE_FAILED_TABLE"}} {
		for _, quote := range []string{`"`, "'", "`"} {
			if strings.Contains(err.Error(), quote+t.name+quote) {
				return fmt.Errorf(
					"%w; vukem: %q is also the table of the database queue (QUEUE_CONNECTION=database), which "+
						"boot creates before your migrations. Rename your table, or set %s to another name such as "+
						"queue_%s",
					err,
					t.name,
					t.env,
					t.name,
				)
			}
		}
	}

	return err
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
				if err != nil {
					if q, qerr := configLoad[QueueConfig](app); qerr == nil {
						err = explainQueueTableClash(err, q)
					}
				}

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

// FlagSeeder is a Seeder or AppSeeder that takes command-line flags from
// db:seed. SeedFlags defines them on fs before each run. Implement it on a
// pointer receiver, bind the flags to its fields and register the pointer
// (&DemoCatalog{}); a seeder registered as a value that defines flags is an
// error, since the flags would bind to a copy:
//
//	func (s *DemoCatalog) SeedFlags(fs *flag.FlagSet) {
//		fs.BoolVar(&s.fresh, "fresh", false, "delete the demo catalog first")
//	}
//
//	vukem db:seed DemoCatalog --fresh
//
// The flags of every seeder that runs are accepted; seeders defining the
// same flag all receive its value. "force", "class", "help" and "h" belong
// to db:seed and cannot be redefined, and the words --force and --class are
// always read as db:seed's flags, so they cannot be values of a seeder
// flag. App.Seed applies the defaults.
type FlagSeeder interface {
	SeedFlags(fs *flag.FlagSet)
}

// Seed runs every registered seeder in order, with their flag defaults;
// the first error stops it. db:seed boots the app first, so seeders see
// services providers open in Boot; call Boot before Seed when calling it
// directly.
func (a *App) Seed(ctx context.Context) error {
	fs, err := seedFlagSet(a.seeders, new(bool), io.Discard)
	if err != nil {
		return err
	}

	if err := fs.Parse(nil); err != nil {
		return err
	}

	return a.runSeeders(ctx, a.seeders)
}

func (a *App) runSeeders(ctx context.Context, seeders []AppSeeder) error {
	for _, s := range seeders {
		if err := s.SeedApp(ctx, a); err != nil {
			return fmt.Errorf("seed %s: %w", seederName(s), err)
		}

		fmt.Fprintln(a.stdout, "Seeded:", seederName(s))
	}

	return nil
}

// seederValue is the seeder as registered (the Seeder inside a dbSeeder).
func seederValue(s AppSeeder) any {
	if d, ok := s.(dbSeeder); ok {
		return d.s
	}

	return s
}

// seederName is the seeder's Name() when it has one (Seeder or
// AppSeeder), else its type name.
func seederName(s AppSeeder) string {
	v := seederValue(s)

	if n, ok := v.(interface{ Name() string }); ok {
		return n.Name()
	}

	name := fmt.Sprintf("%T", v)

	return name[strings.LastIndex(name, ".")+1:]
}

// seedCommand is db:seed [Name...] [--class Name] [--force] [seeder flags].
// It parses its own arguments: the seeder flags are known only once the
// names have picked the seeders that run.
type seedCommand struct{}

func (seedCommand) Name() string { return "db:seed" }

func (seedCommand) Description() string {
	return "Run the database seeders (db:seed [Name...] [--class Name] [--force] [seeder flags])"
}

func (seedCommand) Flags(*flag.FlagSet) {}

func (seedCommand) rawArgs() {}

func (seedCommand) Run(ctx context.Context, app *App, args []string) error {
	names, flagArgs, err := splitSeedArgs(args)
	if err != nil {
		return seedUsage(app, err)
	}

	run, err := selectSeeders(app.seeders, names)
	if err != nil {
		return seedUsage(app, err)
	}

	force := new(bool)

	fs, err := seedFlagSet(run, force, app.stdout)
	if err != nil {
		return err
	}

	if err := fs.Parse(flagArgs); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}

		return fmt.Errorf("db:seed: %w: %w", err, errUsage) // flag printed the message
	}

	if fs.NArg() > 0 {
		return seedUsage(
			app,
			fmt.Errorf("db:seed: unexpected argument %q (seeder names go before the flags)", fs.Arg(0)),
		)
	}

	if app.Config.IsProduction() && !*force {
		return errForceRequired
	}

	if len(app.seeders) == 0 {
		fmt.Fprintln(app.stdout, "No seeders registered (vukem make:seeder).")
		return nil
	}

	return app.runSeeders(ctx, run)
}

// seedUsage prints err (Main prints nothing for a usage error) and marks it
// as one.
func seedUsage(app *App, err error) error {
	fmt.Fprintln(app.stdout, err)
	return fmt.Errorf("%w: %w", err, errUsage)
}

// splitSeedArgs returns the seeder names and the flag arguments. Names come
// first, mixed with db:seed's own --force and --class; the first other flag
// starts the seeder flags. --force and --class are always db:seed's, never
// a seeder flag's value. Every --class value is a name, and a --class
// without one is an error: selecting nothing would run every seeder.
func splitSeedArgs(args []string) (names, flagArgs []string, err error) {
	inFlags := false

	// --force is moved in front of the seeder flags, so it is never read as
	// the value of one ("--note --force").
	var force []string

	for i := 0; i < len(args); i++ {
		a := args[i]
		name, value, hasValue := strings.Cut(strings.TrimLeft(a, "-"), "=")
		isFlag := strings.HasPrefix(a, "-") && a != "-"

		switch {
		case a == "--":
			return names, slices.Concat(force, flagArgs, args[i:]), nil
		case isFlag && name == "class":
			if !hasValue && i+1 < len(args) {
				value = args[i+1]
				i++
			}

			if strings.TrimSpace(value) == "" {
				return nil, nil, errors.New("db:seed: -class needs a seeder name")
			}

			names = append(names, value)
		case !isFlag && !inFlags:
			names = append(names, a)
		case isFlag && name == "force":
			force = append(force, a)
		case isFlag:
			inFlags = true

			flagArgs = append(flagArgs, a)
		default:
			flagArgs = append(flagArgs, a)
		}
	}

	return names, slices.Concat(force, flagArgs), nil
}

// selectSeeders returns every seeder when names is empty, else the named
// ones in argument order, once each. A name matches a seeder's name
// case-insensitively, also without its "Seeder" suffix.
func selectSeeders(all []AppSeeder, names []string) ([]AppSeeder, error) {
	if len(names) == 0 {
		return all, nil
	}

	var out []AppSeeder

	// Seeders are deduplicated by index: a value seeder with a slice, map
	// or func field would make == panic.
	picked := make([]bool, len(all))

	for _, q := range names {
		var exact, suffixed []int

		for i, s := range all {
			switch n := seederName(s); {
			case strings.EqualFold(n, q):
				exact = append(exact, i)
			case strings.EqualFold(n, q+"Seeder"):
				suffixed = append(suffixed, i)
			}
		}

		found := exact
		if len(found) == 0 {
			found = suffixed
		}

		switch len(found) {
		case 0:
			return nil, fmt.Errorf("db:seed: unknown seeder %q (registered: %s)", q, seederNames(all))
		case 1:
			if !picked[found[0]] {
				picked[found[0]] = true
				out = append(out, all[found[0]])
			}
		default:
			return nil, fmt.Errorf("db:seed: seeder name %q is ambiguous (registered: %s)", q, seederNames(all))
		}
	}

	return out, nil
}

func seederNames(all []AppSeeder) string {
	if len(all) == 0 {
		return "none"
	}

	names := make([]string, len(all))
	for i, s := range all {
		names[i] = seederName(s)
	}

	return strings.Join(names, ", ")
}

// seedFlagSet defines db:seed's own flags and those of every FlagSeeder in
// seeders on one set. A flag several seeders define is set on all of them.
// Redefining force, class, help or h is an error, so no seeder can take over
// the production guard or hide the usage; a seeder defining flags must be a
// pointer, or they would bind to a copy.
func seedFlagSet(seeders []AppSeeder, force *bool, out io.Writer) (*flag.FlagSet, error) {
	fs := flag.NewFlagSet("db:seed", flag.ContinueOnError)
	fs.SetOutput(out)
	fs.BoolVar(force, "force", false, "allow in production")
	fs.Var(ignoredFlag{}, "class", "run only this seeder (repeatable; same as a name before the flags)")

	defined := map[string][]*flag.Flag{}

	var order []string

	for _, s := range seeders {
		fsr, ok := seederValue(s).(FlagSeeder)
		if !ok {
			continue
		}

		sub := flag.NewFlagSet(seederName(s), flag.ContinueOnError)
		fsr.SeedFlags(sub)

		var err error

		if hasFlags(sub) && reflect.ValueOf(seederValue(s)).Kind() != reflect.Pointer {
			return nil, fmt.Errorf("seeder %s defines flags but is not a pointer: register &%s{} so they bind to it",
				seederName(s), seederName(s))
		}

		sub.VisitAll(func(f *flag.Flag) {
			if reservedSeedFlag(f.Name) {
				err = cmp.Or(err, fmt.Errorf("seeder %s: flag -%s is reserved by db:seed", seederName(s), f.Name))
				return
			}

			if defined[f.Name] == nil {
				order = append(order, f.Name)
			}

			defined[f.Name] = append(defined[f.Name], f)
		})

		if err != nil {
			return nil, err
		}
	}

	for _, name := range order {
		flags := defined[name]
		values := make(fanout, len(flags))
		bools := 0

		for i, f := range flags {
			values[i] = f.Value
			if b, ok := f.Value.(interface{ IsBoolFlag() bool }); ok && b.IsBoolFlag() {
				bools++
			}
		}

		switch bools {
		case 0:
			fs.Var(values, name, flags[0].Usage)
		case len(flags):
			fs.Var(boolFanout{values}, name, flags[0].Usage)
		default:
			return nil, fmt.Errorf("db:seed: seeders define flag -%s both as a bool and with a value", name)
		}
	}

	return fs, nil
}

// reservedSeedFlag reports the flags that belong to db:seed (help and h
// print the usage).
func reservedSeedFlag(name string) bool {
	switch name {
	case "force", "class", "help", "h":
		return true
	default:
		return false
	}
}

func hasFlags(fs *flag.FlagSet) bool {
	n := 0
	fs.VisitAll(func(*flag.Flag) { n++ })

	return n > 0
}

// fanout sets one flag value on every seeder that defines the flag.
type fanout []flag.Value

func (f fanout) String() string {
	if len(f) == 0 {
		return ""
	}

	return f[0].String()
}

func (f fanout) Set(s string) error {
	for _, v := range f {
		if err := v.Set(s); err != nil {
			return err
		}
	}

	return nil
}

type boolFanout struct{ fanout }

func (boolFanout) IsBoolFlag() bool { return true }

// ignoredFlag accepts --class on the flag set; splitSeedArgs already read
// its values.
type ignoredFlag struct{}

func (ignoredFlag) String() string   { return "" }
func (ignoredFlag) Set(string) error { return nil }

// maxRunSQLBytes bounds a script read by db:run.
const maxRunSQLBytes = 8 << 20

// txControl matches a statement that starts, ends or splits a transaction: inside db:run's own transaction it would
// make the dry run commit (COMMIT) or end the transaction early (ROLLBACK, END), and a nested BEGIN is an error.
var txControl = regexp.MustCompile(`(?im)^\s*(begin|commit|rollback|end|start\s+transaction|set\s+transaction)\b`)

// implicitCommit matches DDL: MySQL (and MariaDB) commit the open transaction before and after it, so a dry run of a
// script holding it would apply it.
var implicitCommit = regexp.MustCompile(`(?im)^\s*(alter|create|drop|truncate|rename)\b`)

var errRunNoFile = fmt.Errorf("db:run needs -file <path> (or -file - for stdin): %w", errUsage)

// errDryRun rolls the transaction of a db:run dry run back; it never leaves the command.
var errDryRun = errors.New("dry run")

// dbRunCommand is db:run: it runs a SQL script in one transaction against the app's own database and rolls it back
// unless -commit is given, so a maintenance script gets a dry run on the real data first. The script may hold several
// statements; any error rolls the whole script back. The script is read from -file (a path, or - for stdin).
func dbRunCommand() Command {
	var (
		file   *string
		commit *bool
	)

	return CommandFunc{
		CommandName: "db:run",
		Desc:        "Run a SQL file in one transaction; rolls back unless -commit",
		DefineFlags: func(fs *flag.FlagSet) {
			file = fs.String("file", "", "SQL script path, or - for stdin")
			commit = fs.Bool("commit", false, "commit the transaction (default: roll back, a dry run)")
		},
		Fn: func(ctx context.Context, app *App, args []string) error {
			if len(args) > 0 || *file == "" {
				return errRunNoFile
			}

			script, err := readRunScript(*file, app.stdin)
			if err != nil {
				return err
			}

			if txControl.MatchString(script) {
				return errors.New("db:run: the script holds BEGIN, COMMIT, ROLLBACK or START TRANSACTION; " +
					"db:run owns the transaction, so remove them")
			}

			db, err := app.DB()
			if err != nil {
				return err
			}

			mysql := database.MustCapabilities(db.Dialect()).Family == "mysql"
			if mysql && implicitCommit.MatchString(script) {
				return errors.New("db:run: MySQL commits implicitly on ALTER, CREATE, DROP, TRUNCATE and RENAME, " +
					"so a dry run of this script would apply it; run schema changes as migrations")
			}

			err = db.Tx(ctx, func(tx *database.Tx) error {
				if _, err := tx.ExecContext(ctx, script); err != nil {
					if mysql {
						err = fmt.Errorf(
							"%w (MySQL runs several statements only with multiStatements=true in DATABASE_URL)",
							err,
						)
					}

					return err
				}

				if !*commit {
					return errDryRun
				}

				return nil
			})

			switch {
			case errors.Is(err, errDryRun):
				fmt.Fprintln(app.stdout, "db:run: dry run, rolled back. Pass -commit to apply.")

				return nil
			case err != nil:
				return fmt.Errorf("db:run: rolled back: %w", err)
			}

			fmt.Fprintln(app.stdout, "db:run: committed.")

			return nil
		},
	}
}

func readRunScript(file string, stdin io.Reader) (string, error) {
	r := stdin

	if file != "-" {
		f, err := os.Open(file)
		if err != nil {
			return "", fmt.Errorf("db:run: %w", err)
		}

		defer f.Close()

		r = f
	}

	raw, err := io.ReadAll(io.LimitReader(r, maxRunSQLBytes+1))
	if err != nil {
		return "", fmt.Errorf("db:run: read script: %w", err)
	}

	if len(raw) > maxRunSQLBytes {
		return "", fmt.Errorf("db:run: script is larger than %d bytes", maxRunSQLBytes)
	}

	if strings.TrimSpace(string(raw)) == "" {
		return "", errors.New("db:run: the script is empty")
	}

	return string(raw), nil
}

// dbBackupCommand is db:backup: a SQLite snapshot via VACUUM INTO, or a
// restore of that snapshot into a different directory. It is not a traffic
// cutover. PostgreSQL and MySQL are refused.
func dbBackupCommand() Command {
	var (
		to      *string
		from    *string
		restore *bool
	)

	return CommandFunc{
		CommandName: "db:backup",
		Desc:        "Snapshot SQLite with VACUUM INTO, or restore that file elsewhere",
		DefineFlags: func(fs *flag.FlagSet) {
			to = fs.String("to", "", "new snapshot or restore path (must not exist)")
			from = fs.String("from", "", "snapshot to restore (only with -restore)")
			restore = fs.Bool("restore", false, "copy -from to -to in another directory")
		},
		Fn: func(ctx context.Context, app *App, args []string) error {
			if len(args) > 0 || strings.TrimSpace(*to) == "" {
				return errors.New("db:backup needs -to <path>")
			}

			if *restore {
				return restoreSQLiteBackup(ctx, app, *from, *to)
			}

			if strings.TrimSpace(*from) != "" {
				return errors.New("db:backup: -from is only valid with -restore")
			}

			return snapshotSQLite(ctx, app, *to)
		},
	}
}

func snapshotSQLite(ctx context.Context, app *App, dest string) error {
	db, live, err := openSQLiteBackup(app)
	if err != nil {
		return err
	}

	if err := sameSQLiteFile(live, dest); err != nil {
		return err
	}

	if err := refuseNewFile(dest); err != nil {
		return err
	}

	abs, err := filepath.Abs(dest)
	if err != nil {
		return fmt.Errorf("db:backup: %w", err)
	}

	if _, err := db.SQL().ExecContext(ctx, "VACUUM INTO "+sqliteStringLiteral(abs)); err != nil {
		_ = os.Remove(abs)

		return fmt.Errorf("db:backup: snapshot: %w", err)
	}

	if err := finishSQLiteFile(ctx, abs); err != nil {
		_ = os.Remove(abs)

		return err
	}

	fmt.Fprintf(app.stdout, "db:backup: wrote %s\n", abs)

	return nil
}

func restoreSQLiteBackup(ctx context.Context, app *App, from, dest string) error {
	if strings.TrimSpace(from) == "" {
		return errors.New("db:backup -restore needs -from <snapshot>")
	}

	_, live, err := openSQLiteBackup(app)
	if err != nil {
		return err
	}

	src, err := filepath.Abs(from)
	if err != nil {
		return fmt.Errorf("db:backup: %w", err)
	}

	abs, err := filepath.Abs(dest)
	if err != nil {
		return fmt.Errorf("db:backup: %w", err)
	}

	if filepath.Dir(src) == filepath.Dir(abs) {
		return errors.New("db:backup: restore into a different directory than the snapshot")
	}

	if filepath.Clean(abs) == filepath.Clean(live) {
		return errors.New("db:backup: -to is the live database")
	}

	info, err := os.Lstat(src)
	if err != nil {
		return fmt.Errorf("db:backup: %w", err)
	}

	if info.Mode()&os.ModeSymlink != 0 || info.IsDir() {
		return errors.New("db:backup: -from must be a regular snapshot file")
	}

	if err := refuseNewFile(abs); err != nil {
		return err
	}

	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("db:backup: %w", err)
	}
	defer in.Close()

	out, err := os.OpenFile(abs, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("db:backup: %w", err)
	}

	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()

	if copyErr != nil || closeErr != nil {
		_ = os.Remove(abs)

		return fmt.Errorf("db:backup: restore: %w", cmp.Or(copyErr, closeErr))
	}

	if err := finishSQLiteFile(ctx, abs); err != nil {
		_ = os.Remove(abs)

		return err
	}

	fmt.Fprintf(app.stdout, "db:backup: restored %s\n", abs)

	return nil
}

// openSQLiteBackup opens the app database and returns its SQLite file path.
// Anything else, including an in-memory database, is an error.
func openSQLiteBackup(app *App) (*database.DB, string, error) {
	cfg, err := configLoad[DatabaseConfig](app)
	if err != nil {
		return nil, "", err
	}

	live, err := sqliteFilePath(cfg.URL)
	if err != nil {
		return nil, "", err
	}

	db, err := app.DB()
	if err != nil {
		return nil, "", err
	}

	// Identity, not family: the backup copies the embedded SQLite driver's file.
	if db.Dialect() != database.SQLite {
		return nil, "", errors.New("db:backup: only SQLite has a file snapshot")
	}

	return db, live, nil
}

func isMemorySQLite(raw string) bool {
	return raw == ":memory:" || strings.Contains(raw, "mode=memory")
}

// sqlitePathOf returns the file path inside a file: or sqlite: URL, and whether
// the URL names an in-memory database instead.
func sqlitePathOf(raw string) (path string, memory bool) {
	if isMemorySQLite(raw) {
		return "", true
	}

	path = strings.TrimPrefix(strings.TrimPrefix(raw, "sqlite:"), "file:")
	path, _, _ = strings.Cut(path, "?")

	if path == "" || path == ":memory:" {
		return "", true
	}

	return path, false
}

func isSQLiteURL(raw string) bool {
	return strings.HasPrefix(raw, "file:") || strings.HasPrefix(raw, "sqlite:")
}

func sqliteFilePath(raw string) (string, error) {
	if isMemorySQLite(raw) {
		return "", errors.New("db:backup: an in-memory database has no file to snapshot")
	}

	if !isSQLiteURL(raw) {
		return "", errors.New("db:backup: only SQLite has a file snapshot")
	}

	path, memory := sqlitePathOf(raw)
	if memory {
		return "", errors.New("db:backup: an in-memory database has no file to snapshot")
	}

	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("db:backup: %w", err)
	}

	return abs, nil
}

func sameSQLiteFile(live, dest string) error {
	abs, err := filepath.Abs(dest)
	if err != nil {
		return fmt.Errorf("db:backup: %w", err)
	}

	if filepath.Clean(abs) == filepath.Clean(live) {
		return errors.New("db:backup: -to is the live database")
	}

	return nil
}

// refuseNewFile rejects a path that exists, including a symlink whose
// target is missing. VACUUM INTO and the restore copy both create the file.
func refuseNewFile(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}

	if err != nil {
		return fmt.Errorf("db:backup: %w", err)
	}

	if info.Mode()&os.ModeSymlink != 0 {
		return errors.New("db:backup: -to is a symlink")
	}

	return errors.New("db:backup: -to already exists")
}

func finishSQLiteFile(ctx context.Context, path string) error {
	if err := sqliteIntegrity(ctx, path); err != nil {
		return err
	}

	if err := os.Chmod(path, 0o600); err != nil {
		return fmt.Errorf("db:backup: mode: %w", err)
	}

	return nil
}

func sqliteIntegrity(ctx context.Context, path string) error {
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path)+"?mode=ro")
	if err != nil {
		return fmt.Errorf("db:backup: integrity_check: %w", err)
	}

	defer db.Close()

	var status string
	if err := db.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&status); err != nil {
		return fmt.Errorf("db:backup: integrity_check: %w", err)
	}

	if status != "ok" {
		return errors.New("db:backup: integrity_check failed")
	}

	return nil
}

// sqliteStringLiteral quotes path for VACUUM INTO, which takes a filename
// literal rather than a bound parameter.
func sqliteStringLiteral(path string) string {
	return "'" + strings.ReplaceAll(path, "'", "''") + "'"
}

// dbConsole describes the client to start for a DATABASE_URL. Env carries
// the password, so it is never printed or logged.
type dbConsole struct {
	Bin  string
	Args []string
	Env  []string
}

// consoleFor picks the console client for rawURL. lookPath reports whether the
// client is installed. The password goes only in Env, never in Args, and no
// error text repeats the URL.
func consoleFor(rawURL string, lookPath func(string) (string, error)) (dbConsole, error) {
	if isMemorySQLite(rawURL) {
		return dbConsole{}, errors.New("db: an in-memory database has no file to open")
	}

	if isSQLiteURL(rawURL) {
		return sqliteConsole(rawURL, lookPath)
	}

	// Without "://" the text is no URL we know; naming it could print a password.
	scheme, _, ok := strings.Cut(rawURL, "://")
	if !ok {
		return dbConsole{}, errors.New("db: DATABASE_URL has no URL scheme")
	}

	if scheme != "postgres" && scheme != "postgresql" {
		return dbConsole{}, fmt.Errorf("db: no console client for %q URLs", scheme)
	}

	return postgresConsole(rawURL, lookPath)
}

func sqliteConsole(rawURL string, lookPath func(string) (string, error)) (dbConsole, error) {
	path, memory := sqlitePathOf(rawURL)
	if memory {
		return dbConsole{}, errors.New("db: an in-memory database has no file to open")
	}

	abs, err := filepath.Abs(path)
	if err != nil {
		return dbConsole{}, fmt.Errorf("db: %w", err)
	}

	if _, err := lookPath("sqlite3"); err != nil {
		return dbConsole{}, fmt.Errorf("db: sqlite3 is not installed (it opens %s)", abs)
	}

	return dbConsole{Bin: "sqlite3", Args: []string{abs}}, nil
}

func postgresConsole(rawURL string, lookPath func(string) (string, error)) (dbConsole, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		// url.Parse errors quote the URL, which holds the password.
		return dbConsole{}, errors.New("db: DATABASE_URL is not a valid URL")
	}

	name := strings.TrimPrefix(u.Path, "/")

	// psql reads a -d value that holds "=" or a scheme as a connection string, whose
	// parameters override the other options: refuse it instead of letting a name redirect the connection.
	if strings.ContainsAny(name, "= \t\r\n") || strings.Contains(name, "://") {
		return dbConsole{}, errors.New("db: the database name cannot be passed to psql")
	}

	if _, err := lookPath("psql"); err != nil {
		return dbConsole{}, errors.New("db: psql is not installed (it opens a postgres database)")
	}

	c := dbConsole{Bin: "psql"}

	for _, o := range [][2]string{{"-h", u.Hostname()}, {"-p", u.Port()}, {"-U", u.User.Username()}, {"-d", name}} {
		if o[1] != "" {
			c.Args = append(c.Args, o[0], o[1])
		}
	}

	if pw, ok := u.User.Password(); ok && pw != "" {
		c.Env = append(c.Env, "PGPASSWORD="+pw)
	}

	if mode := u.Query().Get("sslmode"); mode != "" {
		c.Env = append(c.Env, "PGSSLMODE="+mode)
	}

	return c, nil
}

func dbConsoleCommand() Command {
	var (
		force  *bool
		dryRun *bool
	)

	return CommandFunc{
		CommandName: "db",
		Desc:        "Open the database console (sqlite3, psql) for DATABASE_URL",
		DefineFlags: func(fs *flag.FlagSet) {
			force = fs.Bool("force", false, "allow in production")
			dryRun = fs.Bool("print", false, "print the client command (without the password) instead of running it")
		},
		Fn: func(ctx context.Context, app *App, args []string) error {
			if len(args) > 0 {
				return fmt.Errorf("db: unexpected argument %q: %w", args[0], errUsage)
			}

			if app.Config.IsProduction() && !*force {
				return errForceRequired
			}

			cfg, err := configLoad[DatabaseConfig](app)
			if err != nil {
				return err
			}

			lookPath := exec.LookPath
			if *dryRun {
				lookPath = func(name string) (string, error) { return name, nil }
			}

			c, err := consoleFor(cfg.URL, lookPath)
			if err != nil {
				return err
			}

			if *dryRun {
				_, err := fmt.Fprintln(app.Stdout(), strings.Join(append([]string{c.Bin}, c.Args...), " "))

				return err
			}

			cmd := exec.CommandContext(ctx, c.Bin, c.Args...) //nolint:gosec // G204: fixed client binaries
			cmd.Stdin, cmd.Stdout, cmd.Stderr = app.Stdin(), app.Stdout(), app.Stderr()
			cmd.Env = append(os.Environ(), c.Env...)

			return cmd.Run()
		},
	}
}

func seedCommands() []Command {
	var wipeForce *bool

	return []Command{
		seedCommand{},
		dbRunCommand(),
		dbBackupCommand(),
		dbConsoleCommand(),
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
