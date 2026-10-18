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
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"text/template"

	"github.com/AlexGalhardo/vukem/config"
)

// starterFS is the HTMX + SQLite starter. Files ending in .tmpl are
// text/template stubs (the suffix is dropped); everything else, including
// html/template views, is copied verbatim.
//
//go:embed all:starter
var starterFS embed.FS

// The starter vendors htmx so apps work offline and under a strict CSP.
// TestVendoredHTMX fails if the file changes without updating the hash.
const (
	htmxVersion = "4.0.0"
	htmxSHA256  = "e484d9171a9db30a39c8f16e3d709d4137f3211c659f8e6125816635033d593f"
	htmxPath    = "resources/public/js/htmx.min.js"
)

const frameworkModule = "github.com/AlexGalhardo/vukem"

var projectName = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_.-]*$`)

type starterData struct {
	Name   string // directory and APP_NAME
	Module string // Go module path
}

func newProject(set *flag.FlagSet) func(context.Context, *Env, []string) error {
	module := set.String("module", "", "Go module path (default: the project name)")
	framework := set.String("framework", "", "path to a local Vukem checkout to use instead of a release")
	apiOnly := set.Bool("api", false, "JSON API starter: tokens, /api/v1, no views or sessions")
	inertiaFW := set.String("inertia", "", "Inertia starter with react, vue or svelte")
	templ := set.Bool("templ", false, "HTMX starter with templ components instead of html/template views")
	minimal := set.Bool("minimal", false, "HTMX starter without a database: no posts, uploads or migrations, no SQLite")
	noInstall := set.Bool("no-install", false, "skip installing frontend dependencies")
	pmFlag := set.String("pm", "", "JavaScript package manager: npm, bun, pnpm or yarn (default npm)")
	standards := set.Bool("standards", false,
		"markdownlint and a lint:md script, a Keep a Changelog CHANGELOG.md with cliff.toml, "+
			"Conventional Commits git hooks and a SemVer version")
	license := set.String("license", "", "write LICENSE, CONTRIBUTING.md, CODE_OF_CONDUCT.md and SECURITY.md (mit)")
	ciFlag := set.Bool("ci", false, "GitHub Actions CI workflow (format, vet, test, govulncheck, image) and Dependabot")
	deploy := set.String("deploy", "", "platform deploy configuration: railway (.railway/railway.ts)")
	compose := set.Bool("docker", false, "compose.yaml running the app, PostgreSQL and a mail catcher locally")

	return func(ctx context.Context, env *Env, args []string) error {
		name, err := oneArg(args, "project name")
		if err != nil {
			return err
		}

		if !projectName.MatchString(name) {
			return usagef("invalid project name %q (letters, digits, '-', '_' and '.')", name)
		}

		std := standardsOpts{
			standards: *standards,
			license:   strings.ToLower(strings.TrimSpace(*license)),
			ci:        *ciFlag,
			deploy:    strings.ToLower(strings.TrimSpace(*deploy)),
			docker:    *compose,
		}
		if err := checkStandards(std); err != nil {
			return err
		}

		dir := env.path(name)
		if _, err := os.Stat(dir); err == nil {
			return fmt.Errorf("%s already exists", name)
		}

		st := stack{kind: "minimal"}
		if *minimal {
			if *apiOnly || *templ || *inertiaFW != "" {
				return usagef("--minimal is an HTMX starter: drop --api, --templ and --inertia")
			}
		} else if st, err = chooseStack(env, *apiOnly, *templ, *inertiaFW); err != nil {
			return err
		}

		pm, err := packageManager(dir, *pmFlag)
		if err != nil {
			return err
		}

		data := starterData{Name: name, Module: *module}
		if data.Module == "" {
			data.Module = name
		}

		if err := writeStarter(dir, data); err != nil {
			return err
		}

		project := *env
		project.Dir = dir

		switch st.kind {
		case "api":
			if err := makeAPIStarter(&project, data); err != nil {
				return err
			}
		case "inertia":
			if err := applyInertia(&project, st.framework); err != nil {
				return err
			}
		case "templ":
			if err := applyTempl(&project); err != nil {
				return err
			}
		case "minimal":
			if err := makeMinimalStarter(&project); err != nil {
				return err
			}
		}

		if err := writeDocker(dir, st.kind, pm); err != nil {
			return err
		}

		// Before writeAppKey: it copies .env.example to .env, so the
		// Compose block has to be in place already.
		if err := writeStandards(&project, newStandardsData(data, st.kind, std, env.Now().Year()), std); err != nil {
			return err
		}

		if err := requireFramework(ctx, &project, *framework); err != nil {
			return err
		}

		if err := addGovulncheck(ctx, &project); err != nil {
			return err
		}

		if st.kind == "templ" {
			if err := addTempl(ctx, &project); err != nil {
				return err
			}
		}

		if err := tool(ctx, &project, "go", "mod", "tidy"); err != nil {
			return err
		}

		if st.kind == "templ" {
			if err := templGenerate(ctx, &project); err != nil {
				return err
			}
		}

		if err := writeAppKey(&project, config.GenerateKey(), false); err != nil {
			return err
		}

		if _, err := exec.LookPath("git"); err == nil {
			cmd := exec.CommandContext(ctx, "git", "init", "--quiet")
			cmd.Dir = dir

			if out, err := cmd.CombinedOutput(); err != nil {
				fmt.Fprintf(env.Stderr, "git init failed (continuing): %v\n%s", err, out)
			} else {
				installHooksPath(ctx, &project)
			}
		}

		if st.kind != "api" {
			jsInstall(ctx, &project, pm, *noInstall)
		}

		desc := "htmx " + htmxVersion + ", Tailwind, SQLite"

		switch st.kind {
		case "api":
			desc = "JSON API, SQLite"
		case "inertia":
			desc = "Inertia + " + st.framework + ", Tailwind, SQLite"
		case "templ":
			desc = "htmx " + htmxVersion + ", templ, Tailwind, SQLite"
		case "minimal":
			desc = "htmx " + htmxVersion + ", Tailwind, no database"
		}

		migrate := "  vukem migrate\n"
		if st.kind == "minimal" {
			migrate = ""
		}

		fmt.Fprintf(env.Stdout, "\nCreated %s (%s). Next:\n\n  cd %s\n%s  vukem serve\n\n", name, desc, name, migrate)

		return nil
	}
}

// jsInstall installs the frontend dependencies with pm when it is
// available; a failure only warns, since the Go app works without built
// assets in development.
func jsInstall(ctx context.Context, env *Env, pm jsPM, skip bool) {
	if skip {
		return
	}

	if _, err := exec.LookPath(pm.binary()); err != nil {
		fmt.Fprintf(env.Stdout, "%s not found: install it, then run %s\n", pm.name, pm.install())
		return
	}

	if err := tool(ctx, env, pm.binary(), pm.installArgs...); err != nil {
		fmt.Fprintf(env.Stderr, "%s failed (run it again later): %v\n", pm.install(), err)
	}
}

// writeStarter renders the embedded starter into dir.
func writeStarter(dir string, data starterData) error {
	return fs.WalkDir(starterFS, "starter", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}

		rel := strings.TrimPrefix(p, "starter/")
		if rel == "gitignore" {
			rel = ".gitignore" // stored without the dot so it does not apply to this repository
		}

		content, err := starterFS.ReadFile(p)
		if err != nil {
			return err
		}

		if stub, ok := strings.CutSuffix(rel, ".tmpl"); ok {
			rel = stub

			if content, err = renderStarterFile(rel, content, data); err != nil {
				return err
			}
		}

		dst := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(dst), 0o750); err != nil {
			return err
		}

		return writeFile(dst, content)
	})
}

func renderStarterFile(rel string, src []byte, data starterData) ([]byte, error) {
	t, err := template.New(rel).Option("missingkey=error").Parse(string(src))
	if err != nil {
		return nil, fmt.Errorf("starter %s: %w", rel, err)
	}

	var b bytes.Buffer
	if err := t.Execute(&b, data); err != nil {
		return nil, fmt.Errorf("starter %s: %w", rel, err)
	}

	if !strings.HasSuffix(rel, ".go") {
		return b.Bytes(), nil
	}

	out, err := format.Source(b.Bytes())
	if err != nil {
		return nil, fmt.Errorf("starter %s: %w", rel, err)
	}

	return out, nil
}

// requireFramework points the new project at Vukem: a local checkout via a
// replace directive, or the release matching this CLI (latest for dev
// builds).
func requireFramework(ctx context.Context, env *Env, local string) error {
	if local != "" {
		abs, err := filepath.Abs(local)
		if err != nil {
			return err
		}

		f, err := os.OpenFile(env.path("go.mod"), os.O_APPEND|os.O_WRONLY, 0)
		if err != nil {
			return err
		}

		_, err = fmt.Fprintf(f, "\nrequire %s v0.0.0\n\nreplace %s => %s\n", frameworkModule, frameworkModule,
			filepath.ToSlash(abs))

		return errors.Join(err, f.Close())
	}

	return tool(ctx, env, "go", "get", frameworkModule+"@"+releaseVersion(version(env)))
}

// releaseVersion returns v when it is a tagged release, "latest" for
// development builds (devel, pseudo-versions, +dirty).
func releaseVersion(v string) string {
	if !strings.HasPrefix(v, "v") || strings.Contains(v, "-0.") || strings.Contains(v, "+") {
		return "latest"
	}

	return v
}

// apiStarterRemove lists the HTMX starter files an API project drops.
var apiStarterRemove = []string{
	"resources/views/home.html", "resources/views/layouts", "resources/views/partials", "resources/views/posts",
	"resources/public", "app/controllers/home.go", "app/controllers/post_controller.go", "routes/web.go",
	"tests/home_test.go", "package.json", "vite.config.ts", "resources/css", "resources/js", "resources/vite",
	"resources/views/components", "resources/views/uploads", "app/controllers/upload_controller.go",
	"tests/uploads_test.go",
}

// makeAPIStarter turns the starter into a JSON API: the api-starter
// overlay, the authentication core and the API token routes.
func makeAPIStarter(env *Env, sd starterData) error {
	for _, rel := range apiStarterRemove {
		if err := os.RemoveAll(env.path(filepath.FromSlash(rel))); err != nil {
			return err
		}
	}

	// No frontend assets: drop Vite from the bootstrap.
	app := env.path(filepath.Join("bootstrap", "app.go"))

	src, err := os.ReadFile(app)
	if err != nil {
		return err
	}

	if err := writeFile(
		app,
		bytes.ReplaceAll(src, []byte("\t\tvukem.WithVite(resources.Public()),\n"), nil),
	); err != nil {
		return err
	}

	data, err := scaffoldData(env)
	if err != nil {
		return err
	}

	data["Name"], data["Module"] = sd.Name, sd.Module

	for _, name := range []string{"api-starter", "auth-core", "api"} {
		if err := installScaffold(env, name, data, true); err != nil {
			return err
		}
	}

	for _, reg := range []registration{
		{"database/migrations/registry.go", "All", "m" + data["TS1"] + "CreateUsersTable()"},
		{"database/migrations/registry.go", "All", "m" + data["TS2"] + "CreateAuthTables()"},
	} {
		if err := register(env, reg.file, reg.fn, reg.expr); err != nil {
			return err
		}
	}

	return appendEnvExample(env, []string{"CORS_ALLOWED_ORIGINS"})
}
