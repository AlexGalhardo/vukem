package cli

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"go/format"
	"os"
	"path/filepath"
	"slices"
)

func installFlags(set *flag.FlagSet) func(context.Context, *Env, []string) error {
	force := set.Bool("force", false, "overwrite existing files")

	return func(ctx context.Context, env *Env, _ []string) error {
		if !exists(env, filepath.Join("bootstrap", "app.go")) {
			return usagef("no bootstrap/app.go: run vukem install:flags inside a Vukem project")
		}

		if !exists(env, filepath.Join("database", "migrations", "registry.go")) {
			return usagef("install:flags stores flag values in the database (database/migrations/registry.go)")
		}

		data, err := scaffoldData(env)
		if err != nil {
			return err
		}

		if err := installScaffold(env, "flags", data, *force); err != nil {
			return err
		}

		if err := registerFlags(env, data["Module"]); err != nil {
			return err
		}

		regs := []registration{
			{"database/migrations/registry.go", "All", "m" + data["TS1"] + "CreateFeatureFlagsTable()"},
		}
		if err := finishInstall(ctx, env, regs); err != nil {
			return err
		}

		fmt.Fprint(env.Stdout, "\nFeature flags installed. Next:\n\n"+
			"  vukem migrate\n"+
			"  declare flags in app/features/features.go and list them in All()\n"+
			"  with the admin panel, toggle them at /admin/_flags\n\n")

		return nil
	}
}

// registerFlags adds vukem.WithFlags(features.All()...) to bootstrap/app.go.
func registerFlags(env *Env, module string) error {
	p := env.path(filepath.Join("bootstrap", "app.go"))

	src, err := os.ReadFile(p)
	if err != nil {
		return err
	}

	if bytes.Contains(src, []byte("vukem.WithFlags(")) {
		return nil
	}

	const anchor = "\t\tvukem.WithProviders(Providers()...),\n"

	src = bytes.Replace(src, []byte(anchor), []byte(anchor+"\t\tvukem.WithFlags(features.All()...),\n"), 1)
	// Join the project's import group; format.Source sorts it.
	imp := []byte("\t\"" + module + "/app/features\"\n")
	if i := bytes.Index(src, []byte("\t\""+module+"/")); i >= 0 {
		src = slices.Concat(src[:i], imp, src[i:])
	} else {
		src = bytes.Replace(src, []byte("vukem \"github.com/AlexGalhardo/vukem\"\n"),
			slices.Concat([]byte("vukem \"github.com/AlexGalhardo/vukem\"\n\n"), imp), 1)
	}

	if !bytes.Contains(src, []byte("vukem.WithFlags(")) {
		return errors.New("bootstrap/app.go: no vukem.WithProviders(Providers()...) line to add vukem.WithFlags after")
	}

	if src, err = format.Source(src); err != nil {
		return fmt.Errorf("bootstrap/app.go: %w", err)
	}

	if err := writeFile(p, src); err != nil {
		return err
	}

	fmt.Fprintln(env.Stdout, "updated bootstrap/app.go")

	return nil
}
