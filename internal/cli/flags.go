package cli

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"go/format"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

func installFlags(set *flag.FlagSet) func(context.Context, *Env, []string) error {
	force := set.Bool("force", false, "rewrite the feature flags migration (app/features is always kept)")

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

		ts, err := writeFlagsFiles(env, data, *force)
		if err != nil {
			return err
		}

		if err := registerFlags(env, data["Module"]); err != nil {
			return err
		}

		regs := []registration{
			{"database/migrations/registry.go", "All", "m" + ts + "CreateFeatureFlagsTable()"},
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

const flagsMigrationSuffix = "_create_feature_flags_table.go"

// writeFlagsFiles writes the install:flags files so that running it again
// is safe: app/features/features.go is never overwritten (it holds the
// app's flags, even with --force), and an existing feature flags migration
// is reused instead of adding a second one (--force rewrites it in place).
// It returns the migration's timestamp.
func writeFlagsFiles(env *Env, data map[string]string, force bool) (string, error) {
	const features = "app/features/features.go"

	if exists(env, filepath.FromSlash(features)) {
		fmt.Fprintln(env.Stdout, "kept", features)
	} else if err := writeFlagsFile(env, features, "app/features/features.go.tmpl", data); err != nil {
		return "", err
	}

	ts := data["TS1"]

	matches, err := filepath.Glob(
		filepath.Join(env.path(filepath.Join("database", "migrations")), "*"+flagsMigrationSuffix),
	)
	if err != nil {
		return "", err
	}

	if len(matches) > 0 {
		ts = strings.TrimSuffix(filepath.Base(matches[0]), flagsMigrationSuffix)
		rel := "database/migrations/" + filepath.Base(matches[0])

		if !force {
			fmt.Fprintln(env.Stdout, "kept", rel)
			return ts, nil
		}
	}

	d := maps.Clone(data)
	d["TS1"] = ts

	return ts, writeFlagsFile(env, "database/migrations/"+ts+flagsMigrationSuffix,
		"database/migrations/TS1_create_feature_flags_table.go.tmpl", d)
}

func writeFlagsFile(env *Env, rel, tmpl string, data map[string]string) error {
	src, err := scaffoldFS.ReadFile("scaffolds/flags/" + tmpl)
	if err != nil {
		return err
	}

	out, err := renderScaffold(rel, src, data)
	if err != nil {
		return err
	}

	dst := env.path(filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(dst), 0o750); err != nil {
		return err
	}

	if err := writeFile(dst, out); err != nil {
		return err
	}

	fmt.Fprintln(env.Stdout, "created", rel)

	return nil
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
