package vukem

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/AlexGalhardo/vukem/auth/hash"
	"github.com/AlexGalhardo/vukem/database"
	"github.com/AlexGalhardo/vukem/router"
	"github.com/AlexGalhardo/vukem/ssg"
	"github.com/AlexGalhardo/vukem/web"
)

type echoCommand struct{}

func (echoCommand) Name() string        { return "test:echo" }
func (echoCommand) Description() string { return "echo stdin" }
func (echoCommand) Flags(*flag.FlagSet) {}
func (echoCommand) Run(_ context.Context, app *App, args []string) error {
	in, err := io.ReadAll(app.Stdin())
	if err != nil {
		return err
	}

	fmt.Fprintf(app.Stdout(), "out:%s:%s", strings.Join(args, ","), in)
	fmt.Fprint(app.Stderr(), "err:diag")

	return nil
}

func TestCommandWriters(t *testing.T) {
	t.Parallel()

	var out, errOut bytes.Buffer

	app := newTestApp(t, WithStdout(&out), WithStderr(&errOut), WithStdin(strings.NewReader("in")),
		WithProviders(ProviderFunc(func(app *App) error { app.AddCommand(echoCommand{}); return nil })))

	if err := runCommand(t.Context(), app, "test:echo", []string{"a", "b"}); err != nil {
		t.Fatal(err)
	}

	if got, want := out.String(), "out:a,b:in"; got != want {
		t.Errorf("command stdout = %q, want %q", got, want)
	}

	if got, want := errOut.String(), "err:diag"; got != want {
		t.Errorf("command stderr = %q, want %q", got, want)
	}
}

func TestCommandWritersDefaults(t *testing.T) {
	t.Parallel()

	app := newTestApp(t)
	if app.Stdout() != os.Stdout || app.Stderr() != os.Stderr || app.Stdin() != os.Stdin {
		t.Errorf("Stdout/Stderr/Stdin = %v, %v, %v, want the process streams", app.Stdout(), app.Stderr(), app.Stdin())
	}
}

func TestResealCommand(t *testing.T) {
	t.Parallel()

	var gotBatch int

	ok := func(n int) ResealFunc {
		return func(_ context.Context, db database.Executor, batch int) (int, error) {
			if db == nil {
				t.Error("ResealFunc got a nil db")
			}

			gotBatch = batch

			return n, nil
		}
	}
	failing := func(context.Context, database.Executor, int) (int, error) { return 1, errors.New("boom") }

	var out bytes.Buffer

	app := newTestApp(
		t,
		WithSource(testSource(map[string]string{"DATABASE_URL": ":memory:"})),
		WithStdout(&out),
		WithProviders(ProviderFunc(func(app *App) error {
			app.AddCommand(ResealCommand(ok(2), ok(3)))
			return nil
		})),
	)

	if err := runCommand(t.Context(), app, "crypt:reseal", []string{"-batch", "50"}); err != nil {
		t.Fatal(err)
	}

	if out.String() != "re-sealed 5 values\n" || gotBatch != 50 {
		t.Errorf("crypt:reseal = %q (batch %d), want %q (batch 50)", out.String(), gotBatch, "re-sealed 5 values\n")
	}

	out.Reset()

	bad := newTestApp(
		t,
		WithSource(testSource(map[string]string{"DATABASE_URL": ":memory:"})),
		WithStdout(&out),
		WithProviders(ProviderFunc(func(app *App) error {
			app.AddCommand(ResealCommand(ok(2), failing, ok(3)))
			return nil
		})),
	)

	if err := runCommand(
		t.Context(),
		bad,
		"crypt:reseal",
		nil,
	); err == nil ||
		!strings.Contains(out.String(), "3 values before") {
		t.Errorf("crypt:reseal with a failing model = %v, output %q", err, out.String())
	}
}

func runHashMake(t *testing.T, stdin string, args ...string) (string, error) {
	t.Helper()

	var out bytes.Buffer

	app := newTestApp(t, WithStdout(&out), WithStdin(strings.NewReader(stdin)))
	cmd := hashMakeCommand()
	fset := flag.NewFlagSet("hash:make", flag.ContinueOnError)
	cmd.Flags(fset)

	if err := fset.Parse(args); err != nil {
		t.Fatal(err)
	}

	err := cmd.Run(t.Context(), app, fset.Args())

	return strings.TrimSpace(out.String()), err
}

