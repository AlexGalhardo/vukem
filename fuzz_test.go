package vukem

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/AlexGalhardo/vukem/router"
)

type fakeFuzzRunner struct {
	argv []string
	env  []string
	err  error
	ran  bool
}

func (f *fakeFuzzRunner) Run(_ context.Context, argv, env []string, _, _ io.Writer) error {
	f.ran = true
	f.argv = argv
	f.env = env

	return f.err
}

func runFuzz(
	t *testing.T,
	r fuzzRunner,
	routes func(*router.Router, *App) error,
	opts []Option,
	args ...string,
) (string, error) {
	t.Helper()

	var out bytes.Buffer

	app := newTestApp(t, append(opts, WithStdout(&out), WithRoutes(routes))...)
	cmd := fuzzCommand(r)
	fs := flag.NewFlagSet("api:fuzz", flag.ContinueOnError)
	cmd.Flags(fs)

	if err := fs.Parse(args); err != nil {
		t.Fatal(err)
	}

	err := cmd.Run(t.Context(), app, fs.Args())

	return out.String(), err
}

func TestFuzzCommandBuildsDockerRun(t *testing.T) {
	t.Parallel()

	fake := &fakeFuzzRunner{}
	report := filepath.Join(t.TempDir(), "rep")

	auth := WithFuzzAuth(func(context.Context, *App) (FuzzAuth, error) {
		return FuzzAuth{Header: "Authorization", Value: "Bearer s3cret"}, nil
	})

	_, err := runFuzz(t, fake, openAPIRoutes, []Option{auth}, "-report-dir", report, "-url", "http://127.0.0.1:8080")
	if err != nil {
		t.Fatal(err)
	}

	joined := strings.Join(fake.argv, " ")

	for _, want := range []string{
		"run --rm", "--add-host=host.docker.internal:host-gateway",
		"schemathesis/schemathesis:4.29.0", "--url http://host.docker.internal:8080", "-e VUKEM_FUZZ_AUTH",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("argv %q lacks %q", joined, want)
		}
	}

	if strings.Contains(joined, "s3cret") {
		t.Error("the credential leaked into argv")
	}

	if !slices.Contains(fake.env, "VUKEM_FUZZ_AUTH=Bearer s3cret") {
		t.Errorf("env = %v, want the credential", fake.env)
	}

	if _, err := os.Stat(report); err != nil {
		t.Errorf("report dir not created: %v", err)
	}
}

func TestFuzzCommandDryRunPrintsAndDoesNotRun(t *testing.T) {
	t.Parallel()

	fake := &fakeFuzzRunner{}

	out, err := runFuzz(t, fake, openAPIRoutes, nil, "-dry-run", "-report-dir", t.TempDir(), "-max-examples", "7")
	if err != nil {
		t.Fatal(err)
	}

	if fake.ran {
		t.Fatal("dry run executed the runner")
	}

	for _, want := range []string{"docker run", "max-examples = 7", "use_after_free", "SCHEMATHESIS_HOOKS"} {
		if !strings.Contains(out, want) {
			t.Errorf("dry run output lacks %q:\n%s", want, out)
		}
	}
}

func TestFuzzConfigDerivedFromDocument(t *testing.T) {
	t.Parallel()

	readOnly := fuzzTOML(fuzzSettings{MaxExamples: 50, Workers: 1, ReadOnly: true})
	if !strings.Contains(readOnly, "use_after_free.enabled = false") {
		t.Errorf("read-only config keeps use_after_free:\n%s", readOnly)
	}

	writable := fuzzTOML(fuzzSettings{MaxExamples: 50, Workers: 1, AuthHeader: "X-Api-Key"})
	if strings.Contains(writable, "use_after_free") {
		t.Errorf("writable API config disables use_after_free:\n%s", writable)
	}

	if !strings.Contains(writable, `"X-Api-Key" = "${VUKEM_FUZZ_AUTH}"`) {
		t.Errorf("auth header not wired to the env var:\n%s", writable)
	}
}

func TestFuzzDetectsReadOnly(t *testing.T) {
	t.Parallel()

	doc, err := newTestApp(t, WithRoutes(openAPIRoutes)).OpenAPI()
	if err != nil {
		t.Fatal(err)
	}

	if !fuzzReadOnly(doc) {
		t.Error("a GET-only document is not read-only")
	}

	for p := range doc.Paths {
		doc.Paths[p]["post"] = nil
	}

	if fuzzReadOnly(doc) {
		t.Error("a document with POST is read-only")
	}
}

func TestFuzzRejectsBadInput(t *testing.T) {
	t.Parallel()

	fake := &fakeFuzzRunner{}
	badHeader := WithFuzzAuth(func(context.Context, *App) (FuzzAuth, error) {
		return FuzzAuth{Header: "Bad Header\n", Value: "x"}, nil
	})
	hookErr := errors.New("no db")
	failing := WithFuzzAuth(func(context.Context, *App) (FuzzAuth, error) { return FuzzAuth{}, hookErr })

	if _, err := runFuzz(t, fake, openAPIRoutes, []Option{badHeader}, "-dry-run"); err == nil {
		t.Error("an invalid header name was accepted")
	}

	if _, err := runFuzz(t, fake, openAPIRoutes, []Option{failing}, "-dry-run"); !errors.Is(err, hookErr) {
		t.Errorf("hook error = %v", err)
	}

	empty := func(*router.Router, *App) error { return nil }
	if _, err := runFuzz(t, fake, empty, nil, "-dry-run", "-prefix", "/nothing"); err == nil {
		t.Error("an app without documented routes was accepted")
	}

	if _, err := runFuzz(t, fake, openAPIRoutes, nil, "-dry-run", "-url", "ftp://x"); !errors.Is(err, errUsage) {
		t.Errorf("bad url error = %v", err)
	}
}
