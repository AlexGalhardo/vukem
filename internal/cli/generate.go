package cli

import (
	"bytes"
	"context"
	"embed"
	"errors"
	"flag"
	"fmt"
	"go/format"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"text/template"

	"github.com/AlexGalhardo/golang-framework/internal/codemod"
	"github.com/AlexGalhardo/golang-framework/internal/naming"
)

//go:embed stubs
var stubFS embed.FS

// stubData is available to every stub as {{.Name}}, {{.Snake}}, ...
type stubData struct {
	Name      string // Pascal: "BlogPost"
	Snake     string // "blog_post"
	Kebab     string // "blog-post"
	Camel     string // "blogPost"
	Plural    string // "blog_posts"
	Table     string // table a migration targets
	Module    string // module path from go.mod
	Timestamp string // migration timestamp
	ID        string // migration ID ("<timestamp>_<name>")
	Func      string // generated function name, when the stub needs one
	Resource  bool
	API       bool
	Create    bool // migration creates Table
	Alter     bool // migration alters Table
}

func newStubData(env *Env, name string) (stubData, error) {
	mod, err := modulePath(env)
	if err != nil {
		return stubData{}, err
	}

	snake := naming.Snake(name)

	return stubData{
		Name:   naming.Pascal(name),
		Snake:  snake,
		Kebab:  naming.Kebab(name),
		Camel:  naming.Camel(name),
		Plural: naming.Plural(snake),
		Module: mod,
	}, nil
}

var moduleLine = regexp.MustCompile(`(?m)^module\s+(\S+)`)

func modulePath(env *Env) (string, error) {
	b, err := os.ReadFile(env.path("go.mod"))
	if err != nil {
		return "", fmt.Errorf("no go.mod here: run lumek inside a project (%w)", err)
	}

	m := moduleLine.FindSubmatch(b)
	if m == nil {
		return "", errors.New("go.mod has no module line")
	}

	return string(m[1]), nil
}

// renderStub executes a stub, preferring the project's stubs/<name> over
// the embedded one, and gofmt's Go output.
func renderStub(env *Env, name string, data stubData) ([]byte, error) {
	src, err := os.ReadFile(env.path(filepath.Join("stubs", name)))
	if errors.Is(err, fs.ErrNotExist) {
		src, err = stubFS.ReadFile(path.Join("stubs", name))
	}

	if err != nil {
		return nil, fmt.Errorf("stub %s: %w", name, err)
	}

	t, err := template.New(name).Option("missingkey=error").Parse(string(src))
	if err != nil {
		return nil, fmt.Errorf("stub %s: %w", name, err)
	}

	var b bytes.Buffer
	if err := t.Execute(&b, data); err != nil {
		return nil, fmt.Errorf("stub %s: %w", name, err)
	}

	if !strings.HasSuffix(name, ".go.tmpl") {
		return b.Bytes(), nil
	}

	out, err := format.Source(b.Bytes())
	if err != nil {
		return nil, fmt.Errorf("stub %s produced invalid Go: %w", name, err)
	}

	return out, nil
}

// create writes a new project file; existing files need force.
func create(env *Env, rel string, content []byte, force bool) error {
	p := env.path(filepath.FromSlash(rel))

	if _, err := os.Stat(p); err == nil && !force {
		return fmt.Errorf("%s already exists (use --force to overwrite)", rel)
	}

	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		return err
	}

	if err := writeFile(p, content); err != nil {
		return err
	}

	fmt.Fprintln(env.Stdout, "created", rel)

	return nil
}

func generate(env *Env, stub, rel string, data stubData, force bool) error {
	out, err := renderStub(env, stub, data)
	if err != nil {
		return err
	}

	return create(env, rel, out, force)
}

// register appends expr to the list returned by funcName in rel. When the
// file was restructured it prints the snippet instead of failing.
func register(env *Env, rel, funcName, expr string, imports ...string) error {
	p := env.path(filepath.FromSlash(rel))

	src, err := os.ReadFile(p)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}

	var out []byte
	if err == nil {
		out, err = codemod.AppendToReturn(src, funcName, expr, imports...)
	}

	if err != nil {
		fmt.Fprintf(
			env.Stdout,
			"could not update %s automatically; add this to %s():\n\n\t%s,\n\n",
			rel,
			funcName,
			expr,
		)
		return nil //nolint:nilerr // the generated file is fine; registration is manual
	}

	if bytes.Equal(out, src) {
		return nil
	}

	if err := writeFile(p, out); err != nil {
		return err
	}

	fmt.Fprintln(env.Stdout, "updated", rel)

	return nil
}

// className strips a conventional suffix ("PostController" → "Post").
func className(name, suffix string) string {
	return naming.Pascal(strings.TrimSuffix(naming.Pascal(name), suffix))
}

func makeController(set *flag.FlagSet) func(context.Context, *Env, []string) error {
	resource := set.Bool("resource", false, "add the seven resource actions")
	api := set.Bool("api", false, "add the five JSON API resource actions")
	force := set.Bool("force", false, "overwrite existing files")

	return func(_ context.Context, env *Env, args []string) error {
		name, err := oneArg(args, "controller name")
		if err != nil {
			return err
		}

		return controller(env, className(name, "Controller"), *resource, *api, *force)
	}
}