func TestHashMakeReadsStdinAndNeverArgv(t *testing.T) {
	t.Parallel()

	out, err := runHashMake(t, "s3cret-pw\r\nignored\n", "-stdin")
	if err != nil {
		t.Fatal(err)
	}

	if ok, err := hash.Default().Verify(out, "s3cret-pw"); err != nil || !ok {
		t.Fatalf("Verify(%q, password) = %v, %v, want true (the line ending is trimmed)", out, ok, err)
	}

	if strings.Contains(out, "s3cret-pw") {
		t.Fatal("the plain password was printed")
	}

	if _, err := runHashMake(t, "", "-stdin", "positional-password"); !errors.Is(err, errUsage) {
		t.Fatalf("a positional argument = %v, want errUsage", err)
	}

	if _, err := runHashMake(t, "", "-stdin"); err == nil {
		t.Fatal("an empty stdin was accepted")
	}
}

func TestHashMakeReadsTheEnvironment(t *testing.T) {
	t.Setenv("VUKEM_TEST_HASH_PW", "from-env")

	out, err := runHashMake(t, "", "-env", "VUKEM_TEST_HASH_PW")
	if err != nil {
		t.Fatal(err)
	}

	if ok, err := hash.Default().Verify(out, "from-env"); err != nil || !ok {
		t.Fatalf("Verify(%q, password) = %v, %v, want true", out, ok, err)
	}

	if _, err := runHashMake(t, "", "-env", "VUKEM_TEST_HASH_UNSET"); err == nil {
		t.Fatal("an unset variable was accepted")
	}
}

func TestSSGGenerateCommand(t *testing.T) {
	secret := false

	var buf bytes.Buffer

	app := newTestApp(t, WithStdout(&buf), WithRoutes(func(r *router.Router, _ *App) error {
		r.Get("/", func(c *web.Ctx) error {
			if !ssg.FromContext(c.Context()) {
				return c.String(http.StatusInternalServerError, "live")
			}

			return c.String(http.StatusOK, "home")
		})
		r.Get("/about", func(c *web.Ctx) error {
			return c.String(http.StatusOK, "about-page")
		})
		r.Get("/secret", func(c *web.Ctx) error {
			secret = true

			return c.String(http.StatusOK, "secret")
		})

		return nil
	}))

	parent := t.TempDir()
	dir := filepath.Join(parent, "dist")

	if err := app.Call(
		t.Context(),
		"ssg:generate",
		[]string{"-out", dir, "-path", "/", "-path", "/about"},
	); err != nil {
		t.Fatal(err)
	}

	home, err := os.ReadFile(filepath.Join(dir, "index.html"))
	if err != nil {
		t.Fatal(err)
	}

	if string(home) != "home" {
		t.Fatalf("index.html = %q", home)
	}

	about, err := os.ReadFile(filepath.Join(dir, "about", "index.html"))
	if err != nil {
		t.Fatal(err)
	}

	if string(about) != "about-page" {
		t.Fatalf("about = %q", about)
	}

	if secret {
		t.Fatal("ssg:generate crawled /secret")
	}

	if _, err := os.Stat(filepath.Join(dir, "secret", "index.html")); !os.IsNotExist(err) {
		t.Fatalf("secret file: %v", err)
	}

	if !strings.Contains(buf.String(), "wrote index.html") ||
		!strings.Contains(buf.String(), "wrote about/index.html") {
		t.Fatalf("stdout = %q", buf.String())
	}

	if err := app.Call(t.Context(), "ssg:generate", []string{"-out", dir}); !errors.Is(err, errUsage) {
		t.Fatalf("missing -path = %v, want usage error", err)
	}

	if err := app.Call(
		t.Context(),
		"ssg:generate",
		[]string{"-out", dir, "-path", "/", "extra"},
	); !errors.Is(
		err,
		errUsage,
	) {
		t.Fatalf("extra arg = %v, want usage error", err)
	}

	for _, p := range []string{"../secret", "https://example.com/x", "/../secret"} {
		buf.Reset()

		err := app.Call(t.Context(), "ssg:generate", []string{"-out", dir, "-path", p})
		if err == nil {
			t.Fatalf("path %q was accepted", p)
		}

		if _, statErr := os.Stat(filepath.Join(parent, "secret")); !os.IsNotExist(statErr) {
			t.Fatalf("path %q wrote %s: %v", p, filepath.Join(parent, "secret"), statErr)
		}

		if _, statErr := os.Stat(filepath.Join(parent, "x")); !os.IsNotExist(statErr) {
			t.Fatalf("path %q wrote outside: %v", p, statErr)
		}
	}
}

