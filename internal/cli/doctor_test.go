package cli

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"
)

func TestDoctor(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		look func(string) (string, error)
		run  func(context.Context, string, string) (string, error)
		want string
	}{
		{
			name: "missing",
			look: func(string) (string, error) { return "", exec.ErrNotFound },
			want: "betterleaks: not installed (secret scanning in the pre-commit hook and npm run secrets:scan); " +
				"install it from https://github.com/betterleaks/betterleaks/releases\n",
		},
		{
			name: "installed",
			look: func(string) (string, error) { return "/usr/local/bin/betterleaks", nil },
			run: func(_ context.Context, bin, arg string) (string, error) {
				if bin != "/usr/local/bin/betterleaks" || arg != "version" {
					t.Errorf("run(%q, %q)", bin, arg)
				}

				return "1.9.0\n", nil
			},
			want: "betterleaks: 1.9.0 (/usr/local/bin/betterleaks)\n",
		},
		{
			name: "version fails",
			look: func(string) (string, error) { return "/bin/betterleaks", nil },
			run:  func(context.Context, string, string) (string, error) { return "", errors.New("exit status 1") },
			want: "betterleaks: installed at /bin/betterleaks, version unknown (exit status 1)\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var out bytes.Buffer

			doctor(t.Context(), &Env{Stdout: &out}, tt.look, tt.run)

			if got := out.String(); got != tt.want {
				t.Errorf("doctor =\n%q\nwant\n%q", got, tt.want)
			}
		})
	}
}

func TestDoctorCommandNeverFails(t *testing.T) {
	t.Parallel()

	code, out, errOut := vukem(t, t.TempDir(), "doctor")
	if code != 0 || !strings.HasPrefix(out, "betterleaks: ") {
		t.Errorf("vukem doctor = %d %q %q", code, out, errOut)
	}
}
