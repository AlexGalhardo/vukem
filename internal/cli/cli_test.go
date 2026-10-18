package cli

import (
	"bytes"
	"context"
	"flag"
	"io/fs"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
)

var update = flag.Bool("update", false, "rewrite golden files")

var fixedNow = func() time.Time { return time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC) }

// vukem runs the CLI in dir and returns exit code, stdout and stderr.
func vukem(t *testing.T, dir string, args ...string) (int, string, string) {
	t.Helper()

	var out, errOut bytes.Buffer

	code := Run(t.Context(), args, Env{
		Dir: dir, Stdout: &out, Stderr: &errOut, Now: fixedNow, Version: "v0.0.0-test",
	})

	return code, out.String(), errOut.String()
}

func golden(t *testing.T, name, got string) {
	t.Helper()

	p := filepath.Join("testdata", "golden", filepath.FromSlash(name))
	if *update {
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			t.Fatal(err)
		}

		if err := os.WriteFile(p, []byte(got), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	want, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("%v (run with -update)", err)
	}

	if diff := cmp.Diff(strings.ReplaceAll(string(want), "\r\n", "\n"), got); diff != "" {
		t.Errorf("%s (-want +got):\n%s", name, diff)
	}
}

// project copies the fixture project into a temp dir.
func project(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	src := filepath.Join("testdata", "project")

	repo, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}

	err = filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}

		rel, _ := filepath.Rel(src, p)

		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}

		b = bytes.ReplaceAll(b, []byte("{{REPO}}"), []byte(filepath.ToSlash(repo)))
		b = bytes.ReplaceAll(b, []byte("\r\n"), []byte("\n"))

		dst := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(dst), 0o750); err != nil {
			return err
		}

		return os.WriteFile(dst, b, 0o600)
	})
	if err != nil {
		t.Fatal(err)
	}

	return dir
}

func read(t *testing.T, dir, rel string) string {
	t.Helper()

	b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}

	return string(b)
}

// requireFile fails unless rel exists inside dir.
func requireFile(t *testing.T, dir, rel string) {
	t.Helper()

	if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(rel))); err != nil {
		t.Errorf("%s: %v", rel, err)
	}
}

// requireNoFile fails when rel exists inside dir.
func requireNoFile(t *testing.T, dir, rel string) {
	t.Helper()

	if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(rel))); err == nil {
		t.Errorf("%s exists, want it absent", rel)
	}
}

func TestListHelpVersion(t *testing.T) {
	t.Parallel()

	code, out, _ := vukem(t, t.TempDir())
	if code != 0 {
		t.Fatalf("vukem = %d", code)
	}

	golden(t, "list.txt", out)

	if _, out2, _ := vukem(t, t.TempDir(), "list"); out2 != out {
		t.Error("list differs from no-arg output")
	}

	_, out, _ = vukem(t, t.TempDir(), "help", "make:model")
	golden(t, "help-make-model.txt", out)

	if code, out, _ := vukem(t, t.TempDir(), "make:model", "-h"); code != 0 || !strings.Contains(out, "-m") {
		t.Errorf("make:model -h = %d %q", code, out)
	}

	if _, out, _ := vukem(t, t.TempDir(), "help", "version"); !strings.Contains(out, "Usage: vukem version") {
		t.Errorf("help version = %q", out)
	}

	if _, out, _ := vukem(t, t.TempDir(), "--version"); out != "vukem v0.0.0-test\n" {
		t.Errorf("version = %q", out)
	}

	if v := version(&Env{}); v == "" {
		t.Error("version without override is empty")
	}
}

func TestUsageErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		args []string
		want string
	}{
		{[]string{"migrate"}, `unknown command "migrate"`},
		{[]string{"help", "nope"}, `unknown command "nope"`},
		{[]string{"make:model"}, "expected exactly one model name"},
		{[]string{"make:model", "-bogus", "Post"}, "flag provided but not defined"},
	}

	for _, tt := range tests {
		code, _, errOut := vukem(t, t.TempDir(), tt.args...)
		if code != 2 || !strings.Contains(errOut, tt.want) {
			t.Errorf("vukem %v = %d %q, want 2 and %q", tt.args, code, errOut, tt.want)
		}
	}

	if code, _, errOut := vukem(
		t,
		t.TempDir(),
		"make:model",
		"Post",
	); code != 1 ||
		!strings.Contains(errOut, "no go.mod") {
		t.Errorf("outside a project = %d %q", code, errOut)
	}

	for _, cmd := range []string{"serve", "build"} {
		if code, _, errOut := vukem(t, t.TempDir(), cmd); code != 1 || !strings.Contains(errOut, "no cmd/app") {
			t.Errorf("%s outside a project = %d %q", cmd, code, errOut)
		}
	}
}

