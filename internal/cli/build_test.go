package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

// vukem build matches the Dockerfile (CGO_ENABLED=0) unless the developer
// chose CGO_ENABLED explicitly.
func TestReleaseBuildEnv(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		env  map[string]string
		want []string
	}{
		"default":      {want: []string{"CGO_ENABLED=0"}},
		"explicit on":  {env: map[string]string{"CGO_ENABLED": "1"}, want: nil},
		"explicit off": {env: map[string]string{"CGO_ENABLED": "0"}, want: nil},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			env := &Env{Getenv: func(k string) (string, bool) { v, ok := tt.env[k]; return v, ok }}

			if diff := cmp.Diff(tt.want, releaseBuildEnv(env)); diff != "" {
				t.Errorf("releaseBuildEnv() (-want +got):\n%s", diff)
			}
		})
	}
}

// A file that compiles only with cgo is left out of vukem build.
func TestBuildDisablesCgo(t *testing.T) {
	t.Parallel()
	requireGo(t)

	dir := stdlibApp(t, echoApp)
	cgoOnly := "//go:build cgo\n\npackage main\n\nvar _ = needsCgo\n"

	if err := os.WriteFile(filepath.Join(dir, "cmd", "app", "cgo.go"), []byte(cgoOnly), 0o600); err != nil {
		t.Fatal(err)
	}

	if code, out, errOut := vukem(t, dir, "build"); code != 0 || !strings.Contains(out, "built") {
		t.Fatalf("vukem build = %d %q %q, want the cgo-only file skipped", code, out, errOut)
	}
}
