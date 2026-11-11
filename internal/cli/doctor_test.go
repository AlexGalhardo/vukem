package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/AlexGalhardo/vukem/config"
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
	want := "cli: v0.0.0-test\nruntime: " + runtime.Version() + "\nupgrade: " + starterUpgradeDocs + "\n"
	if code != 0 || !strings.HasPrefix(out, "betterleaks: ") || !strings.Contains(out, want) {
		t.Errorf("vukem doctor = %d %q %q", code, out, errOut)
	}
}

func TestDoctorCoreToolsStayGeneric(t *testing.T) {
	t.Parallel()

	for _, tool := range doctorTools() {
		switch tool.name {
		case "lilypond", "ffmpeg":
			t.Fatalf("core doctor lists %s; an app registers that in .vukem/doctor.json", tool.name)
		}
	}
}

func TestDoctorProject(t *testing.T) {
	t.Parallel()

	key := config.GenerateKey()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "storage"), 0o755); err != nil {
		t.Fatal(err)
	}

	body := "APP_KEY=" + key + "\nPASSWORD=hunter2\nDATABASE_URL=postgres://user:s3cret@db.example/app\nPORT=8080\nTRUSTED_PROXIES=10.0.0.0/8\n"
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := os.MkdirAll(filepath.Join(dir, ".vukem"), 0o755); err != nil {
		t.Fatal(err)
	}

	extra := `{"tools":[{"name":"vukem-doctor-not-a-binary","why":"optional app tool","install":"https://example.test/tool"}]}`
	if err := os.WriteFile(filepath.Join(dir, ".vukem", "doctor.json"), []byte(extra), 0o600); err != nil {
		t.Fatal(err)
	}

	code, out, errOut := vukem(t, dir, "doctor", "--json", "--strict")
	if code != 0 {
		t.Fatalf("doctor --strict = %d %s %s", code, out, errOut)
	}

	for _, secret := range []string{key, "hunter2", "s3cret", "db.example"} {
		if strings.Contains(out, secret) || strings.Contains(errOut, secret) {
			t.Fatalf("doctor leaked %q\n%s", secret, out)
		}
	}

	var report struct {
		CLI     string `json:"cli"`
		Runtime string `json:"runtime"`
		Upgrade string `json:"upgrade"`
		Tools   []struct {
			Name   string `json:"name"`
			Status string `json:"status"`
		} `json:"tools"`
		Checks []doctorCheck `json:"checks"`
	}
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatal(err)
	}

	if report.CLI != "v0.0.0-test" || report.Runtime != runtime.Version() || report.Upgrade != starterUpgradeDocs {
		t.Fatalf("versions = %s %s %s", report.CLI, report.Runtime, report.Upgrade)
	}

	byName := map[string]doctorCheck{}
	for _, c := range report.Checks {
		byName[c.Name] = c
	}

	if byName["app_key"].Detail != "set" || byName["app_key"].Status != "ok" {
		t.Fatalf("app_key = %+v", byName["app_key"])
	}

	if byName["database"].Status != "ok" || strings.Contains(byName["database"].Detail, "postgres://") {
		t.Fatalf("database = %+v", byName["database"])
	}

	if byName["port"].Detail != "8080" || byName["proxy"].Detail != "set" {
		t.Fatalf("port/proxy = %+v %+v", byName["port"], byName["proxy"])
	}

	var optional string
	for _, tool := range report.Tools {
		if tool.Name == "vukem-doctor-not-a-binary" {
			optional = tool.Status
		}
	}

	if optional != "missing" {
		t.Fatalf("optional tool status = %q, want missing", optional)
	}

	// A missing key fails --strict and still does not print a secret.
	bare := t.TempDir()
	if err := os.WriteFile(filepath.Join(bare, ".env"), []byte("APP_KEY=\nPASSWORD=hunter2\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	code, out, errOut = vukem(t, bare, "doctor", "--strict")
	if code == 0 || !strings.Contains(errOut, "project checks failed") || strings.Contains(out+errOut, "hunter2") {
		t.Fatalf("strict missing key = %d out=%q err=%q", code, out, errOut)
	}

	if !strings.Contains(out, "app_key: missing; run vukem key:generate") {
		t.Fatalf("human report = %q", out)
	}

	// Informative mode stays exit 0 when the same check fails.
	if code, _, errOut = vukem(t, bare, "doctor"); code != 0 {
		t.Fatalf("informative doctor = %d %s", code, errOut)
	}
}

func TestDoctorAppEnvCheck(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name, dotenv, process, status string
	}{
		{"unset", "APP_KEY=\n", "", "warn"},
		{"in .env", "APP_ENV=development\n", "", "ok"},
		{"in the process environment", "APP_KEY=\n", "production", "ok"},
		{"test", "APP_ENV=test\n", "", "ok"},
		{"mixed case is refused like the boot", "APP_ENV=Production\n", "", "fail"},
		{"invalid in .env", "APP_ENV=staging\n", "", "fail"},
		{"invalid in the process environment", "APP_KEY=\n", "prod", "fail"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, ".env"), []byte(tt.dotenv), 0o600); err != nil {
				t.Fatal(err)
			}

			var out, errOut bytes.Buffer

			code := Run(t.Context(), []string{"doctor", "--json"}, Env{
				Dir: dir, Stdout: &out, Stderr: &errOut, Now: fixedNow, Version: "v0.0.0-test",
				Getenv: func(k string) (string, bool) { return tt.process, k == "APP_ENV" && tt.process != "" },
			})
			if code != 0 {
				t.Fatalf("doctor = %d %s", code, errOut.String())
			}

			var report struct {
				Checks []doctorCheck `json:"checks"`
			}
			if err := json.Unmarshal(out.Bytes(), &report); err != nil {
				t.Fatal(err)
			}

			var got *doctorCheck

			for i := range report.Checks {
				if report.Checks[i].Name == "app_env" {
					got = &report.Checks[i]
				}
			}

			if got == nil || got.Status != tt.status {
				t.Fatalf("app_env check = %+v, want status %s", got, tt.status)
			}

			if tt.status == "warn" && (got.Detail != "APP_ENV is not set: the app runs as production" ||
				got.Fix != "add APP_ENV=development to .env for local work") {
				t.Errorf("warning text = %+v", got)
			}
		})
	}
}

