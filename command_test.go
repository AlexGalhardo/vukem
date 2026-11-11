package vukem

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"github.com/AlexGalhardo/vukem/auth/hash"
	"github.com/AlexGalhardo/vukem/database"
	"github.com/AlexGalhardo/vukem/loadtest"
	"github.com/AlexGalhardo/vukem/router"
	"github.com/AlexGalhardo/vukem/ssg"
	"github.com/AlexGalhardo/vukem/view"
	"github.com/AlexGalhardo/vukem/view/viewcheck"
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

func TestRunCommandSuggestsClosest(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		wantExact  string // the whole output when set
		wantPrefix string
		wantList   bool
	}{
		{name: "migrte", wantExact: "unknown command \"migrte\" (did you mean \"migrate\"?)\n"},
		{name: "route:lst", wantExact: "unknown command \"route:lst\" (did you mean \"route:list\"?)\n"},
		{name: "zzzzzz", wantPrefix: `unknown command "zzzzzz"`, wantList: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var out bytes.Buffer

			app := newTestApp(t, WithStdout(&out))

			err := runCommand(t.Context(), app, tt.name, nil)
			if !errors.Is(err, errUsage) {
				t.Fatalf("runCommand error = %v, want errUsage", err)
			}

			got := out.String()
			if tt.wantExact != "" && got != tt.wantExact {
				t.Errorf("output = %q, want %q", got, tt.wantExact)
			}

			if tt.wantPrefix != "" && !strings.HasPrefix(got, tt.wantPrefix) {
				t.Errorf("output = %q, want prefix %q", got, tt.wantPrefix)
			}

			if listed := strings.Count(got, "\n") > 2; listed != tt.wantList {
				t.Errorf("command list printed = %v, want %v:\n%s", listed, tt.wantList, got)
			}
		})
	}
}

type viewCheckData struct{ Title string }

func runViewCheck(t *testing.T, page string, decls []view.Decl, args ...string) (string, error) {
	t.Helper()

	var out bytes.Buffer

	fsys := fstest.MapFS{
		"layouts/app.html": {Data: []byte(`{{block "content" .}}{{end}}`)},
		"home.html":        {Data: []byte(page)},
	}

	build := func(opts ...Option) (*App, error) {
		opts = append(opts, WithLogger(discardLogger()), WithStdout(&out), WithViews(fsys))
		if decls != nil {
			opts = append(opts, WithViewPages(decls...))
		}

		return New(opts...)
	}

	err := RunMain(t.Context(), append([]string{"app", "view:check"}, args...), testSource(nil).Lookup, build)

	return out.String(), err
}

func TestViewCheckOK(t *testing.T) {
	t.Parallel()

	out, err := runViewCheck(t, `{{define "content"}}{{.Title}}{{end}}`,
		[]view.Decl{view.Of[viewCheckData]("home").Decl()})
	if err != nil || out != "" {
		t.Fatalf("view:check = %q, %v, want no output and no error", out, err)
	}
}

func TestViewCheckProblems(t *testing.T) {
	t.Parallel()

	out, err := runViewCheck(t, `{{define "content"}}{{.Titel}}{{end}}`,
		[]view.Decl{view.Of[viewCheckData]("home").Decl()})
	if err == nil || !strings.Contains(err.Error(), "1 problem(s) in 1 view(s)") {
		t.Fatalf("view:check error = %v, want 1 problem(s) in 1 view(s)", err)
	}

	want := `home.html:1:23: V002: field or method "Titel" not found in vukem.viewCheckData` + "\n"
	if out != want {
		t.Errorf("view:check output = %q, want %q", out, want)
	}
}

func TestViewCheckJSON(t *testing.T) {
	t.Parallel()

	out, err := runViewCheck(t, `{{define "content"}}{{.Titel}}{{end}}`,
		[]view.Decl{view.Of[viewCheckData]("home").Decl()}, "-json")
	if err == nil {
		t.Fatal("view:check -json = nil error, want the failure")
	}

	var problems []viewcheck.Problem
	if err := json.Unmarshal([]byte(out), &problems); err != nil {
		t.Fatalf("output %q is not JSON: %v", out, err)
	}

	if len(problems) != 1 || problems[0].Code != "V002" || problems[0].File != "home.html" {
		t.Errorf("problems = %+v", problems)
	}
}

