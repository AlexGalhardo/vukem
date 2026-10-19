// Command modcheck gates the nested modules on being resolvable the way a
// consumer resolves them: from their own go.mod, with no workspace.
//
// go.work makes every module in the repository resolve the core module from
// the checkout, so a nested go.mod can require a core version that predates
// a package its own code imports and nothing here notices. The module then
// builds for everyone working in this repository and for nobody fetching it
// from the proxy. drivers/postgres shipped exactly that: it required core
// v0.20.0 while its integration tests imported vukem/flags, added in
// v0.27.0.
//
// For each drivers/* and contrib/* module, modcheck runs "go vet" in the
// module directory with GOWORK=off, so the module's own requirements are the
// only thing that can satisfy its imports. Test files count: the postgres
// defect was in one, and a build-tagged one at that, which is why the vet
// runs with every custom build tag the module's own files declare. Without
// -tags integration the defect is invisible even with GOWORK=off.
//
// It also fails when a module is missing from go.work, because a module
// outside the workspace is not built by anything run from the root.
//
// Run from anywhere inside the repository:
//
//	go run ./internal/tools/modcheck
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	err := run(ctx, os.Args[1:], os.Stdout)

	stop()

	if err != nil {
		fmt.Fprintln(os.Stderr, "modcheck:", err)
		os.Exit(1)
	}
}

// errUnresolvable reports that at least one module failed the gate.
var errUnresolvable = errors.New("a nested module cannot be resolved from its own go.mod")

func run(ctx context.Context, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("modcheck", flag.ContinueOnError)
	fs.SetOutput(out)

	if err := fs.Parse(args); err != nil {
		return err
	}

	root, err := repoRoot()
	if err != nil {
		return err
	}

	mods, err := discoverModules(root)
	if err != nil {
		return err
	}

	if len(mods) == 0 {
		return errors.New("no drivers/* or contrib/* module found; run inside the repository")
	}

	used, err := workspaceModules(root)
	if err != nil {
		return err
	}

	failed := false

	for _, dir := range mods {
		// examples/* are deliberately outside the workspace: they exist to
		// resolve the way a consumer does, so membership is not required of
		// them. The GOWORK=off resolution check below still applies, and for
		// an example it is the only check that matters.
		if !used[dir] && !strings.HasPrefix(dir, "examples/") {
			failed = true

			// GitHub Actions renders ::error lines as annotations.
			fmt.Fprintf(out, "::error title=modcheck::%s is not in go.work, so nothing here builds it\n", dir)

			continue
		}

		tags, err := customTags(root, dir)
		if err != nil {
			return err
		}

		vetOut, err := vetWithoutWorkspace(ctx, root, dir, tags)
		if err == nil {
			fmt.Fprintf(out, "%s: resolves from its own go.mod%s\n", dir, tagNote(tags))
			continue
		}

		failed = true

		fmt.Fprintf(out, "::error title=modcheck::%s does not resolve from its own go.mod%s\n", dir, tagNote(tags))
		fmt.Fprintf(out, "%s\n", strings.TrimRight(vetOut, "\n"))
		fmt.Fprintf(out,
			"  fix: cd %s && GOWORK=off go get github.com/AlexGalhardo/vukem@<release with those packages>"+
				" && GOWORK=off go mod tidy\n",
			dir,
		)
	}

	if failed {
		return errUnresolvable
	}

	return nil
}

// tagNote names the build tags a check ran with, for the one-line report.
func tagNote(tags []string) string {
	if len(tags) == 0 {
		return ""
	}

	return " (-tags " + strings.Join(tags, ",") + ")"
}
