package vukem

import (
	"cmp"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"go/format"
	"io"
	"maps"
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
	"time"

	"github.com/AlexGalhardo/vukem/database"
	"github.com/AlexGalhardo/vukem/internal/naming"
	"github.com/AlexGalhardo/vukem/orm"
	"github.com/AlexGalhardo/vukem/schema"
	"github.com/AlexGalhardo/vukem/schema/stores"
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
		migrateRunCommand(),
		rollbackCommand(),
		destructiveCommand("migrate:reset", "Roll back every migration", "Rolled back:",
			func(ctx context.Context, m *schema.Migrator) ([]string, error) { return m.Reset(ctx) }),
		destructiveCommand("migrate:fresh", "Drop all tables and re-run every migration", "Migrated:",
			func(ctx context.Context, m *schema.Migrator) ([]string, error) { return m.Fresh(ctx) }),
		statusCommand(),
		migrateCheckCommand(),
		migrateDiffCommand(),
		dbPullCommand(),
	}
}

// migrateRunCommand is "migrate": it runs the pending migrations. In
// production it refuses a pending migration that drops a table or a column
// unless --allow-destructive is given.
func migrateRunCommand() Command {
	var allow *bool

	return CommandFunc{
		CommandName: "migrate",
		Desc:        "Run pending migrations (in production, --allow-destructive to run ones that drop data)",
		DefineFlags: func(fs *flag.FlagSet) {
			allow = fs.Bool(
				"allow-destructive",
				false,
				"run pending migrations that drop tables or columns in production",
			)
		},
		Fn: func(ctx context.Context, app *App, _ []string) error {
			m, err := app.Migrator()
			if err != nil {
				return err
			}

			if app.Config.IsProduction() && !*allow {
				if err := checkDestructive(ctx, app, m); err != nil {
					return err
				}
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
				case s.OutOfOrder:
					status = "Pending (out of order)"
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

// ------------------------------------------------------------------ db:pull and migrate:diff

// colSpec is one column as the generated migration code writes it.
type colSpec struct {
	name      string
	kind      string // schema.Table method: "String", "BigInteger"...
	length    int
	precision int
	scale     int
	nullable  bool
	def       sql.Null[string]
	unique    bool
	fk        *schema.ForeignKeyInfo
}

// builderDefault turns a catalog default expression into the argument of
// Column.Default.
func builderDefault(def string) string {
	d := strings.TrimSpace(def)
	if i := strings.Index(d, "::"); i > 0 && strings.HasPrefix(d, "'") {
		d = strings.TrimSpace(d[:i]) // 'a'::text
	}

	d = strings.TrimSuffix(strings.TrimPrefix(d, "("), ")")

	switch {
	case strings.EqualFold(d, "true"), strings.EqualFold(d, "false"):
		return strings.ToLower(d)
	case len(d) >= 2 && d[0] == '\'' && d[len(d)-1] == '\'':
		return strconv.Quote(strings.ReplaceAll(d[1:len(d)-1], "''", "'"))
	}

	if _, err := strconv.ParseFloat(d, 64); err == nil {
		return d
	}

	return "schema.Expr(" + strconv.Quote(d) + ")"
}

// colLine writes the builder call of c, with its modifiers.
func colLine(c colSpec) string {
	var b strings.Builder

	q := strconv.Quote(c.name)

	switch {
	case c.kind == "ID":
		return "t.ID()"
	case c.fk != nil && c.kind == "BigInteger":
		b.WriteString("t.ForeignID(" + q + ")")
	case c.kind == "String":
		b.WriteString("t.String(" + q + ", " + strconv.Itoa(c.length) + ")")
	case c.kind == "Decimal":
		b.WriteString("t.Decimal(" + q + ", " + strconv.Itoa(c.precision) + ", " + strconv.Itoa(c.scale) + ")")
	default:
		b.WriteString("t." + c.kind + "(" + q + ")")
	}

	if c.fk != nil && c.kind == "BigInteger" {
		b.WriteString(".Constrained(" + strconv.Quote(c.fk.RefTable) + ")")

		switch c.fk.OnDelete {
		case "CASCADE":
			b.WriteString(".CascadeOnDelete()")
		case "SET NULL":
			b.WriteString(".NullOnDelete()")
		case "RESTRICT":
			b.WriteString(".RestrictOnDelete()")
		}
	}

	if c.nullable && (c.fk == nil || c.fk.OnDelete != "SET NULL") {
		b.WriteString(".Nullable()")
	}

	if c.def.Valid {
		b.WriteString(".Default(" + builderDefault(c.def.V) + ")")
	}

	if c.unique {
		b.WriteString(".Unique()")
	}

	return b.String()
}

// fromColumnInfo turns a catalog column into a spec, noting what the
// builder has no method for.
func fromColumnInfo(tbl schema.TableInfo, c schema.ColumnInfo) colSpec {
	spec := colSpec{
		name: c.Name, kind: c.Kind, length: c.Length, precision: c.Precision, scale: c.Scale,
		nullable: c.Nullable, def: c.Default,
	}

	if c.Kind == "ID" {
		spec.nullable, spec.def = false, sql.Null[string]{}
	}

	for i := range tbl.ForeignKeys {
		fk := &tbl.ForeignKeys[i]
		if len(fk.Columns) == 1 && fk.Columns[0] == c.Name && len(fk.RefColumns) == 1 && fk.RefColumns[0] == "id" {
			spec.fk = fk
		}
	}

	for _, ix := range tbl.Indexes {
		if ix.Unique && !ix.Expression && len(ix.Columns) == 1 && ix.Columns[0] == c.Name && ix.Where == "" &&
			(ix.Name == "" || ix.Name == tbl.Name+"_"+c.Name+"_key" || ix.Name == c.Name) {
			spec.unique = true
		}
	}

	return spec
}

// tableBody writes the body of the Create closure for tbl: columns, composite
// key and the indexes not already expressed on a column.
func tableBody(tbl schema.TableInfo, skip func(schema.ColumnInfo) bool) (lines, warnings []string) {
	for _, c := range tbl.Columns {
		if skip != nil && skip(c) || c.Generated {
			if c.Generated {
				warnings = append(
					warnings,
					fmt.Sprintf("%s.%s is a generated column; add it with s.Raw", tbl.Name, c.Name),
				)
			}

			continue
		}

		if c.Kind == "" {
			lines = append(lines, fmt.Sprintf(
				"// db:pull: column %q has type %q, which the schema builder has no method for; add it with s.Raw.",
				c.Name,
				c.Type,
			))
			warnings = append(
				warnings,
				fmt.Sprintf("column %s.%s has type %q, which the schema builder has no method for",
					tbl.Name, c.Name, c.Type),
			)

			continue
		}

		lines = append(lines, colLine(fromColumnInfo(tbl, c)))
	}

	if len(tbl.PrimaryKey) > 1 || (len(tbl.PrimaryKey) == 1 && !hasKind(tbl, tbl.PrimaryKey[0], "ID")) {
		cols := make([]string, len(tbl.PrimaryKey))
		for i, c := range tbl.PrimaryKey {
			cols[i] = strconv.Quote(c)
		}

		lines = append(lines, "t.Primary("+strings.Join(cols, ", ")+")")
	}

	for _, ix := range tbl.Indexes {
		if ix.Expression {
			warnings = append(warnings, fmt.Sprintf(
				"table %s: index %q is on an expression, which the builder cannot write; add it with Schema.Raw",
				tbl.Name,
				ix.Name,
			))

			continue
		}

		if len(ix.Columns) == 1 && ix.Unique && ix.Where == "" &&
			(ix.Name == "" || ix.Name == tbl.Name+"_"+ix.Columns[0]+"_key" ||
				ix.Name == ix.Columns[0]) {
			continue // written as Unique() on its column
		}

		cols := make([]string, len(ix.Columns))
		for i, c := range ix.Columns {
			cols[i] = strconv.Quote(c)
		}

		call := "t.Index"
		if ix.Unique {
			call = "t.UniqueIndex"
		}

		line := call + "(" + strings.Join(cols, ", ") + ")"
		if ix.Where != "" {
			line += ".Where(" + strconv.Quote(ix.Where) + ")"
		}

		lines = append(lines, line)
	}

	return lines, warnings
}

func hasKind(tbl schema.TableInfo, col, kind string) bool {
	for _, c := range tbl.Columns {
		if c.Name == col {
			return c.Kind == kind
		}
	}

	return false
}

// sortedForCreate orders tables so the ones a foreign key references come
// first (alphabetical otherwise, and for cycles).
func sortedForCreate(tables []schema.TableInfo) []schema.TableInfo {
	byName := map[string]schema.TableInfo{}
	for _, t := range tables {
		byName[t.Name] = t
	}

	var (
		out   []schema.TableInfo
		state = map[string]int{} // 1 visiting, 2 done
		visit func(string)
	)

	visit = func(n string) {
		if state[n] != 0 {
			return
		}

		state[n] = 1

		for _, fk := range byName[n].ForeignKeys {
			if _, ok := byName[fk.RefTable]; ok && fk.RefTable != n {
				visit(fk.RefTable)
			}
		}

		state[n] = 2
		out = append(out, byName[n])
	}

	names := make([]string, 0, len(tables))
	for _, t := range tables {
		names = append(names, t.Name)
	}

	slices.Sort(names)

	for _, n := range names {
		visit(n)
	}

	return out
}

// baselineGo renders one migration that creates every table of snap, in the
// format of the make:migration stub.
func baselineGo(pkg, id string, snap schema.Snapshot) (src []byte, warnings []string, err error) {
	for _, t := range snap.Tables {
		if err := checkPullNames(t); err != nil {
			return nil, nil, err
		}
	}

	var b strings.Builder

	tables := sortedForCreate(snap.Tables)

	b.WriteString("package " + pkg + "\n\nimport \"github.com/AlexGalhardo/vukem/schema\"\n\n")
	b.WriteString("// Baseline of an existing database, written by db:pull.\n")
	b.WriteString(
		"func " + migrationFunc(
			id,
		) + "() schema.Migration {\n\treturn schema.Migration{\n\t\tID: " + strconv.Quote(
			id,
		) + ",\n",
	)
	b.WriteString("\t\tUp: func(s *schema.Schema) error {\n")

	for _, tbl := range tables {
		lines, w := tableBody(tbl, nil)
		warnings = append(warnings, w...)

		b.WriteString("\t\t\tif err := s.Create(" + strconv.Quote(tbl.Name) + ", func(t *schema.Table) {\n")

		for _, l := range lines {
			b.WriteString("\t\t\t\t" + l + "\n")
		}

		b.WriteString("\t\t\t}); err != nil {\n\t\t\t\treturn err\n\t\t\t}\n\n")
	}

	b.WriteString("\t\t\treturn nil\n\t\t},\n\t\tDown: func(s *schema.Schema) error {\n")

	for _, tbl := range slices.Backward(tables) {
		b.WriteString(
			"\t\t\tif err := s.DropIfExists(" + strconv.Quote(
				tbl.Name,
			) + "); err != nil {\n\t\t\t\treturn err\n\t\t\t}\n\n",
		)
	}

	b.WriteString("\t\t\treturn nil\n\t\t},\n\t}\n}\n")

	src, err = format.Source([]byte(b.String()))
	if err != nil {
		return nil, nil, fmt.Errorf("db:pull: format the migration: %w", err)
	}

	return src, warnings, nil
}

// parseDefault turns a catalog default expression into the value of
// Column.Default and says whether it is a raw expression.
func parseDefault(def string) (value any, expr bool) {
	d := strings.TrimSpace(def)
	if i := strings.Index(d, "::"); i > 0 && strings.HasPrefix(d, "'") {
		d = strings.TrimSpace(d[:i])
	}

	d = strings.TrimSuffix(strings.TrimPrefix(d, "("), ")")

	switch {
	case strings.EqualFold(d, "true"):
		return true, false
	case strings.EqualFold(d, "false"):
		return false, false
	case len(d) >= 2 && d[0] == '\'' && d[len(d)-1] == '\'':
		return strings.ReplaceAll(d[1:len(d)-1], "''", "'"), false
	}

	if n, err := strconv.ParseInt(d, 10, 64); err == nil {
		return n, false
	}

	if f, err := strconv.ParseFloat(d, 64); err == nil {
		return f, false
	}

	return d, true
}

// applyCol calls the builder the way colLine writes the code, so the specs
// of a pulled schema can be replayed in a test.
func applyCol(t *schema.Table, c colSpec) {
	var col *schema.Column

	switch {
	case c.kind == "ID":
		t.ID()
		return
	case c.fk != nil && c.kind == "BigInteger":
		col = t.ForeignID(c.name).Constrained(c.fk.RefTable)

		switch c.fk.OnDelete {
		case "CASCADE":
			col.CascadeOnDelete()
		case "SET NULL":
			col.NullOnDelete()
		case "RESTRICT":
			col.RestrictOnDelete()
		}
	case c.kind == "String":
		col = t.String(c.name, c.length)
	case c.kind == "Decimal":
		col = t.Decimal(c.name, c.precision, c.scale)
	case c.kind == "Text":
		col = t.Text(c.name)
	case c.kind == "Integer":
		col = t.Integer(c.name)
	case c.kind == "SmallInteger":
		col = t.SmallInteger(c.name)
	case c.kind == "BigInteger":
		col = t.BigInteger(c.name)
	case c.kind == "Boolean":
		col = t.Boolean(c.name)
	case c.kind == "Float":
		col = t.Float(c.name)
	case c.kind == "Date":
		col = t.Date(c.name)
	case c.kind == "Timestamp":
		col = t.Timestamp(c.name)
	case c.kind == "JSON":
		col = t.JSON(c.name)
	case c.kind == "UUID":
		col = t.UUID(c.name)
	case c.kind == "ULID":
		col = t.ULID(c.name)
	case c.kind == "Binary":
		col = t.Binary(c.name)
	default:
		return
	}

	if c.nullable && (c.fk == nil || c.fk.OnDelete != "SET NULL") {
		col.Nullable()
	}

	if c.def.Valid {
		if v, isExpr := parseDefault(c.def.V); isExpr {
			col.Default(schema.Expr(c.def.V))
		} else {
			col.Default(v)
		}
	}

	if c.unique {
		col.Unique()
	}
}

// applyTableDef declares tbl's columns, keys and indexes on t through the
// builder (the calls tableBody writes as code).
func applyTableDef(t *schema.Table, tbl schema.TableInfo) {
	for _, c := range tbl.Columns {
		if c.Kind != "" && !c.Generated {
			applyCol(t, fromColumnInfo(tbl, c))
		}
	}

	if len(tbl.PrimaryKey) > 1 || (len(tbl.PrimaryKey) == 1 && !hasKind(tbl, tbl.PrimaryKey[0], "ID")) {
		t.Primary(tbl.PrimaryKey...)
	}

	for _, ix := range tbl.Indexes {
		if ix.Expression || isColumnUnique(tbl, ix) {
			continue
		}

		i := t.Index(ix.Columns...)
		if ix.Unique {
			i = t.UniqueIndex(ix.Columns...)
		}

		if ix.Where != "" {
			i.Where(ix.Where)
		}
	}
}

// isColumnUnique reports whether ix is the UNIQUE constraint of one column,
// which is written as Unique() on the column instead of as an index.
func isColumnUnique(tbl schema.TableInfo, ix schema.IndexInfo) bool {
	return len(ix.Columns) == 1 && ix.Unique && ix.Where == "" &&
		(ix.Name == "" || ix.Name == tbl.Name+"_"+ix.Columns[0]+"_key" || ix.Name == ix.Columns[0])
}

// baselineMigration is the migration baselineGo writes, as a value: the
// same specs applied through the builder.
func baselineMigration(id string, snap schema.Snapshot) schema.Migration {
	tables := sortedForCreate(snap.Tables)

	return schema.Migration{
		ID: id,
		Up: func(s *schema.Schema) error {
			for _, tbl := range tables {
				if err := s.Create(tbl.Name, func(t *schema.Table) { applyTableDef(t, tbl) }); err != nil {
					return err
				}
			}

			return nil
		},
	}
}

// migrationFunc is the function name of a migration ID ("20261201_x_y" is
// "m20261201XY").
func migrationFunc(id string) string {
	var b strings.Builder

	b.WriteString("m")

	for i, w := range strings.Split(id, "_") {
		if i == 0 {
			b.WriteString(w)
			continue
		}

		b.WriteString(naming.Pascal(w))
	}

	return b.String()
}

// singularTable is the singular of a table name, with the rules of the
// router's resource naming: ies to y, sses/shes/ches/xes lose es, a final s
// that is not ss is dropped.
func singularTable(s string) string {
	switch {
	case strings.HasSuffix(s, "ies") && len(s) > 3:
		return s[:len(s)-3] + "y"
	case strings.HasSuffix(s, "sses"),
		strings.HasSuffix(s, "shes"),
		strings.HasSuffix(s, "ches"),
		strings.HasSuffix(s, "xes"):
		return s[:len(s)-2]
	case strings.HasSuffix(s, "s") && !strings.HasSuffix(s, "ss"):
		return s[:len(s)-1]
	}

	return s
}

// modelGo renders the Go struct of one table, for db:pull -models.
func modelGo(pkg string, t schema.TableInfo) ([]byte, error) {
	if err := checkPullNames(t); err != nil {
		return nil, err
	}

	var (
		fields  strings.Builder
		imports = map[string]bool{}
		names   = map[string]bool{}
	)

	cols := map[string]bool{}
	for _, c := range t.Columns {
		cols[c.Name] = true
	}

	withModel := cols["id"] && cols["created_at"] && cols["updated_at"]

	if withModel {
		fields.WriteString("\torm.Model\n\n")

		imports["github.com/AlexGalhardo/vukem/orm"] = true
	}

	for _, c := range t.Columns {
		if withModel && (c.Name == "id" || c.Name == "created_at" || c.Name == "updated_at") {
			continue
		}

		typ, imp := goFieldType(c)
		if typ == "" {
			continue
		}

		if imp != "" {
			imports[imp] = true
		}

		name := naming.Pascal(c.Name)
		if names[name] {
			continue
		}

		names[name] = true

		tag := ""
		if naming.Snake(name) != c.Name {
			tag = " `db:\"" + c.Name + "\"`"
		}

		fields.WriteString("\t" + name + " " + typ + tag + "\n")
	}

	var b strings.Builder

	b.WriteString("package " + pkg + "\n\n")

	if len(imports) > 0 {
		b.WriteString("import (\n")

		for _, p := range slices.Sorted(maps.Keys(imports)) {
			b.WriteString("\t" + strconv.Quote(p) + "\n")
		}

		b.WriteString(")\n\n")
	}

	b.WriteString(
		"// " + naming.Pascal(singularTable(t.Name)) + " is a row of the " + t.Name + " table, written by db:pull.\n",
	)
	b.WriteString("type " + naming.Pascal(singularTable(t.Name)) + " struct {\n" + fields.String() + "}\n")

	src, err := format.Source([]byte(b.String()))
	if err != nil {
		return nil, fmt.Errorf("db:pull: format the model of %s: %w", t.Name, err)
	}

	return src, nil
}

// goFieldType is the Go type of a column and the import it needs.
func goFieldType(c schema.ColumnInfo) (typ, imp string) {
	base, imp := "", ""

	switch c.Kind {
	case "ID", "BigInteger", "Integer", "SmallInteger":
		base = "int64"
	case "String", "Text", "ULID":
		base = "string"
	case "Boolean":
		base = "bool"
	case "Float", "Decimal":
		base = "float64"
	case "Timestamp", "Date":
		base, imp = "time.Time", "time"
	case "UUID":
		base, imp = "uuid.UUID", "github.com/AlexGalhardo/vukem/uuid"
	case "Binary":
		return "[]byte", ""
	case "JSON":
		return "orm.JSON[map[string]any]", "github.com/AlexGalhardo/vukem/orm"
	default:
		return "", ""
	}

	if c.Nullable && c.Kind != "ID" {
		return "sql.Null[" + base + "]", "database/sql"
	}

	return base, imp
}

// frameworkTables are the tables the framework owns, which db:pull and
// migrate:diff leave alone.
func frameworkTables() []string {
	return append(stores.Tables(stores.Options{}), "migrations")
}

var safeNameRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// checkPullNames refuses a table or column whose name is not a plain
// identifier: the catalog is data, and a backtick, a quote or a line break in
// a name would become code (or a path) in the generated files.
func checkPullNames(t schema.TableInfo) error {
	if !safeNameRe.MatchString(t.Name) {
		return fmt.Errorf("db:pull: table %q is not a plain identifier; rename it or leave it out with -table", t.Name)
	}

	for _, c := range t.Columns {
		if !safeNameRe.MatchString(c.Name) {
			return fmt.Errorf("db:pull: column %q of table %s is not a plain identifier; rename it", c.Name, t.Name)
		}
	}

	return nil
}

// multiFlag is a repeatable string flag.
type multiFlag []string

func (m *multiFlag) String() string     { return strings.Join(*m, ",") }
func (m *multiFlag) Set(v string) error { *m = append(*m, v); return nil }

func dbPullCommand() Command {
	var (
		dir     *string
		tables  multiFlag
		models  *bool
		markApp *bool
	)

	return CommandFunc{
		CommandName: "db:pull",
		Desc:        "Write a baseline migration (and with -models, Go structs) from the existing database",
		DefineFlags: func(fs *flag.FlagSet) {
			dir = fs.String("o", "database/migrations", "directory of the generated migration")
			fs.Var(&tables, "table", "only this table (repeatable)")
			models = fs.Bool("models", false, "also write app/models/<name>.go for each table (never overwrites)")
			markApp = fs.Bool("mark-applied", false, "record the generated migration as already applied")
		},
		Fn: func(ctx context.Context, app *App, _ []string) error {
			db, err := app.DB()
			if err != nil {
				return err
			}

			snap, err := schema.Inspect(ctx, db)
			if err != nil {
				return err
			}

			snap.Tables = slices.DeleteFunc(snap.Tables, func(t schema.TableInfo) bool {
				return slices.Contains(frameworkTables(), t.Name) ||
					(len(tables) > 0 && !slices.Contains(tables, t.Name))
			})

			if len(snap.Tables) == 0 {
				return errors.New("db:pull: no tables to pull")
			}

			id := app.Now().UTC().Format("20060102150405") + "_baseline"

			src, warnings, err := baselineGo(filepath.Base(*dir), id, snap)
			if err != nil {
				return err
			}

			if err := os.MkdirAll(*dir, 0o750); err != nil {
				return fmt.Errorf("db:pull: %w", err)
			}

			path := filepath.Join(*dir, id+".go")
			if err := createFile(path, src); err != nil {
				return fmt.Errorf("db:pull: %w", err)
			}

			fmt.Fprintln(app.stdout, "Wrote", path)

			for _, w := range warnings {
				fmt.Fprintln(app.stderr, "db:pull: warning:", w)
			}

			if *models {
				if err := writeModels(app, snap); err != nil {
					return err
				}
			}

			fmt.Fprintf(app.stdout, "Register it: add %s() to database/migrations/registry.go\n", migrationFunc(id))

			if *markApp {
				return markBaselineApplied(ctx, app, id)
			}

			return nil
		},
	}
}

// createFile writes src to a new file: it fails with os.ErrExist rather than
// overwrite one, so a generated migration never replaces a migration that
// shares its ID (a second run in the same second) or one the developer edited.
func createFile(path string, src []byte) error {
	//nolint:gosec // G703: the developer chose the output directory
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}

	_, werr := f.Write(src)

	return errors.Join(werr, f.Close())
}

func writeModels(app *App, snap schema.Snapshot) error {
	if err := os.MkdirAll("app/models", 0o750); err != nil {
		return fmt.Errorf("db:pull: %w", err)
	}

	for _, t := range snap.Tables {
		src, err := modelGo("models", t)
		if err != nil {
			return err
		}

		path := filepath.Join("app", "models", naming.Snake(singularTable(t.Name))+".go")

		err = createFile(path, src)
		if errors.Is(err, os.ErrExist) {
			fmt.Fprintln(app.stderr, "db:pull: kept existing", path)
			continue
		}

		if err != nil {
			return fmt.Errorf("db:pull: %w", err)
		}

		fmt.Fprintln(app.stdout, "Wrote", path)
	}

	return nil
}

// markBaselineApplied records the generated migration as applied, once it is
// compiled into the binary (registered with WithMigrations).
func markBaselineApplied(ctx context.Context, app *App, id string) error {
	m, err := app.Migrator()
	if err != nil {
		return err
	}

	pending, err := m.Pending(ctx)
	if err != nil {
		return err
	}

	if err := m.MarkApplied(ctx, id); err != nil {
		if errors.Is(err, schema.ErrUnknownMigration) {
			fmt.Fprintln(app.stdout, "Not registered yet: rebuild and run db:pull -mark-applied again")

			return nil
		}

		return err
	}

	if !slices.ContainsFunc(pending, func(mig schema.Migration) bool { return mig.ID == id }) {
		fmt.Fprintln(app.stdout, "Already applied:", id)

		return nil
	}

	fmt.Fprintln(app.stdout, "Marked as applied:", id)

	return nil
}

type changeKind int

const (
	createTable  changeKind = iota // Model != nil, Table does not exist
	addColumn                      // Field != nil
	dropColumn                     // needs --delete table.column
	dropTable                      // needs --delete table
	renameColumn                   // Column -> To, from --rename
	warn                           // listed only, nothing generated
)

// change is one difference between the models and the database.
type change struct {
	Kind   changeKind
	Table  string
	Column string         // "" for table-level changes
	To     string         // new column name, renameColumn only
	Model  *orm.ModelInfo // createTable
	Field  *orm.FieldInfo // addColumn
	Note   string         // warn: the human-readable message
}

var intKinds = []string{"ID", "BigInteger", "Integer", "SmallInteger"}

// kindGroup puts the builder kinds that one database type can hold together,
// so a Go int stored as INTEGER or BIGINT is not reported as a difference.
func kindGroup(kind string) string {
	switch {
	case slices.Contains(intKinds, kind):
		return "int"
	case kind == "String" || kind == "Text" || kind == "ULID" || kind == "UUID" || kind == "JSON":
		return "text"
	default:
		return kind
	}
}

// specFromField derives the column the models say the table should have.
func specFromField(f orm.FieldInfo, isID bool) (spec colSpec, note string) {
	spec = colSpec{name: f.Column}
	rt := f.Type

	if rt.Kind() == reflect.Pointer {
		spec.nullable, rt = true, rt.Elem()
	}

	switch {
	case isID:
		spec.kind = "ID"
	case rt.Kind() == reflect.String:
		spec.kind, spec.length = "String", 255
	case rt.Kind() >= reflect.Int && rt.Kind() <= reflect.Uint64:
		spec.kind = "BigInteger"
	case rt.Kind() == reflect.Bool:
		spec.kind = "Boolean"
	case rt.Kind() == reflect.Float32 || rt.Kind() == reflect.Float64:
		spec.kind = "Float"
	case rt == reflect.TypeFor[time.Time]():
		spec.kind = "Timestamp"
	case rt == reflect.TypeFor[orm.NullTime]():
		spec.kind, spec.nullable = "Timestamp", true
	case rt.Kind() == reflect.Slice && rt.Elem().Kind() == reflect.Uint8:
		spec.kind = "Binary"
	case rt.PkgPath() == "github.com/AlexGalhardo/vukem/uuid" && rt.Name() == "UUID":
		spec.kind = "UUID"
	case strings.HasPrefix(rt.Name(), "JSON["):
		spec.kind = "JSON"
	case strings.HasPrefix(rt.Name(), "Encrypted["):
		spec.kind = "Text"
	default:
		spec.kind = "Text"
		note = fmt.Sprintf("column %s has Go type %s, which has no builder method; generated as Text", f.Column, rt)
	}

	return spec, note
}

// diffModels compares the models with the database. ignore are tables the
// framework owns.
func diffModels(models []orm.ModelInfo, snap schema.Snapshot, ignore []string) []change {
	var out []change

	modeled := map[string]bool{}

	for i := range models {
		m := &models[i]
		modeled[m.Table] = true

		tbl, ok := snap.Table(m.Table)
		if !ok {
			out = append(out, change{Kind: createTable, Table: m.Table, Model: m})
			continue
		}

		have := map[string]schema.ColumnInfo{}
		for _, c := range tbl.Columns {
			have[c.Name] = c
		}

		want := map[string]bool{}

		for j := range m.Fields {
			f := &m.Fields[j]
			want[f.Column] = true

			spec, note := specFromField(*f, f.Column == "id" && len(m.Keys) == 1)

			c, exists := have[f.Column]
			switch {
			case !exists:
				out = append(
					out,
					change{Kind: addColumn, Table: m.Table, Column: f.Column, Field: f, Model: m, Note: note},
				)
			case c.Kind != "" && kindGroup(c.Kind) != kindGroup(spec.kind):
				out = append(out, change{
					Kind:   warn,
					Table:  m.Table,
					Column: f.Column,
					Note: fmt.Sprintf(
						"column %s.%s is %s in the database but %s in the model",
						m.Table,
						f.Column,
						c.Type,
						spec.kind,
					),
				})
			}
		}

		for _, c := range tbl.Columns {
			if !want[c.Name] {
				out = append(out, change{Kind: dropColumn, Table: m.Table, Column: c.Name, Model: m})
			}
		}
	}

	for _, t := range snap.Tables {
		if !modeled[t.Name] && !slices.Contains(ignore, t.Name) {
			out = append(
				out,
				change{
					Kind:  warn,
					Table: t.Name,
					Note:  "table " + t.Name + " has no model (pass --delete " + t.Name + " to drop it)",
				},
			)
		}
	}

	return out
}

// diffGo renders the migration for changes, in the format of the
// make:migration stub. On SQLite, dropping a column that has an index is
// written as Schema.Rebuild with the full definition from the model, since
// ALTER TABLE cannot do it.
func diffGo(
	pkg, id string,
	changes []change,
	snap schema.Snapshot,
	d database.Dialect,
) (src []byte, warnings []string, err error) {
	sqlite := database.MustCapabilities(d).Family == "sqlite"
	byTable := groupByTable(changes)

	var up, down strings.Builder

	rebuilds := false

	for _, table := range slices.Sorted(maps.Keys(byTable)) {
		cs := byTable[table]

		genWholeTables(&up, &down, table, cs)

		var plan alterPlan

		// One Rebuild carries every change of its table: it drops the other
		// columns by leaving them out, creates the added ones and copies a
		// renamed column through RenamedFrom, so s.Table must not follow it.
		needsRebuild := sqlite && slices.ContainsFunc(cs, func(c change) bool {
			return c.Kind == dropColumn && columnIsIndexed(snap, table, c.Column)
		})

		if needsRebuild {
			rebuilds = true

			w, err := genRebuild(&up, &down, table, cs, changes, snap)
			if err != nil {
				return nil, nil, err
			}

			warnings = append(warnings, w...)

			continue
		}

		for _, c := range cs {
			switch c.Kind {
			case addColumn:
				plan.adds = append(plan.adds, c)
			case renameColumn:
				plan.renames = append(plan.renames, c)
			case dropColumn:
				plan.drops = append(plan.drops, c)
			case createTable, dropTable, warn:
			}
		}

		plan.write(&up, &down, table)
	}

	var b strings.Builder

	b.WriteString("package " + pkg + "\n\nimport \"github.com/AlexGalhardo/vukem/schema\"\n\n")
	b.WriteString("// Written by migrate:diff from the models; review it before you run it.\n")
	b.WriteString(
		"func " + migrationFunc(
			id,
		) + "() schema.Migration {\n\treturn schema.Migration{\n\t\tID: " + strconv.Quote(
			id,
		) + ",\n",
	)

	if rebuilds {
		b.WriteString("\t\tNoTransaction: true, // Rebuild manages its own transaction on SQLite\n")
	}

	b.WriteString("\t\tUp: func(s *schema.Schema) error {\n" + up.String() + "\t\t\treturn nil\n\t\t},\n")
	b.WriteString("\t\tDown: func(s *schema.Schema) error {\n" + down.String() + "\t\t\treturn nil\n\t\t},\n\t}\n}\n")

	src, err = format.Source([]byte(b.String()))
	if err != nil {
		return nil, nil, fmt.Errorf("migrate:diff: format the migration: %w", err)
	}

	return src, warnings, nil
}

// genWholeTables writes the Create and Drop calls of a table.
func genWholeTables(up, down *strings.Builder, table string, cs []change) {
	for _, c := range cs {
		switch c.Kind {
		case createTable:
			up.WriteString("\t\t\tif err := s.Create(" + strconv.Quote(table) + ", func(t *schema.Table) {\n")

			for _, l := range modelLines(*c.Model, nil) {
				up.WriteString("\t\t\t\t" + l + "\n")
			}

			up.WriteString("\t\t\t}); err != nil {\n\t\t\t\treturn err\n\t\t\t}\n\n")
			down.WriteString(
				"\t\t\tif err := s.DropIfExists(" + strconv.Quote(
					table,
				) + "); err != nil {\n\t\t\t\treturn err\n\t\t\t}\n\n",
			)
		case dropTable:
			up.WriteString(
				"\t\t\tif err := s.Drop(" + strconv.Quote(table) + "); err != nil {\n\t\t\t\treturn err\n\t\t\t}\n\n",
			)
			down.WriteString("\t\t\t// The drop of " + table + " cannot be undone.\n")
		case addColumn, dropColumn, renameColumn, warn:
		}
	}
}

// genRebuild writes a Schema.Rebuild with the full definition of table. cs
// are all the changes of the table: the drops leave their column out of it,
// the adds are nullable (the rows have no value for them) and a rename
// becomes a RenamedFrom option. It returns what the definition does not
// reproduce from the database; the same list is a comment above the call.
func genRebuild(
	up, down *strings.Builder,
	table string,
	cs, changes []change,
	snap schema.Snapshot,
) (losses []string, err error) {
	m, _ := modelOf(changes, table)
	if m == nil {
		return nil, fmt.Errorf("migrate:diff: no model for %s to rebuild it", table)
	}

	plan := rebuildPlan{dropped: map[string]bool{}, added: map[string]bool{}, renamed: map[string]string{}}

	var opts []string

	for _, c := range cs {
		switch c.Kind {
		case dropColumn:
			plan.dropped[c.Column] = true
		case addColumn:
			plan.added[c.Column] = true
		case renameColumn:
			plan.renamed[c.Column] = c.To
			opts = append(opts, "schema.RenamedFrom("+strconv.Quote(c.Column)+", "+strconv.Quote(c.To)+")")
		case createTable, dropTable, warn:
		}
	}

	tbl, _ := snap.Table(table)
	lines, losses := rebuildLines(*m, tbl, plan)

	for _, l := range losses {
		up.WriteString("\t\t\t// migrate:diff did not reproduce: " + l + "\n")
	}

	if len(losses) > 0 {
		up.WriteString("\t\t\t// Add them to the definition below, or accept losing them.\n")
	}

	up.WriteString("\t\t\tif err := s.Rebuild(" + strconv.Quote(table) + ", func(t *schema.Table) {\n")

	for _, l := range lines {
		up.WriteString("\t\t\t\t" + l + "\n")
	}

	up.WriteString("\t\t\t}")

	for _, o := range opts {
		up.WriteString(", " + o)
	}

	up.WriteString("); err != nil {\n\t\t\t\treturn err\n\t\t\t}\n\n")
	down.WriteString("\t\t\t// The changes to " + table + " cannot be undone: it was rebuilt.\n")

	warnings := make([]string, len(losses))
	for i, l := range losses {
		warnings[i] = "the rebuild of " + table + " does not reproduce " + l
	}

	return warnings, nil
}

// alterPlan is the column changes of one table that go through s.Table.
type alterPlan struct{ adds, drops, renames []change }

func (p alterPlan) write(up, down *strings.Builder, table string) {
	if len(p.adds)+len(p.renames)+len(p.drops) == 0 {
		return
	}

	up.WriteString("\t\t\tif err := s.Table(" + strconv.Quote(table) + ", func(t *schema.Table) {\n")
	down.WriteString("\t\t\tif err := s.Table(" + strconv.Quote(table) + ", func(t *schema.Table) {\n")

	for _, c := range p.adds {
		spec, _ := specFromField(*c.Field, false)
		spec.nullable = true // existing rows have no value for it

		up.WriteString("\t\t\t\t" + colLine(spec) + "\n")
		down.WriteString("\t\t\t\tt.DropColumn(" + strconv.Quote(c.Column) + ")\n")
	}

	for _, c := range p.renames {
		up.WriteString("\t\t\t\tt.RenameColumn(" + strconv.Quote(c.Column) + ", " + strconv.Quote(c.To) + ")\n")
		down.WriteString("\t\t\t\tt.RenameColumn(" + strconv.Quote(c.To) + ", " + strconv.Quote(c.Column) + ")\n")
	}

	for _, c := range p.drops {
		up.WriteString("\t\t\t\tt.DropColumn(" + strconv.Quote(c.Column) + ")\n")
		down.WriteString("\t\t\t\t// " + c.Column + " was dropped and cannot be restored.\n")
	}

	up.WriteString("\t\t\t}); err != nil {\n\t\t\t\treturn err\n\t\t\t}\n\n")
	down.WriteString("\t\t\t}); err != nil {\n\t\t\t\treturn err\n\t\t\t}\n\n")
}

func groupByTable(changes []change) map[string][]change {
	out := map[string][]change{}

	for _, c := range changes {
		if c.Kind != warn {
			out[c.Table] = append(out[c.Table], c)
		}
	}

	return out
}

func modelOf(changes []change, table string) (*orm.ModelInfo, bool) {
	for _, c := range changes {
		if c.Table == table && c.Model != nil {
			return c.Model, true
		}
	}

	return nil, false
}

func columnIsIndexed(snap schema.Snapshot, table, col string) bool {
	tbl, ok := snap.Table(table)
	if !ok {
		return false
	}

	for _, ix := range tbl.Indexes {
		if slices.Contains(ix.Columns, col) {
			return true
		}
	}

	return false
}

// modelLines writes the columns of a model for a Create or Rebuild closure.
func modelLines(m orm.ModelInfo, skip map[string]bool) []string {
	var lines []string

	for _, f := range m.Fields {
		if skip[f.Column] {
			continue
		}

		spec, _ := specFromField(f, f.Column == "id" && len(m.Keys) == 1)
		lines = append(lines, colLine(spec))
	}

	if len(m.Keys) > 1 {
		cols := make([]string, len(m.Keys))
		for i, k := range m.Keys {
			cols[i] = strconv.Quote(k)
		}

		lines = append(lines, "t.Primary("+strings.Join(cols, ", ")+")")
	}

	return lines
}

// rebuildPlan is what changes in a rebuilt table.
type rebuildPlan struct {
	dropped, added map[string]bool
	renamed        map[string]string // old column -> new column
}

// rebuildLines is the full definition of a rebuilt table: the model's
// columns without the dropped ones, plus the indexes and unique constraints
// of the database that do not cite a dropped column (a renamed column's
// index follows the rename). losses lists what the database has that the
// builder does not write, so the migration does not silently drop it.
func rebuildLines(m orm.ModelInfo, tbl schema.TableInfo, p rebuildPlan) (lines, losses []string) {
	for _, f := range m.Fields {
		if p.dropped[f.Column] {
			continue
		}

		spec, _ := specFromField(f, f.Column == "id" && len(m.Keys) == 1)

		fromDB := fromColumnInfo(tbl, columnInfoOf(tbl, f.Column))
		spec.unique, spec.fk = fromDB.unique, fromDB.fk
		spec.nullable = spec.nullable || p.added[f.Column]

		if c := columnInfoOf(tbl, f.Column); c.Default.Valid {
			losses = append(losses, "the default of "+f.Column+" ("+c.Default.V+")")
		}

		lines = append(lines, colLine(spec))
	}

	for _, ix := range tbl.Indexes {
		if ix.Expression {
			losses = append(losses, "the expression index "+ix.Name)
			continue
		}

		if slices.ContainsFunc(ix.Columns, func(c string) bool { return p.dropped[c] }) {
			continue
		}

		if len(ix.Columns) == 1 && ix.Unique && ix.Where == "" &&
			(ix.Name == "" || ix.Name == tbl.Name+"_"+ix.Columns[0]+"_key" ||
				ix.Name == ix.Columns[0]) {
			continue // already written as Unique() on its column
		}

		cols := make([]string, len(ix.Columns))
		for i, c := range ix.Columns {
			if to, ok := p.renamed[c]; ok {
				c = to
			}

			cols[i] = strconv.Quote(c)
		}

		call := "t.Index"
		if ix.Unique {
			call = "t.UniqueIndex"
		}

		line := call + "(" + strings.Join(cols, ", ") + ")"
		if ix.Where != "" {
			line += ".Where(" + strconv.Quote(ix.Where) + ")"
		}

		lines = append(lines, line)
	}

	return lines, append(losses, tableLosses(tbl, p)...)
}

// tableLosses lists the table-level definitions a rebuild from the models
// cannot reproduce.
func tableLosses(tbl schema.TableInfo, p rebuildPlan) []string {
	var out []string

	for _, fk := range tbl.ForeignKeys {
		if slices.ContainsFunc(fk.Columns, func(c string) bool { return p.dropped[c] }) {
			continue
		}

		if len(fk.Columns) > 1 || len(fk.RefColumns) != 1 || fk.RefColumns[0] != "id" {
			out = append(out, "the foreign key on ("+strings.Join(fk.Columns, ", ")+") to "+fk.RefTable)
		}
	}

	if tbl.SQLite == nil {
		return out
	}

	if tbl.SQLite.HasCheck {
		out = append(out, "the CHECK constraints")
	}

	if len(tbl.SQLite.Triggers) > 0 {
		out = append(out, "the triggers "+strings.Join(tbl.SQLite.Triggers, ", ")+
			" (Rebuild refuses the table until you pass schema.DropTriggers())")
	}

	return out
}

func columnInfoOf(tbl schema.TableInfo, name string) schema.ColumnInfo {
	for _, c := range tbl.Columns {
		if c.Name == name {
			return c
		}
	}

	return schema.ColumnInfo{Name: name}
}

// applyConsent turns the --delete and --rename flags into changes, and
// returns the destructive ones that nobody consented to.
func applyConsent(changes []change, deletes, renames []string) ([]change, []string, error) {
	consumed := map[string]bool{}

	for _, r := range renames {
		from, to, ok := strings.Cut(r, ":")
		if !ok {
			return nil, nil, fmt.Errorf("migrate:diff: --rename %q must be table.old:table.new", r)
		}

		ft, fc, ok1 := strings.Cut(from, ".")
		tt, tc, ok2 := strings.Cut(to, ".")

		if !ok1 || !ok2 || ft != tt {
			return nil, nil, fmt.Errorf("migrate:diff: --rename %q must be table.old:table.new on one table", r)
		}

		di := slices.IndexFunc(
			changes,
			func(c change) bool { return c.Kind == dropColumn && c.Table == ft && c.Column == fc },
		)
		ai := slices.IndexFunc(
			changes,
			func(c change) bool { return c.Kind == addColumn && c.Table == tt && c.Column == tc },
		)

		if di < 0 || ai < 0 {
			return nil, nil, fmt.Errorf("migrate:diff: --rename %s matches no pending drop and add", r)
		}

		changes[di] = change{Kind: renameColumn, Table: ft, Column: fc, To: tc}
		changes = slices.Delete(changes, ai, ai+1)
		consumed[r] = true
	}

	for _, d := range deletes {
		table, col, isCol := strings.Cut(d, ".")

		var matched bool

		for i := range changes {
			c := &changes[i]

			switch {
			case isCol && c.Kind == dropColumn && c.Table == table && c.Column == col:
				matched = true
				c.Note = "consented"
			case !isCol && c.Kind == warn && c.Table == table && c.Column == "" && strings.Contains(c.Note, "has no model"):
				matched = true
				*c = change{Kind: dropTable, Table: table, Note: "consented"}
			}
		}

		if !matched {
			return nil, nil, fmt.Errorf("migrate:diff: --delete %s matches no pending change", d)
		}
	}

	var missing []string

	for _, c := range changes {
		if c.Kind == dropColumn && c.Note != "consented" {
			missing = append(
				missing,
				fmt.Sprintf("  drop column %s.%-12s pass --delete %s.%s\n%sor   --rename %s.%s:%s.<new>",
					c.Table, c.Column, c.Table, c.Column, strings.Repeat(" ", 34), c.Table, c.Column, c.Table),
			)
		}
	}

	return changes, missing, nil
}

func migrateDiffCommand() Command {
	var (
		opts diffOptions
		name *string
		dir  *string
	)

	return CommandFunc{
		CommandName: "migrate:diff",
		Desc:        "Write a migration stub for the differences between the models and the database",
		DefineFlags: func(fs *flag.FlagSet) {
			name = fs.String("name", "sync_models", "suffix of the migration ID")
			dir = fs.String("o", "database/migrations", `output directory ("-" for stdout)`)
			fs.Var(&opts.deletes, "delete", "consent to dropping table or table.column (repeatable)")
			fs.Var(&opts.renames, "rename", "treat table.old:table.new as a rename (repeatable)")
			opts.check = fs.Bool("check", false, "write nothing; fail when anything differs")
		},
		Fn: func(ctx context.Context, app *App, _ []string) error {
			opts.name, opts.dir = *name, *dir

			return runMigrateDiff(ctx, app, opts)
		},
	}
}

type diffOptions struct {
	name, dir string
	deletes   multiFlag
	renames   multiFlag
	check     *bool
}

func runMigrateDiff(ctx context.Context, app *App, o diffOptions) error {
	if len(app.modelList) == 0 {
		return errors.New("migrate:diff: no models registered; pass vukem.WithModels(orm.ModelOf[T]()...)")
	}

	db, err := app.DB()
	if err != nil {
		return err
	}

	snap, err := schema.Inspect(ctx, db)
	if err != nil {
		return err
	}

	changes := diffModels(app.modelList, snap, frameworkTables())

	if *o.check {
		return diffCheck(app, changes)
	}

	changes, missing, err := applyConsent(changes, o.deletes, o.renames)
	if err != nil {
		return err
	}

	if len(missing) > 0 {
		return fmt.Errorf(
			"migrate:diff: consent required for %d destructive change(s):\n%s",
			len(missing),
			strings.Join(missing, "\n"),
		)
	}

	for _, c := range changes {
		if c.Kind == warn || (c.Kind == addColumn && c.Note != "") {
			fmt.Fprintln(app.stderr, "migrate:diff: warning:", c.Note)
		}
	}

	if !slices.ContainsFunc(changes, func(c change) bool { return c.Kind != warn }) {
		fmt.Fprintln(app.stdout, "No changes.")
		return nil
	}

	id := app.Now().UTC().Format("20060102150405") + "_" + o.name

	pkg := "migrations"
	if o.dir != "-" {
		pkg = filepath.Base(o.dir)
	}

	src, warnings, err := diffGo(pkg, id, changes, snap, db.Dialect())
	if err != nil {
		return err
	}

	for _, w := range warnings {
		fmt.Fprintln(app.stderr, "migrate:diff: warning:", w)
	}

	return writeMigration(app, o.dir, id, src)
}

// diffCheck is migrate:diff -check: nothing is written; any pending change
// fails.
func diffCheck(app *App, changes []change) error {
	pending := 0

	for _, c := range changes {
		if c.Kind == warn {
			fmt.Fprintln(app.stderr, "migrate:diff: warning:", c.Note)
			continue
		}

		pending++
	}

	if pending > 0 {
		return fmt.Errorf("migrate:diff: the models and the database differ (%d change(s))", pending)
	}

	fmt.Fprintln(app.stdout, "No changes.")

	return nil
}

// writeMigration writes src as dir/<id>.go, or to stdout for dir "-".
func writeMigration(app *App, dir, id string, src []byte) error {
	if dir == "-" {
		_, err := app.stdout.Write(src)
		return err
	}

	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("migrate:diff: %w", err)
	}

	path := filepath.Join(dir, id+".go")
	if err := createFile(path, src); err != nil {
		return fmt.Errorf("migrate:diff: %w", err)
	}

	fmt.Fprintln(app.stdout, "Wrote", path)
	fmt.Fprintf(app.stdout, "Register it: add %s() to database/migrations/registry.go\n", migrationFunc(id))

	return nil
}

// ------------------------------------------------------------------ migrate:check

type checkFinding struct {
	Kind    string `json:"kind"` // "conflict" or "order"
	ID      string `json:"id"`
	Table   string `json:"table,omitempty"`
	Column  string `json:"column,omitempty"`
	Message string `json:"message"`
}

func migrateCheckCommand() Command {
	var asJSON *bool

	return CommandFunc{
		CommandName: "migrate:check",
		Desc:        "Find migrations that collide, and pending ones older than the last that ran",
		DefineFlags: func(fs *flag.FlagSet) { asJSON = fs.Bool("json", false, "print JSON") },
		Fn: func(ctx context.Context, app *App, _ []string) error {
			findings, err := checkFindings(ctx, app)
			if err != nil {
				return err
			}

			if *asJSON {
				if findings == nil {
					findings = []checkFinding{}
				}

				if err := json.NewEncoder(app.stdout).Encode(findings); err != nil {
					return err
				}
			} else {
				printFindings(app, findings)
			}

			if len(findings) > 0 {
				return fmt.Errorf("migrate:check: %d problem(s) found", len(findings))
			}

			return nil
		},
	}
}

// checkFindings replays the migrations on a model of the schema and, when the
// database answers, adds the pending ones that are older than the last that ran.
func checkFindings(ctx context.Context, app *App) ([]checkFinding, error) {
	d := database.SQLite

	db, dbErr := app.DB()
	if dbErr == nil {
		d = db.Dialect()
	}

	conflicts, err := schema.FindConflicts(d, app.migrations)
	if err != nil {
		return nil, err
	}

	var findings []checkFinding

	for _, c := range conflicts {
		findings = append(
			findings,
			checkFinding{Kind: "conflict", ID: c.ID, Table: c.Table, Column: c.Column, Message: c.Message},
		)
	}

	if dbErr != nil {
		return findings, nil //nolint:nilerr // without a database only the static check applies
	}

	m, err := app.Migrator()
	if err != nil {
		return findings, nil //nolint:nilerr // without a migrator only the static check applies
	}

	st, err := m.Status(ctx)
	if err != nil {
		return findings, nil //nolint:nilerr // without the migrations table only the static check applies
	}

	last := ""

	for _, s := range st {
		if s.Ran && s.ID > last {
			last = s.ID
		}
	}

	for _, s := range st {
		if s.OutOfOrder {
			findings = append(
				findings,
				checkFinding{Kind: "order", ID: s.ID, Message: "pending, but " + last + " already ran"},
			)
		}
	}

	return findings, nil
}

func printFindings(app *App, findings []checkFinding) {
	if len(findings) == 0 {
		fmt.Fprintln(app.stdout, "No conflicts.")
		return
	}

	for _, f := range findings {
		target := f.Table
		if f.Column != "" {
			target += "." + f.Column
		}

		if f.Kind == "conflict" {
			fmt.Fprintf(app.stdout, "conflict  %s  %s  %s\n", f.ID, target, f.Message)
		} else {
			fmt.Fprintf(app.stdout, "order     %s  %s\n", f.ID, f.Message)
		}
	}
}

// errDestructiveMigration is returned by migrate in production for a pending
// migration that drops a table or a column.
var errDestructiveMigration = errors.New(
	"refusing to run a destructive migration in production without --allow-destructive",
)

// checkDestructive refuses pending migrations that drop data.
func checkDestructive(ctx context.Context, app *App, m *schema.Migrator) error {
	db, err := app.DB()
	if err != nil {
		return err
	}

	pending, err := m.Pending(ctx)
	if err != nil {
		return err
	}

	for _, mig := range pending {
		drops, err := schema.Destructive(ctx, db, mig)
		if err != nil {
			return err
		}

		if len(drops) > 0 {
			return fmt.Errorf("%w: %s would %s", errDestructiveMigration, mig.ID, strings.Join(drops, ", "))
		}
	}

	return nil
}