func TestViewCheckNoTypedViews(t *testing.T) {
	t.Parallel()

	_, err := runViewCheck(t, `{{define "content"}}x{{end}}`, nil)
	if err == nil || !strings.Contains(err.Error(), "no typed views") {
		t.Fatalf("view:check without declarations = %v, want no typed views", err)
	}
}

func TestViewPagesNeedViews(t *testing.T) {
	t.Parallel()

	_, err := New(
		WithSource(testSource(nil)),
		WithLogger(discardLogger()),
		WithViewPages(view.Of[viewCheckData]("home").Decl()),
	)
	if err == nil || !strings.Contains(err.Error(), "WithViewPages needs WithViews") {
		t.Fatalf("New(WithViewPages without WithViews) = %v", err)
	}
}

func TestViewCheckMissingTargetIsAWarning(t *testing.T) {
	t.Parallel()

	out, err := runViewCheck(t, `{{define "content"}}<a hx-target="#nope">{{.Title}}</a>{{end}}`,
		[]view.Decl{view.Of[viewCheckData]("home").Decl()})
	if err != nil {
		t.Fatalf("view:check = %v, want a warning that does not fail", err)
	}

	if !strings.Contains(out, `V005: hx-target "#nope"`) || !strings.Contains(out, "warning: V005") {
		t.Errorf("output = %q, want the V005 warning", out)
	}

	if _, err := runViewCheck(t, `{{define "content"}}<a hx-target="#nope">{{.Title}}</a>{{end}}`,
		[]view.Decl{view.Of[viewCheckData]("home").Decl()}, "-strict"); err == nil {
		t.Error("view:check -strict = nil error, want the missing target to fail")
	}
}

type benchEnvCapture struct {
	mu          sync.Mutex
	databaseURL string
	ran         atomic.Int64
	setups      atomic.Int64
}

func (c *benchEnvCapture) scenario(name string, setupErr error) BenchScenario {
	var b BenchScenario

	b.Scenario = loadtest.Scenario{Name: name, Do: func(ctx context.Context, cl *loadtest.Client) error {
		c.ran.Add(1)

		_, err := cl.Get(ctx, "/ping")

		return err
	}}
	b.Setup = func(_ context.Context, app *App) error {
		c.setups.Add(1)

		cfg, err := configLoad[DatabaseConfig](app)
		if err != nil {
			return err
		}

		c.mu.Lock()
		c.databaseURL = cfg.URL
		c.mu.Unlock()

		return setupErr
	}

	return b
}

func (c *benchEnvCapture) tempDir() string {
	c.mu.Lock()
	defer c.mu.Unlock()

	return filepath.Dir(strings.TrimPrefix(c.databaseURL, "file:"))
}

// runBenchMain runs bench:run through RunMain with an app that has a /ping route and the given scenarios.
func runBenchMain(
	t *testing.T,
	env map[string]string,
	scenarios []BenchScenario,
	args ...string,
) (string, string, error) {
	t.Helper()

	var out, errOut bytes.Buffer

	getenv := func(k string) (string, bool) {
		if v, ok := env[k]; ok {
			return v, true
		}

		v, ok := map[string]string{"APP_KEY": testKey}[k]

		return v, ok
	}

	build := func(opts ...Option) (*App, error) {
		return New(append(opts,
			WithLogger(discardLogger()), WithStdout(&out), WithStderr(&errOut), WithBench(scenarios...),
			WithRoutes(func(r *router.Router, _ *App) error {
				r.Get("/ping", func(c *web.Ctx) error { return c.String(http.StatusOK, "pong") }).Name("ping")

				return nil
			}),
		)...)
	}

	err := RunMain(t.Context(), append([]string{"app", "bench:run"}, args...), getenv, build)

	return out.String(), errOut.String(), err
}

