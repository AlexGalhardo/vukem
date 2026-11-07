package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallAuthSocialOnlyRefusesMixedFlags(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "both flags",
			args: []string{"install:auth", "--social-only", "google", "--social", "github"},
			want: "pass --social or --social-only, not both",
		},
		{
			name: "two-factor",
			args: []string{"install:auth", "--social-only", "google", "--2fa"},
			want: "--social-only does not add two-factor or passkeys",
		},
		{
			name: "passkeys",
			args: []string{"install:auth", "--social-only", "google", "--passkeys"},
			want: "--social-only does not add two-factor or passkeys",
		},
		{
			name: "no provider",
			args: []string{"install:auth", "--social-only", ""},
			want: "--social-only needs a provider, for example google",
		},
		{
			name: "x",
			args: []string{"install:auth", "--social-only", "x"},
			want: "sends no email",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			writeFiles(t, dir, map[string]string{"go.mod": "module example.com/app\n\ngo 1.27\n"})

			code, out, errOut := vukem(t, dir, tt.args...)
			if code != 2 || !strings.Contains(errOut, tt.want) {
				t.Fatalf("install:auth %s = %d\n%s%s", strings.Join(tt.args[1:], " "), code, out, errOut)
			}

			if _, err := os.Stat(filepath.Join(dir, "routes")); err == nil {
				t.Fatal("refused install:auth wrote routes")
			}
		})
	}
}

func TestInstallAuthSocialOnlySkipsPasswordPages(t *testing.T) {
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

	code, out, errOut := vukem(t, dir, "install:auth", "--social-only", "google")
	if code != 0 {
		t.Fatalf("install:auth --social-only google = %d\n%s%s", code, out, errOut)
	}

	if !strings.Contains(out, "created routes/auth.go") || !strings.Contains(out, "created routes/social.go") {
		t.Fatalf("stdout:\n%s", out)
	}

	auth := read(t, dir, "routes/auth.go")
	for _, want := range []string{"Continue with google", "/auth/google/redirect"} {
		if !strings.Contains(auth, want) {
			t.Errorf("routes/auth.go missing %q\n%s", want, auth)
		}
	}

	for _, ban := range []string{`type="password"`, "password.request", "forgot-password", "/register"} {
		if strings.Contains(auth, ban) {
			t.Errorf("routes/auth.go contains %q", ban)
		}
	}

	requireFile(t, dir, "routes/social.go")
	requireNoFile(t, dir, "resources/views/auth/login.html")
	requireNoFile(t, dir, "resources/views/auth/register.html")
	requireNoFile(t, dir, "resources/views/auth/forgot-password.html")
	requireNoFile(t, dir, "resources/views/auth/reset-password.html")
}
