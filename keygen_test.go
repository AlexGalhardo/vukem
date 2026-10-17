package vukem

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/AlexGalhardo/vukem/config"
)

func assertKey(t *testing.T, out string) {
	t.Helper()

	if !strings.HasSuffix(out, "\n") || strings.Count(out, "\n") != 1 {
		t.Fatalf("key:generate printed %q, want one line", out)
	}

	if _, err := config.ParseKey(config.NewSecret(strings.TrimSpace(out))); err != nil {
		t.Fatalf("ParseKey(%q) = %v, want a valid APP_KEY", out, err)
	}
}

func TestRunMain_KeyGenerate(t *testing.T) {
	t.Parallel()

	// No APP_KEY and a build that must not run: key:generate works before
	// the app (which fails closed without a key) can be built.
	noEnv := func(string) (string, bool) { return "", false }
	noBuild := func(...Option) (*App, error) { return nil, errors.New("build called") }

	run := func(args ...string) (stdout, stderr string, err error) {
		var out, errOut bytes.Buffer
		err = RunMain(t.Context(), append([]string{"app", "key:generate"}, args...), noEnv, noBuild,
			WithStdout(&out), WithStderr(&errOut))

		return out.String(), errOut.String(), err
	}

	first, _, err := run()
	if err != nil {
		t.Fatalf("RunMain(key:generate) = %v, want nil", err)
	}

	assertKey(t, first)

	second, _, err := run()
	if err != nil || second == first {
		t.Fatalf("second key:generate = %q, %v, want a different key", second, err)
	}

	tests := []struct {
		name    string
		args    []string
		wantErr error
		stderr  string
	}{
		{"extra argument", []string{"extra"}, errUsage, "takes no arguments"},
		{"unknown flag", []string{"--write"}, errUsage, "flag provided but not defined"},
		{"help", []string{"-h"}, nil, "Usage of key:generate"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			stdout, stderr, err := run(tt.args...)
			if !errors.Is(err, tt.wantErr) || (tt.wantErr == nil && err != nil) {
				t.Errorf("key:generate %v = %v, want %v", tt.args, err, tt.wantErr)
			}

			// Diagnostics must never pollute APP_KEY=$(./app key:generate ...).
			if stdout != "" {
				t.Errorf("key:generate %v stdout = %q, want empty", tt.args, stdout)
			}

			if !strings.Contains(stderr, tt.stderr) {
				t.Errorf("key:generate %v stderr = %q, want %q", tt.args, stderr, tt.stderr)
			}
		})
	}
}

func TestKeyGenerate_ListedAndCall(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer

	app := newTestApp(t, WithStdout(&out))

	if err := app.Call(t.Context(), "key:generate", nil); err != nil {
		t.Fatalf("Call(key:generate) = %v, want nil", err)
	}

	assertKey(t, out.String())

	if err := app.Call(t.Context(), "key:generate", []string{"x"}); !errors.Is(err, errUsage) {
		t.Errorf("Call(key:generate x) = %v, want errUsage", err)
	}

	out.Reset()

	if err := runCommand(t.Context(), app, "list", nil); err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(out.String(), "key:generate") {
		t.Errorf("list = %q, want key:generate", out.String())
	}
}

func TestRunMain_KeyGenerateBuiltinWins(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer

	build := func(opts ...Option) (*App, error) {
		app, err := New(append(opts, WithSource(testSource(nil)), WithLogger(discardLogger()))...)
		if err != nil {
			return nil, err
		}

		app.AddCommand(CommandFunc{
			CommandName: "key:generate",
			Fn: func(context.Context, *App, []string) error {
				return errors.New("app command ran")
			},
		})

		return app, nil
	}

	getenv := func(string) (string, bool) { return "", false }
	if err := RunMain(t.Context(), []string{"app", "key:generate"}, getenv, build, WithStdout(&out)); err != nil {
		t.Fatalf("RunMain(key:generate) = %v, want the built-in to run", err)
	}

	assertKey(t, out.String())
}