func TestInterleave(t *testing.T) {
	t.Parallel()

	set := flag.NewFlagSet("x", flag.ContinueOnError)
	set.Bool("m", false, "")
	set.String("module", "", "")

	got := interleave(set, []string{"blog", "-m", "--module", "a/b", "--module=c", "--force", "--", "-x"})
	if diff := cmp.Diff([]string{"-m", "--module", "a/b", "--module=c", "--force", "blog", "-x"}, got); diff != "" {
		t.Fatal(diff)
	}

	if got := interleave(set, []string{"--module"}); len(got) != 1 {
		t.Fatalf("dangling value flag = %v", got)
	}
}

// generated lists every file the generators below create.
var generated = []string{
	"app/controllers/post_controller.go",
	"app/controllers/comment_controller.go",
	"app/controllers/photo_controller.go",
	"app/controllers/home_controller.go",
	"app/models/post.go",
	"database/factories/post_factory.go",
	"database/migrations/20261016120000_create_posts_table.go",
	"database/migrations/20261016120000_add_slug_to_posts_table.go",
	"database/migrations/20261016120000_backfill_data.go",
	"database/migrations/registry.go",
	"app/middleware/require_admin.go",
	"app/requests/store_post.go",
	"app/commands/send_reports.go",
	"bootstrap/commands.go",
	"app/providers/billing_provider.go",
	"bootstrap/providers.go",
	"tests/post_test.go",
	"app/resources/post.go",
	"app/models/comment.go",
	"database/factories/comment_factory.go",
	"database/seeders/users_seeder.go",
	"database/seeders/registry.go",
	"app/models/user.go",
	"app/policies/post_policy.go",
	"app/policies/registry.go",
	"app/mail/welcome.go",
	"resources/views/mail/welcome.html",
	"app/events/order_shipped.go",
	"app/listeners/notify_customer.go",
	"app/listeners/registry.go",
	"app/listeners/update_stock.go",
	"app/jobs/send_invoice.go",
	"app/jobs/registry.go",
}

func runGenerators(t *testing.T, dir string) {
	t.Helper()

	for _, args := range [][]string{
		{"make:model", "Post", "-m", "-f", "-c"},
		{"make:controller", "CommentController", "--resource"},
		{"make:controller", "photo", "--api"},
		{"make:controller", "Home"},
		{"make:migration", "add_slug_to_posts_table"},
		{"make:migration", "BackfillData"},
		{"make:middleware", "RequireAdmin"},
		{"make:request", "StorePost"},
		{"make:command", "SendReportsCommand"},
		{"make:provider", "Billing"},
		{"make:test", "Post"},
		{"make:resource", "PostResource"},
		{"make:model", "Comment"},
		{"make:factory", "CommentFactory"},
		{"make:seeder", "Users"},
		{"make:model", "User"},
		{"make:policy", "PostPolicy"},
		{"make:mail", "WelcomeMail"},
		{"make:event", "OrderShipped"},
		{"make:listener", "NotifyCustomer", "--event", "OrderShipped", "--async"},
		{"make:listener", "UpdateStock", "--event", "OrderShipped", "--queued"},
		{"make:job", "SendInvoiceJob"},
	} {
		if code, out, errOut := vukem(t, dir, args...); code != 0 {
			t.Fatalf("vukem %v = %d\n%s%s", args, code, out, errOut)
		}
	}
}

func TestGeneratorsGolden(t *testing.T) {
	t.Parallel()

	dir := project(t)
	runGenerators(t, dir)

	for _, rel := range generated {
		golden(t, "project/"+rel, read(t, dir, rel))
	}

	// Existing files are protected; --force overwrites.
	code, _, errOut := vukem(t, dir, "make:model", "Post")
	if code != 1 || !strings.Contains(errOut, "already exists") {
		t.Errorf("overwrite = %d %q", code, errOut)
	}

	if code, _, _ := vukem(t, dir, "make:model", "Post", "--force"); code != 0 {
		t.Error("--force refused")
	}

	// Registration is idempotent.
	before := read(t, dir, "bootstrap/providers.go")
	if code, out, _ := vukem(
		t,
		dir,
		"make:provider",
		"Billing",
		"--force",
	); code != 0 ||
		strings.Contains(out, "updated") {
		t.Errorf("re-register = %d %q", code, out)
	}

	if read(t, dir, "bootstrap/providers.go") != before {
		t.Error("provider registered twice")
	}
}

