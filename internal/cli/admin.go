package cli

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"

	"github.com/AlexGalhardo/vukem/internal/naming"
)

func installAdmin(set *flag.FlagSet) func(context.Context, *Env, []string) error {
	force := set.Bool("force", false, "overwrite existing files")

	return func(ctx context.Context, env *Env, _ []string) error {
		if !exists(env, filepath.Join("app", "accounts", "accounts.go")) {
			return usagef("install:admin needs authentication: run vukem install:auth first")
		}

		if !exists(env, filepath.Join("routes", "web.go")) {
			return usagef("install:admin needs a web project (routes/web.go with sessions)")
		}

		data, err := scaffoldData(env)
		if err != nil {
			return err
		}

		if err := installScaffold(env, "admin", data, *force); err != nil {
			return err
		}

		regs := []registration{
			{"database/migrations/registry.go", "All", "m" + data["TS1"] + "CreateAdminAuditLogsTable()"},
			{"routes/registry.go", "All", "Admin"},
		}

		if err := appendEnvExample(env, []string{"ADMIN_EMAILS"}); err != nil {
			return err
		}

		if err := finishInstall(ctx, env, regs); err != nil {
			return err
		}

		fmt.Fprint(env.Stdout, "\nAdmin panel installed at /admin. Next:\n\n"+
			"  vukem migrate\n"+
			"  set ADMIN_EMAILS=you@example.com (comma-separated) in .env\n"+
			"  vukem make:admin-resource Post\n\n")

		return nil
	}
}

func makeAdminResource(set *flag.FlagSet) func(context.Context, *Env, []string) error {
	force := set.Bool("force", false, "overwrite existing files")

	return func(_ context.Context, env *Env, args []string) error {
		name, err := oneArg(args, "model name")
		if err != nil {
			return err
		}

		if !exists(env, filepath.Join("app", "admin", "registry.go")) {
			return usagef("make:admin-resource needs the admin panel: run vukem install:admin first")
		}

		d, err := newStubData(env, className(name, ""))
		if err != nil {
			return err
		}

		if !hasModel(env, d.Name) {
			return usagef("no models.%s in app/models (create it with vukem make:model %s)", d.Name, d.Name)
		}

		d.Slug = naming.Kebab(d.Plural)
		d.HasRequest = hasSchema(env, d.Name)

		if err := generate(env, "admin_resource.go.tmpl", "app/admin/"+d.Snake+".go", d, *force); err != nil {
			return err
		}

		if err := generate(
			env,
			"admin_resource_test.go.tmpl",
			"tests/admin_"+d.Snake+"_test.go",
			d,
			*force,
		); err != nil {
			return err
		}

		if err := register(env, "app/admin/registry.go", "Resources", d.Name+"()"); err != nil {
			return err
		}

		return register(env, "app/admin/registry.go", "Policies",
			"authz.For[*models."+d.Name+"]("+d.Name+"Policy{Admins: admins})",
			"github.com/AlexGalhardo/vukem/authz", d.Module+"/app/models")
	}
}

// hasModel reports whether app/models declares type name.
func hasModel(env *Env, name string) bool {
	return declares(filepath.Join(env.path("app"), "models"), `(?m)^type `+name+` struct`)
}

// hasSchema reports whether app/requests declares <Name>Schema, the
// starter's convention for a model's form input.
func hasSchema(env *Env, name string) bool {
	return declares(filepath.Join(env.path("app"), "requests"), `(?m)^var `+name+`Schema\b`)
}

func declares(dir, pattern string) bool {
	re := regexp.MustCompile(pattern)

	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}

	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".go" {
			continue
		}

		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err == nil && re.Match(b) {
			return true
		}
	}

	return false
}
