package cli

import (
	"context"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"slices"
	"strings"
)

// stubPublish copies embedded make:* stubs into the project's stubs/ directory,
// where renderStub prefers them over the embedded copy.
func stubPublish(set *flag.FlagSet) func(context.Context, *Env, []string) error {
	force := set.Bool("force", false, "overwrite stubs that already exist in stubs/")

	return func(_ context.Context, env *Env, args []string) error {
		if _, err := modulePath(env); err != nil {
			return err
		}

		all, err := stubNames()
		if err != nil {
			return err
		}

		names, err := selectStubs(all, args)
		if err != nil {
			return err
		}

		// Check every target first so a refusal never leaves a half-published set.
		if !*force {
			if err := refuseExistingStubs(env, names); err != nil {
				return err
			}
		}

		for _, n := range names {
			content, err := stubFS.ReadFile("stubs/" + n)
			if err != nil {
				return err
			}

			if err := create(env, "stubs/"+n, content, *force); err != nil {
				return err
			}
		}

		return nil
	}
}

func selectStubs(all, args []string) ([]string, error) {
	if len(args) == 0 {
		return all, nil
	}

	var names []string

	for _, a := range args {
		n, err := resolveStub(all, a)
		if err != nil {
			return nil, err
		}

		if !slices.Contains(names, n) {
			names = append(names, n)
		}
	}

	return names, nil
}

func refuseExistingStubs(env *Env, names []string) error {
	for _, n := range names {
		if _, err := os.Stat(env.path("stubs/" + n)); err == nil {
			return fmt.Errorf("stubs/%s already exists (use --force to overwrite)", n)
		}
	}

	return nil
}

// stubNames lists the embedded stub file names, sorted.
func stubNames() ([]string, error) {
	entries, err := fs.ReadDir(stubFS, "stubs")
	if err != nil {
		return nil, err
	}

	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}

	return names, nil
}

// resolveStub maps controller, controller.go or controller.go.tmpl to the
// stub file name: an exact name, then name+".tmpl", then the only file that
// starts with name+".".
func resolveStub(all []string, name string) (string, error) {
	if slices.Contains(all, name) {
		return name, nil
	}

	if slices.Contains(all, name+".tmpl") {
		return name + ".tmpl", nil
	}

	var matches []string

	for _, n := range all {
		if strings.HasPrefix(n, name+".") {
			matches = append(matches, n)
		}
	}

	if len(matches) == 1 {
		return matches[0], nil
	}

	return "", usagef("stub %q is unknown or ambiguous; valid names: %s", name, strings.Join(all, ", "))
}