func controller(env *Env, name string, resource, api, force bool) error {
	d, err := newStubData(env, name)
	if err != nil {
		return err
	}

	d.Resource, d.API = resource || api, api

	return generate(env, "controller.go.tmpl", "app/controllers/"+d.Snake+"_controller.go", d, force)
}

func makeModel(set *flag.FlagSet) func(context.Context, *Env, []string) error {
	migration := set.Bool("m", false, "also create a create_<table>_table migration")
	factory := set.Bool("f", false, "also create a factory")
	ctrl := set.Bool("c", false, "also create a resource controller")
	force := set.Bool("force", false, "overwrite existing files")

	return func(_ context.Context, env *Env, args []string) error {
		name, err := oneArg(args, "model name")
		if err != nil {
			return err
		}

		d, err := newStubData(env, className(name, ""))
		if err != nil {
			return err
		}

		if err := generate(env, "model.go.tmpl", "app/models/"+d.Snake+".go", d, *force); err != nil {
			return err
		}

		if *migration {
			if err := newMigration(env, "create_"+d.Plural+"_table", *force); err != nil {
				return err
			}
		}

		if *factory {
			if err := generate(
				env,
				"factory.go.tmpl",
				"database/factories/"+d.Snake+"_factory.go",
				d,
				*force,
			); err != nil {
				return err
			}
		}

		if *ctrl {
			return controller(env, d.Name, true, false, *force)
		}

		return nil
	}
}

var (
	createTable = regexp.MustCompile(`^create_(\w+?)_table$`)
	alterTable  = regexp.MustCompile(`^(?:\w+_(?:to|from|in|on)_|alter_|change_|update_)(\w+?)_table$`)
)

func makeMigration(set *flag.FlagSet) func(context.Context, *Env, []string) error {
	force := set.Bool("force", false, "overwrite existing files")

	return func(_ context.Context, env *Env, args []string) error {
		name, err := oneArg(args, "migration name")
		if err != nil {
			return err
		}

		return newMigration(env, naming.Snake(name), *force)
	}
}

func newMigration(env *Env, name string, force bool) error {
	d, err := newStubData(env, name)
	if err != nil {
		return err
	}

	d.Timestamp = env.Now().UTC().Format("20060102150405")
	d.ID = d.Timestamp + "_" + name
	d.Func = "m" + d.Timestamp + naming.Pascal(name)

	if m := createTable.FindStringSubmatch(name); m != nil {
		d.Create, d.Table = true, m[1]
	} else if m := alterTable.FindStringSubmatch(name); m != nil {
		d.Alter, d.Table = true, m[1]
	}

	if err := generate(env, "migration.go.tmpl", "database/migrations/"+d.ID+".go", d, force); err != nil {
		return err
	}

	return register(env, "database/migrations/registry.go", "All", d.Func+"()")
}

func makeMiddleware(set *flag.FlagSet) func(context.Context, *Env, []string) error {
	return simple(set, "middleware name", "middleware.go.tmpl", "app/middleware/%s.go", "")
}

func makeRequest(set *flag.FlagSet) func(context.Context, *Env, []string) error {
	return simple(set, "request name", "request.go.tmpl", "app/requests/%s.go", "")
}

func makeTest(set *flag.FlagSet) func(context.Context, *Env, []string) error {
	return simple(set, "test name", "test.go.tmpl", "tests/%s_test.go", "Test")
}

func makeCommand(set *flag.FlagSet) func(context.Context, *Env, []string) error {
	run := simple(set, "command name", "command.go.tmpl", "app/commands/%s.go", "Command")

	return func(ctx context.Context, env *Env, args []string) error {
		if err := run(ctx, env, args); err != nil {
			return err
		}

		d, err := newStubData(env, className(args[0], "Command"))
		if err != nil {
			return err
		}

		return register(env, "bootstrap/commands.go", "Commands", "commands."+d.Name+"()", d.Module+"/app/commands")
	}
}

func makeProvider(set *flag.FlagSet) func(context.Context, *Env, []string) error {
	run := simple(set, "provider name", "provider.go.tmpl", "app/providers/%s_provider.go", "Provider")

	return func(ctx context.Context, env *Env, args []string) error {
		if err := run(ctx, env, args); err != nil {
			return err
		}

		d, err := newStubData(env, className(args[0], "Provider"))
		if err != nil {
			return err
		}

		return register(env, "bootstrap/providers.go", "Providers", "providers."+d.Name+"Provider{}",
			d.Module+"/app/providers")
	}
}

// simple builds a one-file generator; suffix is stripped from the name.
func simple(set *flag.FlagSet, what, stub, pattern, suffix string) func(context.Context, *Env, []string) error {
	force := set.Bool("force", false, "overwrite existing files")

	return func(_ context.Context, env *Env, args []string) error {
		name, err := oneArg(args, what)
		if err != nil {
			return err
		}

		d, err := newStubData(env, className(name, suffix))
		if err != nil {
			return err
		}

		return generate(env, stub, fmt.Sprintf(pattern, d.Snake), d, *force)
	}
}

// writeFile writes a file of the project the CLI runs in.
func writeFile(p string, b []byte) error {
	return os.WriteFile(p, b, 0o600) //nolint:gosec // G703: paths come from the developer's own command line
}