func TestGoVersionCheck(t *testing.T) {
	t.Parallel()

	const goMod = "module x\n\ngo 1.27.4\n"

	tests := []struct {
		name, goMod, installed, status, detail string
	}{
		{
			"older",
			goMod,
			"go version go1.26.2 windows/amd64",
			"fail",
			"go go1.26.2 is older than the go.mod minimum go1.27.4",
		},
		{"equal", goMod, "go version go1.27.4 linux/amd64", "ok", "go go1.27.4 satisfies go.mod go1.27.4"},
		{"newer", goMod, "go version go1.28.0 darwin/arm64", "ok", "go go1.28.0 satisfies go.mod go1.27.4"},
		{
			"language version only",
			"module x\n\ngo 1.27\n",
			"go version go1.27.4 linux/amd64",
			"ok",
			"go go1.27.4 satisfies go.mod go1.27",
		},
		{
			"go line with a comment",
			"module x\n\ngo 1.27.4 // security fixes\n",
			"go version go1.26.2 windows/amd64",
			"fail",
			"go go1.26.2 is older than the go.mod minimum go1.27.4",
		},
		{"no go line", "module x\n", "go version go1.27.4 linux/amd64", "ok", "no go.mod minimum"},
		{"malformed go line", "module x\n\ngo banana\n", "go version go1.27.4 linux/amd64", "ok", "no go.mod minimum"},
		{"no go.mod", "", "go version go1.27.4 linux/amd64", "ok", "no go.mod minimum"},
		{"devel toolchain", goMod, "go version devel +abc123 linux/amd64", "ok", "go version not parsable"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := goVersionCheck(tt.goMod, tt.installed)
			if got.Name != "go_version" || got.Status != tt.status || got.Detail != tt.detail {
				t.Errorf("goVersionCheck() = %+v, want status %q detail %q", got, tt.status, tt.detail)
			}

			if tt.status == "fail" && got.Fix != "install Go go1.27.4 or newer from https://go.dev/dl/" {
				t.Errorf("fix = %q", got.Fix)
			}
		})
	}
}