// Projects whose jobs.All predates WithJobsFunc keep getting handlers
// without the app, so make:job never breaks their build.
func TestMakeJobLegacyRegistry(t *testing.T) {
	t.Parallel()

	dir := project(t)
	legacy := "package jobs\n\nimport \"github.com/AlexGalhardo/vukem/queue\"\n\n" +
		"func All() []queue.Registration {\n\treturn []queue.Registration{}\n}\n"

	if err := os.WriteFile(filepath.Join(dir, "app", "jobs", "registry.go"), []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}

	if code, out, errOut := vukem(t, dir, "make:job", "Ping"); code != 0 {
		t.Fatalf("make:job = %d\n%s%s", code, out, errOut)
	}

	job := read(t, dir, "app/jobs/ping.go")
	if !strings.Contains(job, "func HandlePing(ctx context.Context, j Ping) error {") ||
		strings.Contains(job, "vukem") {
		t.Errorf("legacy job stub =\n%s", job)
	}

	if reg := read(t, dir, "app/jobs/registry.go"); !strings.Contains(reg, "queue.Handle(HandlePing),") {
		t.Errorf("legacy registry =\n%s", reg)
	}
}

func TestRegisterFallback(t *testing.T) {
	t.Parallel()

	dir := project(t)
	if err := os.WriteFile(
		filepath.Join(dir, "bootstrap", "commands.go"),
		[]byte("package bootstrap\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}

	if err := os.Remove(filepath.Join(dir, "bootstrap", "providers.go")); err != nil {
		t.Fatal(err)
	}

	code, out, _ := vukem(t, dir, "make:command", "Ping")
	if code != 0 || !strings.Contains(out, "add this to Commands():\n\n\tcommands.Ping(),") {
		t.Errorf("fallback = %d %q", code, out)
	}

	if code, out, _ := vukem(
		t,
		dir,
		"make:provider",
		"Mail",
	); code != 0 ||
		!strings.Contains(out, "providers.MailProvider{}") {
		t.Errorf("missing file fallback = %d %q", code, out)
	}
}

func TestLocalStubOverride(t *testing.T) {
	t.Parallel()

	dir := project(t)
	if err := os.MkdirAll(filepath.Join(dir, "stubs"), 0o750); err != nil {
		t.Fatal(err)
	}

	stub := "package middleware\n\n// {{.Name}} is custom.\nfunc {{.Name}}() {}\n"
	if err := os.WriteFile(filepath.Join(dir, "stubs", "middleware.go.tmpl"), []byte(stub), 0o600); err != nil {
		t.Fatal(err)
	}

	if code, _, errOut := vukem(t, dir, "make:middleware", "Audit"); code != 0 {
		t.Fatal(errOut)
	}

	if got := read(
		t,
		dir,
		"app/middleware/audit.go",
	); got != "package middleware\n\n// Audit is custom.\nfunc Audit() {}\n" {
		t.Errorf("override = %q", got)
	}

	for name, body := range map[string]string{
		"request.go.tmpl": "{{.Nope}}",
		"test.go.tmpl":    "{{",
		"factory.go.tmpl": "not go {{.Name}}",
	} {
		if err := os.WriteFile(filepath.Join(dir, "stubs", name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	for _, args := range [][]string{{"make:request", "A"}, {"make:test", "A"}, {"make:model", "A", "-f"}} {
		if code, _, errOut := vukem(t, dir, args...); code != 1 || !strings.Contains(errOut, "stub") {
			t.Errorf("vukem %v with a broken stub = %d %q", args, code, errOut)
		}
	}
}

func TestKeyGenerate(t *testing.T) {
	t.Parallel()

	dir := project(t)

	// .env is created from .env.example.
	if code, _, errOut := vukem(t, dir, "key:generate"); code != 0 {
		t.Fatal(errOut)
	}

	env := read(t, dir, ".env")
	if !strings.Contains(env, "APP_NAME=Blog\n") || !strings.Contains(env, "APP_KEY=base64:") {
		t.Fatalf(".env = %q", env)
	}

	// An existing key is protected.
	if code, _, errOut := vukem(t, dir, "key:generate"); code != 1 || !strings.Contains(errOut, "--force") {
		t.Errorf("overwrite = %d %q", code, errOut)
	}

	if code, _, _ := vukem(t, dir, "key:generate", "--force"); code != 0 || read(t, dir, ".env") == env {
		t.Error("--force did not replace the key")
	}

	if code, out, _ := vukem(t, dir, "key:generate", "--show"); code != 0 || !strings.HasPrefix(out, "base64:") {
		t.Errorf("--show = %q", out)
	}

	// No .env and no example: a new file; CRLF files keep working.
	empty := t.TempDir()
	if code, _, _ := vukem(
		t,
		empty,
		"key:generate",
	); code != 0 ||
		!strings.HasPrefix(read(t, empty, ".env"), "APP_KEY=base64:") {
		t.Error("new .env")
	}

	crlf := t.TempDir()
	if err := os.WriteFile(filepath.Join(crlf, ".env"), []byte("APP_ENV=dev\r\nAPP_KEY=\r\nX=1"), 0o600); err != nil {
		t.Fatal(err)
	}

	if code, _, _ := vukem(t, crlf, "key:generate"); code != 0 {
		t.Fatal("CRLF .env")
	}

	got := read(t, crlf, ".env")
	if !strings.Contains(got, "APP_KEY=base64:") || !strings.HasSuffix(got, "\r\nX=1") {
		t.Errorf("CRLF .env = %q", got)
	}

	noNewline := t.TempDir()
	if err := os.WriteFile(filepath.Join(noNewline, ".env"), []byte("A=1"), 0o600); err != nil {
		t.Fatal(err)
	}

	vukem(t, noNewline, "key:generate")

	if !strings.HasPrefix(read(t, noNewline, ".env"), "A=1\nAPP_KEY=") {
		t.Error("append without trailing newline")
	}
}

func TestSnapshot(t *testing.T) {
	t.Parallel()

	dir := project(t)

	a, err := takeSnapshot(dir)
	if err != nil {
		t.Fatal(err)
	}

	if a.changed(a) {
		t.Fatal("snapshot changed against itself")
	}

	// Ignored directories and non-Go files do not count.
	for _, p := range []string{"node_modules/x.go", ".vukem/y.go", "resources/views/home.html"} {
		full := filepath.Join(dir, filepath.FromSlash(p))
		_ = os.MkdirAll(filepath.Dir(full), 0o750)

		if err := os.WriteFile(full, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	b, _ := takeSnapshot(dir)
	if a.changed(b) {
		t.Fatal("ignored files triggered a rebuild")
	}

	if err := os.WriteFile(filepath.Join(dir, "app.go"), []byte("package x"), 0o600); err != nil {
		t.Fatal(err)
	}

	c, _ := takeSnapshot(dir)
	if !b.changed(c) {
		t.Fatal("new Go file not detected")
	}

	future := time.Now().Add(time.Hour)
	if err := os.Chtimes(filepath.Join(dir, "app.go"), future, future); err != nil {
		t.Fatal(err)
	}

	d, _ := takeSnapshot(dir)
	if !c.changed(d) {
		t.Fatal("modified Go file not detected")
	}

	if _, err := takeSnapshot(filepath.Join(dir, "missing")); err == nil {
		t.Fatal("missing dir")
	}
}

// e2e tests below compile real programs; they are skipped with -short.

func requireGo(t *testing.T) {
	t.Helper()

	if testing.Short() {
		t.Skip("compiles Go programs")
	}

	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go not on PATH")
	}
}

func TestGeneratedProjectBuilds(t *testing.T) {
	t.Parallel()
	requireGo(t)

	dir := project(t)

	repoSum, err := os.ReadFile(filepath.Join("..", "..", "go.sum"))
	if err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(dir, "go.sum"), repoSum, 0o600); err != nil {
		t.Fatal(err)
	}

	runGenerators(t, dir)

	for _, args := range [][]string{{"mod", "tidy"}, {"build", "./..."}, {"vet", "./..."}} {
		cmd := exec.CommandContext(t.Context(), "go", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=-mod=mod")

		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("go %v: %v\n%s", args, err, out)
		}
	}
}

// stdlibApp writes a project whose cmd/app needs only the standard library.
func stdlibApp(t *testing.T, main string) string {
	t.Helper()

	dir := t.TempDir()
	files := map[string]string{
		"go.mod":          "module example.com/tiny\n\ngo 1.27.0\n",
		"cmd/app/main.go": main,
	}

	for rel, body := range files {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		_ = os.MkdirAll(filepath.Dir(p), 0o750)

		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	return dir
}

const echoApp = `package main

import (
	"fmt"
	"os"
	"strings"
)

func main() {
	fmt.Println("app:", strings.Join(os.Args[1:], " "))
	if len(os.Args) > 1 && os.Args[1] == "fail" {
		os.Exit(3)
	}
}
`

func TestDelegationAndBuild(t *testing.T) {
	t.Parallel()
	requireGo(t)

	dir := stdlibApp(t, echoApp)

	code, out, errOut := vukem(t, dir, "migrate", "--force")
	if code != 0 || out != "app: migrate --force\n" {
		t.Fatalf("delegate = %d %q %q", code, out, errOut)
	}

	bin := filepath.Join(dir, cacheDir, "bin", exe("app"))

	first, err := os.Stat(bin)
	if err != nil {
		t.Fatal(err)
	}

	// The cached binary is reused while sources are unchanged.
	if code, _, _ := vukem(t, dir, "route:list"); code != 0 {
		t.Fatal("second delegation")
	}

	if again, _ := os.Stat(bin); !again.ModTime().Equal(first.ModTime()) {
		t.Error("binary rebuilt without changes")
	}

	// The app's exit code is passed through.
	if code, _, _ := vukem(t, dir, "fail"); code != 3 {
		t.Errorf("exit code = %d, want 3", code)
	}

	// Broken sources: the go build error is reported.
	if err := os.WriteFile(
		filepath.Join(dir, "cmd", "app", "broken.go"),
		[]byte("package main\nfunc x() {"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}

	if code, _, _ := vukem(t, dir, "migrate"); code != 1 {
		t.Errorf("broken build = %d", code)
	}

	if code, _, _ := vukem(t, dir, "build"); code != 1 {
		t.Errorf("broken vukem build = %d", code)
	}

	_ = os.Remove(filepath.Join(dir, "cmd", "app", "broken.go"))

	code, out, errOut = vukem(t, dir, "build")
	if code != 0 || !strings.Contains(out, "built") {
		t.Fatalf("build = %d %q %q", code, out, errOut)
	}

	got, err := exec.CommandContext(t.Context(), filepath.Join(dir, "bin", exe("app")), "hi").Output()
	if err != nil || string(got) != "app: hi\n" {
		t.Fatalf("built binary = %q %v", got, err)
	}
}

const httpApp = `package main

import (
	"net/http"
	"os"
)

func main() {
	http.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("v1")) })
	_ = http.ListenAndServe("127.0.0.1:"+os.Getenv("PORT"), nil)
}
`

func TestServeRestartsOnChange(t *testing.T) { //nolint:paralleltest // t.Setenv passes PORT to the app
	requireGo(t)

	dir := stdlibApp(t, httpApp)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()

	t.Setenv("PORT", strconv.Itoa(port))

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan int, 1)

	var out bytes.Buffer

	go func() {
		done <- Run(ctx, []string{"serve", "-poll", "100ms"}, Env{Dir: dir, Stdout: &syncWriter{w: &out}, Stderr: &syncWriter{w: &out}})
	}()

	waitFor(t, port, "v1", 60*time.Second)

	main := strings.Replace(httpApp, `"v1"`, `"v2"`, 1)
	if err := os.WriteFile(filepath.Join(dir, "cmd", "app", "main.go"), []byte(main), 0o600); err != nil {
		t.Fatal(err)
	}

	waitFor(t, port, "v2", 60*time.Second)

	cancel()

	if code := <-done; code != 0 {
		t.Fatalf("serve exited %d", code)
	}
}

func waitFor(t *testing.T, port int, want string, timeout time.Duration) {
	t.Helper()

	url := "http://127.0.0.1:" + strconv.Itoa(port) + "/"
	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, url, http.NoBody)

		if resp, err := http.DefaultClient.Do(req); err == nil {
			var b bytes.Buffer
			_, _ = b.ReadFrom(resp.Body)
			_ = resp.Body.Close()

			if b.String() == want {
				return
			}
		}

		time.Sleep(100 * time.Millisecond) //nolint:forbidigo // polling a real process
	}

	t.Fatalf("server never answered %q", want)
}

type syncWriter struct {
	mu sync.Mutex
	w  *bytes.Buffer
}

func (s *syncWriter) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.w.Write(p)
}

func TestDownUp(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	file := filepath.Join(dir, "storage", "framework", "down")

	code, out, errOut := vukem(t, dir, "down", "--with-secret", "--retry", "30s")
	if code != 0 || !strings.Contains(out, "Bypass: open /") {
		t.Fatalf("down = %d %q %q", code, out, errOut)
	}

	b, err := os.ReadFile(file)
	if err != nil || !strings.Contains(string(b), `"retry_after":30`) || !strings.Contains(string(b), "secret_hash") {
		t.Fatalf("down file = %s, %v", b, err)
	}

	secret := strings.Fields(strings.SplitAfter(out, "open /")[1])[0]
	if strings.Contains(string(b), secret) {
		t.Error("down file stores the secret in clear")
	}

	for range 2 { // idempotent
		if code, out, _ := vukem(t, dir, "up"); code != 0 || !strings.Contains(out, "live") {
			t.Fatalf("up = %d %q", code, out)
		}
	}

	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Errorf("down file still there: %v", err)
	}
}
