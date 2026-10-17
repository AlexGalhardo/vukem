package vukem

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/AlexGalhardo/vukem/config"
)

func TestRunMain_KeyGenerate(t *testing.T) {
	t.Parallel()

	// No APP_KEY and a build that must not run: key:generate works before
	// the app (which fails closed without a key) can be built.
	noEnv := func(string) (string, bool) { return "", false }
	noBuild := func(...Option) (*App, error) { return nil, errors.New("build called") }

	run := func(args ...string) (string, error) {
		var out bytes.Buffer
		err := RunMain(t.Context(), append([]string{"app", "key:generate"}, args...), noEnv, noBuild, WithStdout(&out))

		return out.String(), err
	}

	first, err := run()
	if err != nil {
		t.Fatalf("RunMain(key:generate) = %v, want nil", err)
	}

	if !strings.HasSuffix(first, "\n") || strings.Count(first, "\n") != 1 {
		t.Fatalf("RunMain(key:generate) printed %q, want one line", first)
	}

	if _, err := config.ParseKey(config.NewSecret(strings.TrimSpace(first))); err != nil {
		t.Fatalf("ParseKey(%q) = %v, want a valid APP_KEY", first, err)
	}

	second, err := run()
	if err != nil || second == first {
		t.Fatalf("second key:generate = %q, %v, want a different key", second, err)
	}

	if _, err := run("extra"); !errors.Is(err, errUsage) {
		t.Errorf("key:generate extra = %v, want errUsage", err)
	}

	if _, err := run("-h"); err != nil {
		t.Errorf("key:generate -h = %v, want nil", err)
	}
}

func TestKeyGenerate_Listed(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer

	app := newTestApp(t, WithStdout(&out))

	if err := app.Call(t.Context(), "key:generate", nil); err != nil {
		t.Fatalf("Call(key:generate) = %v, want nil", err)
	}

	if _, err := config.ParseKey(config.NewSecret(strings.TrimSpace(out.String()))); err != nil {
		t.Fatalf("Call(key:generate) printed %q: %v", out.String(), err)
	}

	out.Reset()
	printCommands(app)

	if !strings.Contains(out.String(), "key:generate") {
		t.Errorf("list = %q, want key:generate", out.String())
	}
}