func TestBenchRunInProcess(t *testing.T) {
	t.Parallel()

	var c benchEnvCapture

	out, errOut, err := runBenchMain(t, nil, []BenchScenario{c.scenario("ping", nil)}, "-d", "1s", "-c", "2")
	if err != nil {
		t.Fatalf("err = %v, stderr %q", err, errOut)
	}

	if c.ran.Load() == 0 || c.setups.Load() != 1 || !strings.Contains(out, "http_reqs") ||
		!strings.Contains(out, "ping") {
		t.Fatalf("iterations=%d setups=%d out=%q", c.ran.Load(), c.setups.Load(), out)
	}
}

func TestSecurity_A05_BenchRunNeverUsesConfiguredDatabase(t *testing.T) {
	t.Parallel()

	configured := filepath.Join(t.TempDir(), "real.sqlite")
	if err := os.WriteFile(configured, []byte("not a real database, just bytes that must stay"), 0o600); err != nil {
		t.Fatal(err)
	}

	before, err := os.Stat(configured)
	if err != nil {
		t.Fatal(err)
	}

	var c benchEnvCapture

	env := map[string]string{"DATABASE_URL": "file:" + configured, "APP_ENV": "production"}

	if _, errOut, err := runBenchMain(
		t,
		env,
		[]BenchScenario{c.scenario("ping", nil)},
		"-d",
		"500ms",
		"-c",
		"1",
	); err != nil {
		t.Fatalf("err = %v, stderr %q", err, errOut)
	}

	after, err := os.Stat(configured)
	if err != nil || after.Size() != before.Size() || !after.ModTime().Equal(before.ModTime()) {
		t.Fatalf("the configured database was touched: %v %v %v", before, after, err)
	}

	c.mu.Lock()
	seen := c.databaseURL
	c.mu.Unlock()

	if seen == "file:"+configured || !strings.HasPrefix(seen, "file:"+filepath.ToSlash(os.TempDir())) {
		t.Fatalf("the app saw DATABASE_URL %q, want a file in a new temporary directory", seen)
	}
}

func TestBenchRunRemovesTempDir(t *testing.T) {
	t.Parallel()

	for _, setupErr := range []error{nil, errors.New("setup failed")} {
		var c benchEnvCapture

		_, _, err := runBenchMain(t, nil, []BenchScenario{c.scenario("ping", setupErr)}, "-d", "300ms", "-c", "1")
		if (err != nil) != (setupErr != nil) {
			t.Fatalf("setup error %v: RunMain err = %v", setupErr, err)
		}

		dir := c.tempDir()
		if dir == "." || dir == "" {
			t.Fatalf("no temporary directory was seen (%q)", c.databaseURL)
		}

		if _, statErr := os.Stat(dir); !os.IsNotExist(statErr) {
			t.Fatalf("the temporary directory %s survived (setup error %v): %v", dir, setupErr, statErr)
		}
	}
}

func TestBenchRunThresholdExit3(t *testing.T) {
	t.Parallel()

	var c benchEnvCapture

	_, errOut, err := runBenchMain(
		t,
		nil,
		[]BenchScenario{c.scenario("ping", nil)},
		"-d",
		"300ms",
		"-c",
		"1",
		"--threshold",
		"p95<1ns",
	)

	var exit *ExitError
	if !errors.As(err, &exit) || exit.Code != 3 || !strings.Contains(errOut, "FAIL") {
		t.Fatalf("err = %v, stderr %q, want an ExitError with code 3 and the table", err, errOut)
	}
}

func TestBenchRunCompareExit4(t *testing.T) {
	t.Parallel()

	baseline := filepath.Join(t.TempDir(), "baseline.json")
	fast := loadtest.Summary{
		SchemaVersion:   1,
		HTTPReqs:        10,
		RPS:             1e9,
		HTTPReqDuration: loadtest.Trend{P95: time.Nanosecond, P99: time.Nanosecond},
	}

	var b bytes.Buffer
	if err := loadtest.WriteJSON(&b, fast); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(baseline, b.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}

	var c benchEnvCapture

	_, _, err := runBenchMain(
		t,
		nil,
		[]BenchScenario{c.scenario("ping", nil)},
		"-d",
		"300ms",
		"-c",
		"1",
		"--compare",
		baseline,
	)

	var exit *ExitError
	if !errors.As(err, &exit) || exit.Code != 4 {
		t.Fatalf("err = %v, want an ExitError with code 4", err)
	}
}