func TestSSGGenerateNoCleanFlag(t *testing.T) {
	app := newTestApp(t, WithRoutes(func(r *router.Router, _ *App) error {
		r.Get("/a", func(c *web.Ctx) error { return c.String(http.StatusOK, "a") })
		r.Get("/b", func(c *web.Ctx) error { return c.String(http.StatusOK, "b") })

		return nil
	}))

	for _, noClean := range []bool{false, true} {
		dir := filepath.Join(t.TempDir(), "dist")
		extra := []string{}

		if noClean {
			extra = []string{"-no-clean"}
		}

		first := append([]string{"-out", dir, "-path", "/a", "-path", "/b"}, extra...)
		second := append([]string{"-out", dir, "-path", "/a"}, extra...)

		for _, args := range [][]string{first, second} {
			if err := app.Call(t.Context(), "ssg:generate", args); err != nil {
				t.Fatal(err)
			}
		}

		_, err := os.Stat(filepath.Join(dir, "b", "index.html"))
		if kept := err == nil; kept != noClean {
			t.Errorf("noClean=%v: page b kept = %v", noClean, kept)
		}
	}
}

type summaryDoc string

func (s summaryDoc) RouteSummary() string { return string(s) }

func TestRouteListSummaryAndFilters(t *testing.T) {
	t.Parallel()

	withSummaries := func(r *router.Router, _ *App) error {
		ok := func(*web.Ctx) error { return nil }
		r.Get("/api/tasks", ok).Name("api.tasks.index").Doc(summaryDoc("List tasks"))
		r.Post("/api/tasks", ok).Name("api.tasks.store").Doc(summaryDoc("Create a task"))
		r.Get("/posts", ok).Name("posts.index")
		r.Any("/hook", ok).Name("hook")

		return nil
	}
	noSummaries := func(r *router.Router, _ *App) error {
		r.Get("/posts", func(*web.Ctx) error { return nil }).Name("posts.index")

		return nil
	}

	tests := []struct {
		name        string
		routes      func(*router.Router, *App) error
		args        []string
		want        []string // substrings the output must contain
		notWant     []string
		wantSummary bool
	}{
		{
			name: "summary column", routes: withSummaries,
			want: []string{"SUMMARY", "Create a task", "List tasks", "/posts"}, wantSummary: true,
		},
		{
			name: "method filter ignores case", routes: withSummaries, args: []string{"--method", "post"},
			want: []string{"Create a task"}, notWant: []string{"List tasks", "/posts"}, wantSummary: true,
		},
		{
			name: "ANY matches Any routes", routes: withSummaries, args: []string{"--method", "any"},
			want: []string{"/hook"}, notWant: []string{"/api/tasks", "/posts"}, wantSummary: false,
		},
		{
			name: "path filter", routes: withSummaries, args: []string{"--path", "/api"},
			want: []string{"/api/tasks"}, notWant: []string{"/posts", "/hook"}, wantSummary: true,
		},
		{
			name: "name filter", routes: withSummaries, args: []string{"--name", "posts."},
			want: []string{"posts.index"}, notWant: []string{"api.tasks"}, wantSummary: false,
		},
		{
			name: "filters combine", routes: withSummaries, args: []string{"--method", "GET", "--path", "/api"},
			want: []string{"List tasks"}, notWant: []string{"Create a task", "/posts"}, wantSummary: true,
		},
		{
			name: "no match", routes: withSummaries, args: []string{"--name", "nothing"},
			want: []string{"No routes match."}, notWant: []string{"METHOD"},
		},
		{
			name: "no summaries drops the column", routes: noSummaries,
			want: []string{"METHOD", "/posts"}, wantSummary: false,
		},
		{
			name: "json honours filters", routes: withSummaries, args: []string{"--json", "--method", "GET"},
			want: []string{`"path": "/api/tasks"`, `"path": "/posts"`}, notWant: []string{`"method": "POST"`},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var out bytes.Buffer

			app := newTestApp(t, WithStdout(&out), WithRoutes(tt.routes))

			if err := app.Call(t.Context(), "route:list", tt.args); err != nil {
				t.Fatal(err)
			}

			got := out.String()
			for _, w := range tt.want {
				if !strings.Contains(got, w) {
					t.Errorf("output lacks %q:\n%s", w, got)
				}
			}

			for _, n := range tt.notWant {
				if strings.Contains(got, n) {
					t.Errorf("output has %q:\n%s", n, got)
				}
			}

			if !slices.Contains(tt.args, "--json") && !strings.Contains(got, "No routes match.") {
				if has := strings.Contains(got, "SUMMARY"); has != tt.wantSummary {
					t.Errorf("SUMMARY column present = %v, want %v:\n%s", has, tt.wantSummary, got)
				}
			}
		})
	}
}
