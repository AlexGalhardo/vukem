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
)

// inertiaReplaced lists the HTMX starter files an Inertia project drops;
// the inertia scaffolds rewrite the controllers, routes and tests.
var inertiaReplaced = []string{
	"resources/views/home.html", "resources/views/layouts", "resources/views/partials", "resources/views/posts",
	"resources/public/js", "resources/js/app.ts",
	"resources/views/components", "resources/views/uploads", "app/controllers/upload_controller.go",
	"tests/uploads_test.go",
}

func installInertia(set *flag.FlagSet) func(context.Context, *Env, []string) error {
	react := set.Bool("react", false, "React pages (default)")
	vue := set.Bool("vue", false, "Vue pages")
	svelte := set.Bool("svelte", false, "Svelte pages")
	force := set.Bool("force", false, "replace the HTMX starter pages, controllers and tests")
	noInstall := set.Bool("no-install", false, "skip installing frontend dependencies")

	return func(ctx context.Context, env *Env, _ []string) error {
		fw := "react"

		switch n := countTrue(*react, *vue, *svelte); {
		case n > 1:
			return usagef("choose one of --react, --vue or --svelte")
		case *vue:
			fw = "vue"
		case *svelte:
			fw = "svelte"
		}

		if !exists(env, filepath.Join("bootstrap", "app.go")) {
			return usagef("no bootstrap/app.go: run vukem install:inertia inside a Vukem project")
		}

		if !*force {
			return usagef(
				"install:inertia replaces the starter's HTMX pages, controllers and tests; run it with --force",
			)
		}

		if err := applyInertia(env, fw); err != nil {
			return err
		}

		if err := tool(ctx, env, "go", "mod", "tidy"); err != nil {
			return err
		}

		pm, err := packageManager(env.Dir, "")
		if err != nil {
			return err
		}

		jsInstall(ctx, env, pm, *noInstall)
		fmt.Fprintf(env.Stdout, "Inertia (%s) installed: pages in resources/js/pages. Run vukem serve.\n", fw)

		return nil
	}
}

// inertiaFramework reports the Inertia client adapter of the project in
// env ("react", "vue", "svelte"), or "" for an HTMX project.
func inertiaFramework(env *Env) string {
	pkg, err := os.ReadFile(env.path("package.json"))
	if err != nil {
		return ""
	}

	for fw, adapter := range map[string]string{
		"react": "@inertiajs/react", "vue": "@inertiajs/vue3", "svelte": "@inertiajs/svelte",
	} {
		if bytes.Contains(pkg, []byte(`"`+adapter+`"`)) {
			return fw
		}
	}

	return ""
}

// authScaffolds are the install:auth pages for the project's frontend.
func authScaffolds(env *Env) []string {
	if fw := inertiaFramework(env); fw != "" {
		return []string{"auth-inertia", "auth-inertia-" + fw}
	}

	return []string{"auth-web"}
}

func countTrue(bs ...bool) int {
	n := 0

	for _, b := range bs {
		if b {
			n++
		}
	}

	return n
}

// applyInertia turns the HTMX starter in env.Dir into an Inertia app with
// framework ("react", "vue" or "svelte") pages.
func applyInertia(env *Env, framework string) error {
	app := env.path(filepath.Join("bootstrap", "app.go"))

	src, err := os.ReadFile(app)
	if err != nil {
		return err
	}

	if bytes.Contains(src, []byte("WithInertia")) {
		return usagef("this project already uses Inertia")
	}

	for _, rel := range inertiaReplaced {
		if err := os.RemoveAll(env.path(filepath.FromSlash(rel))); err != nil {
			return err
		}
	}

	data, err := scaffoldData(env)
	if err != nil {
		return err
	}

	for _, name := range []string{"inertia", "inertia-" + framework} {
		if err := installScaffold(env, name, data, true); err != nil {
			return err
		}
	}

	src = bytes.Replace(src, []byte("\t\tvukem.WithVite(resources.Public()),\n"),
		[]byte("\t\tvukem.WithVite(resources.Public()),\n\t\tvukem.WithInertia(inertia.Config{}),\n"), 1)
	src = bytes.Replace(src, []byte("vukem \"github.com/AlexGalhardo/vukem\"\n"),
		[]byte("vukem \"github.com/AlexGalhardo/vukem\"\n\t\"github.com/AlexGalhardo/vukem/inertia\"\n"), 1)

	if !bytes.Contains(src, []byte("vukem.WithInertia(")) {
		return errors.New("bootstrap/app.go: no vukem.WithVite(resources.Public()) line to add Inertia after")
	}

	if src, err = format.Source(src); err != nil {
		return fmt.Errorf("bootstrap/app.go: %w", err)
	}

	return writeFile(app, src)
}