func TestBenchRunSavesBaseline(t *testing.T) {
	t.Parallel()

	save := filepath.Join(t.TempDir(), "nested", "baseline.json")

	var c benchEnvCapture

	if _, _, err := runBenchMain(
		t,
		nil,
		[]BenchScenario{c.scenario("ping", nil)},
		"-d",
		"300ms",
		"-c",
		"1",
		"--save",
		save,
	); err != nil {
		t.Fatal(err)
	}

	f, err := os.Open(save)
	if err != nil {
		t.Fatal(err)
	}

	defer f.Close()

	if s, err := loadtest.ReadSummary(f); err != nil || s.HTTPReqs == 0 {
		t.Fatalf("saved summary = %+v, %v", s, err)
	}
}

func TestBenchRunListsScenarios(t *testing.T) {
	t.Parallel()

	var c benchEnvCapture

	out, _, err := runBenchMain(t, nil, []BenchScenario{c.scenario("alpha", nil), c.scenario("beta", nil)}, "--list")
	if err != nil || !strings.Contains(out, "alpha") || !strings.Contains(out, "beta") || c.setups.Load() != 0 {
		t.Fatalf(
			"out=%q err=%v setups=%d: --list must print the scenarios and start nothing",
			out,
			err,
			c.setups.Load(),
		)
	}
}

func TestBenchRunNoScenarios(t *testing.T) {
	t.Parallel()

	_, _, err := runBenchMain(t, nil, nil, "-d", "1s")
	if err == nil || !strings.Contains(err.Error(), "no scenarios registered") {
		t.Fatalf("err = %v", err)
	}
}

func TestBenchRunUsageErrors(t *testing.T) {
	t.Parallel()

	var c benchEnvCapture

	for _, args := range [][]string{
		{"--bogus"},
		{"--model", "sideways"},
		{"--threshold", "nonsense"},
		{"--stages", "x"},
		{"-c", "2", "--rate", "5/s"},
		{"--scenario", "missing"},
		{"--tolerance", "lots"},
	} {
		_, _, err := runBenchMain(t, nil, []BenchScenario{c.scenario("ping", nil)}, args...)
		if !errors.Is(err, errUsage) {
			t.Errorf("bench:run %v = %v, want a usage error", args, err)
		}
	}
}

func TestSecurity_A05_BenchRunRefusesDatabaseURLInProduction(t *testing.T) {
	t.Parallel()

	var c benchEnvCapture

	_, _, err := runBenchMain(t, map[string]string{"APP_ENV": "production"}, []BenchScenario{c.scenario("ping", nil)},
		"--database-url", "file:somewhere.sqlite", "-d", "1s")
	if !errors.Is(err, errUsage) || c.setups.Load() != 0 {
		t.Fatalf("err = %v setups=%d, want a usage error before anything starts", err, c.setups.Load())
	}
}

func TestBenchRunUsesAGivenDatabaseURL(t *testing.T) {
	t.Parallel()

	given := "file:" + filepath.Join(t.TempDir(), "disposable.sqlite")

	var c benchEnvCapture

	if _, errOut, err := runBenchMain(
		t,
		map[string]string{"APP_ENV": "test"},
		[]BenchScenario{c.scenario("ping", nil)},
		"--database-url",
		given,
		"-d",
		"300ms",
		"-c",
		"1",
	); err != nil {
		t.Fatalf("err = %v, stderr %q", err, errOut)
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.databaseURL != given {
		t.Fatalf("the app saw %q, want the --database-url %q", c.databaseURL, given)
	}
}

func TestExitCodeOf(t *testing.T) {
	t.Parallel()

	tests := []struct {
		err  error
		want int
	}{
		{nil, 0},
		{errors.New("boom"), 1},
		{errUsage, 2},
		{fmt.Errorf("wrapped: %w", errUsage), 2},
		{&ExitError{Code: 3, Err: errors.New("thresholds")}, 3},
		{fmt.Errorf("wrapped: %w", &ExitError{Code: 4}), 4},
		{&ExitError{Code: 0, Err: errors.New("no code")}, 1},
	}

	for _, tt := range tests {
		if got := exitCodeOf(tt.err); got != tt.want {
			t.Errorf("exitCodeOf(%v) = %d, want %d", tt.err, got, tt.want)
		}
	}

	e := &ExitError{Code: 3, Err: errors.New("inner")}
	if e.Error() != "inner" || !errors.Is(e, e.Err) {
		t.Fatalf("Error()=%q Unwrap mismatch", e.Error())
	}

	if (&ExitError{Code: 9}).Error() == "" {
		t.Fatal("an ExitError without a cause still needs a message")
	}
}

func TestSecurity_A05_BenchRunProductionCheckFailsClosed(t *testing.T) {
	t.Parallel()

	getenv := func(string) (string, bool) { return "", false }

	// An .env that cannot be read says nothing about the environment: it counts as production.
	broken := fstest.MapFS{".env": &fstest.MapFile{Data: []byte("APP_ENV=\"never closed\nOTHER=1\n")}}
	if !productionEnv(broken, getenv) {
		t.Fatal("an unreadable .env was taken for a development environment")
	}

	local := fstest.MapFS{".env": &fstest.MapFile{Data: []byte("APP_ENV=local\n")}}
	if productionEnv(local, getenv) {
		t.Fatal("APP_ENV=local was taken for production")
	}

	prod := fstest.MapFS{".env": &fstest.MapFile{Data: []byte("APP_ENV=production\n")}}
	if !productionEnv(prod, getenv) {
		t.Fatal("APP_ENV=production in .env was not taken for production")
	}
}

func TestSecurity_A05_BenchEnvIsolatesEveryService(t *testing.T) {
	t.Parallel()

	realEnv := map[string]string{
		"APP_KEY":            testKey,
		"APP_ENV":            "production",
		"DATABASE_URL":       "postgres://prod/db",
		"CACHE_STORE":        "redis",
		"QUEUE_CONNECTION":   "redis",
		"RATE_LIMIT_STORE":   "redis",
		"REDIS_URL":          "redis://prod:6379",
		"SESSION_DRIVER":     "database",
		"MAIL_MAILER":        "smtp",
		"S3_BUCKET":          "prod-bucket",
		"S3_ENDPOINT":        "https://s3.example.com",
		"TELEGRAM_BOT_TOKEN": "123:abc",
		"RESEND_API_KEY":     "re_live",
		"MAILTRAP_API_TOKEN": "mt_live",
		"LOG_LEVEL":          "debug",
	}

	env := benchEnv(
		func(k string) (string, bool) { v, ok := realEnv[k]; return v, ok },
		"file:/tmp/vukem-bench-1/bench.sqlite",
	)

	want := map[string]string{
		"APP_ENV":            "test",
		"DATABASE_URL":       "file:/tmp/vukem-bench-1/bench.sqlite",
		"CACHE_STORE":        "memory",
		"QUEUE_CONNECTION":   "sync",
		"RATE_LIMIT_STORE":   "memory",
		"SESSION_DRIVER":     "cookie",
		"MAIL_MAILER":        "array",
		"REDIS_URL":          "",
		"S3_BUCKET":          "",
		"S3_ENDPOINT":        "",
		"TELEGRAM_BOT_TOKEN": "",
		"RESEND_API_KEY":     "",
		"MAILTRAP_API_TOKEN": "",
		"LOG_LEVEL":          "debug",
		"APP_KEY":            testKey,
	}

	for k, w := range want {
		if got, ok := env(k); !ok || got != w {
			t.Errorf("%s = %q (set %v), want %q", k, got, ok, w)
		}
	}

	// A key the run does not manage still comes from the real environment.
	other := benchEnv(
		func(k string) (string, bool) { return map[string]string{"CUSTOM": "x"}[k], k == "CUSTOM" },
		"file:x",
	)
	if v, ok := other("CUSTOM"); !ok || v != "x" {
		t.Errorf("CUSTOM = %q, %v", v, ok)
	}

	if v, ok := other("LOG_LEVEL"); !ok || v != "error" {
		t.Errorf("LOG_LEVEL default = %q, %v, want error", v, ok)
	}

	if v, ok := other("APP_KEY"); !ok || v == "" {
		t.Error("no APP_KEY was generated")
	}
}

func TestBenchRunListsWithTheDisposableEnvironment(t *testing.T) {
	t.Parallel()

	var c benchEnvCapture

	// --list builds the app too: with the real redis URL in the environment it must not even see it.
	out, _, err := runBenchMain(
		t,
		map[string]string{"REDIS_URL": "redis://prod:6379", "DATABASE_URL": "postgres://prod/db"},
		[]BenchScenario{c.scenario("alpha", nil)},
		"--list",
	)
	if err != nil || !strings.Contains(out, "alpha") {
		t.Fatalf("out=%q err=%v", out, err)
	}
}

func TestBenchRunZeroTrafficFailsTheGate(t *testing.T) {
	t.Parallel()

	var b BenchScenario

	b.Scenario = loadtest.Scenario{Name: "idle", Do: func(context.Context, *loadtest.Client) error { return nil }}

	// A scenario that sends nothing: error_rate is 0 and would pass.
	_, errOut, err := runBenchMain(t, nil, []BenchScenario{b}, "-d", "300ms", "-c", "1", "--threshold", "error_rate<1%")

	var exit *ExitError
	if !errors.As(err, &exit) || exit.Code != 3 || !strings.Contains(errOut, "no request") {
		t.Fatalf("err = %v, stderr %q, want exit 3 and a clear message", err, errOut)
	}
}

func TestBenchRunSaveAndCompareTheSameFile(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "baseline.json")

	fast := loadtest.Summary{
		SchemaVersion:   1,
		HTTPReqs:        10,
		RPS:             1e9,
		HTTPReqDuration: loadtest.Trend{P95: time.Nanosecond, P99: time.Nanosecond},
	}

	var buf bytes.Buffer
	if err := loadtest.WriteJSON(&buf, fast); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}

	var c benchEnvCapture

	_, _, err := runBenchMain(
		t,
		nil,
		[]BenchScenario{c.scenario("ping", nil)},
		"-d",
		"300ms",
		"-c",
		"1",
		"--save",
		path,
		"--compare",
		path,
	)

	var exit *ExitError
	if !errors.As(err, &exit) || exit.Code != 4 {
		t.Fatalf("err = %v, want a regression against the file as it was before --save", err)
	}
}

func TestSecurity_A05_BenchRunDatabaseURLNeedsConfirmation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		url  string
		need bool
	}{
		{"file:" + filepath.ToSlash(filepath.Join(os.TempDir(), "x", "bench.sqlite")), false},
		{"sqlite:" + filepath.ToSlash(filepath.Join(os.TempDir(), "bench.sqlite")), false},
		{"file:storage/database.sqlite", true},
		{"file:../elsewhere.sqlite", true},
		{"postgres://user:pw@db.internal/app", true},
		{"mysql://root@localhost/app", true},
		{"file::memory:", true},
	}

	for _, tt := range tests {
		if got := databaseNeedsConfirmation(tt.url); got != tt.need {
			t.Errorf("databaseNeedsConfirmation(%q) = %v, want %v", tt.url, got, tt.need)
		}
	}

	var c benchEnvCapture

	_, _, err := runBenchMain(t, map[string]string{"APP_ENV": "test"}, []BenchScenario{c.scenario("ping", nil)},
		"--database-url", "postgres://user:pw@db.internal/app", "-d", "1s")
	if !errors.Is(err, errUsage) || c.setups.Load() != 0 {
		t.Fatalf("err = %v setups=%d, want a usage error before anything starts", err, c.setups.Load())
	}
}
