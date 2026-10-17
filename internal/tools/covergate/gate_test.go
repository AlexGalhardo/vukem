package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

const m = "github.com/AlexGalhardo/vukem"

func profile(lines ...string) string {
	return "mode: atomic\n" + strings.Join(lines, "\n") + "\n"
}

func TestParse(t *testing.T) {
	t.Parallel()

	in := profile(
		m+"/app.go:1.1,2.2 3 1",
		m+"/app.go:3.1,4.2 1 0",
		m+"/cache/cache.go:1.1,2.2 4 2",
		m+"/cache/cache.go:3.1,4.2 6 0",
		// The same block from a second test binary: counted once, covered.
		m+"/cache/cache.go:3.1,4.2 6 5",
		m+"/session/manager.go:1.1,2.2 9 1",
		m+"/session/manager.go:3.1,4.2 1 0",
		m+"/cache/cachetest/suite.go:1.1,2.2 50 0",
		m+"/database/dbtest/dbtest.go:1.1,2.2 50 0",
		m+"/cmd/vukem/main.go:1.1,2.2 50 0",
		m+"/internal/cli/cli.go:1.1,2.2 50 0",
		"example.com/other/x.go:1.1,2.2 50 0",
	)

	got, err := Parse(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}

	want := []Result{
		{Package: "", Statements: 4, Covered: 3, Gate: 80},
		{Package: "cache", Statements: 10, Covered: 10, Gate: 80},
		{Package: "session", Statements: 10, Covered: 9, Gate: 90},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("Parse() (-want +got):\n%s", diff)
	}
}

func TestParseErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", "empty coverprofile"},
		{"too few fields", profile(m + "/a.go:1.1,2.2 3"), "malformed"},
		{"no position", profile("nofile 3 1"), "malformed"},
		{"bad statements", profile(m + "/a.go:1.1,2.2 x 1"), "statements"},
		{"bad count", profile(m + "/a.go:1.1,2.2 3 y"), "count"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if _, err := Parse(strings.NewReader(tt.in)); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("Parse(%q) = %v, want error containing %q", tt.in, err, tt.want)
			}
		})
	}
}

func TestResultPass(t *testing.T) {
	t.Parallel()

	tests := []struct {
		r    Result
		want bool
	}{
		{Result{Statements: 10, Covered: 8, Gate: 80}, true},
		{Result{Statements: 10000, Covered: 7999, Gate: 80}, false},
		{Result{Statements: 10000, Covered: 8996, Gate: 90}, false}, // prints as 90.0%
		{Result{Statements: 0, Covered: 0, Gate: 80}, false},
	}

	for _, tt := range tests {
		if got := tt.r.Pass(); got != tt.want {
			t.Errorf("%+v.Pass() = %v, want %v", tt.r, got, tt.want)
		}
	}
}

func TestExcluded(t *testing.T) {
	t.Parallel()

	tests := map[string]bool{
		"": false, "router": false, "auth/jwt": false, "vukemtest": false, "cmdline": false,
		"cmd": true, "cmd/vukem": true, "internal/tools/release": true,
		"cache/cachetest": true, "queue/queuetest": true, "storage/storagetest": true, "database/dbtest": true,
		"session/sessiontest": true,
	}

	for pkg, want := range tests {
		if got := excluded(pkg); got != want {
			t.Errorf("excluded(%q) = %v, want %v", pkg, got, want)
		}
	}
}

func writeProfile(t *testing.T, content string) string {
	t.Helper()

	p := filepath.Join(t.TempDir(), "coverage.out")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	return p
}

// strictProfile covers every strict package fully.
func strictProfile(extra ...string) string {
	lines := []string{}
	for pkg := range strictGates {
		lines = append(lines, m+"/"+pkg+"/x.go:1.1,2.2 1 1")
	}

	return profile(append(lines, extra...)...)
}

func TestRun(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		flags       []string
		content     string // written to a profile passed as the last argument
		missingFile bool   // pass a path that does not exist
		wantCode    int
		wantStdout  []string
		wantStderr  string
	}{
		{
			name:       "all pass",
			content:    strictProfile(m + "/cache/c.go:1.1,2.2 1 1"),
			wantStdout: []string{"covergate: 8 core packages meet their gate"},
		},
		{
			name:       "verbose lists every package",
			flags:      []string{"-v"},
			content:    strictProfile(m + "/app.go:1.1,2.2 1 1"),
			wantStdout: []string{"ok   (root)", "ok   router", "100.0% (gate 90%)"},
		},
		{
			name:       "below gate",
			content:    strictProfile(m+"/cache/c.go:1.1,2.2 1 1", m+"/cache/c.go:3.1,4.2 1 0"),
			wantCode:   1,
			wantStdout: []string{"FAIL cache", "50.0% (gate 80%)"},
			wantStderr: "1 package(s) below the coverage gate",
		},
		{
			name:       "strict package missing",
			content:    profile(m + "/cache/c.go:1.1,2.2 1 1"),
			wantCode:   1,
			wantStdout: []string{"FAIL router", "missing from the profile"},
			wantStderr: "7 package(s) below",
		},
		{name: "no args", wantCode: 2, wantStderr: "usage"},
		{name: "bad flag", flags: []string{"-x", "f"}, wantCode: 2},
		{name: "missing file", missingFile: true, wantCode: 1, wantStderr: "covergate:"},
		{name: "malformed file", content: "garbage\n", wantCode: 1, wantStderr: "malformed"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			args := tt.flags

			switch {
			case tt.content != "":
				args = append(args, writeProfile(t, tt.content))
			case tt.missingFile:
				args = append(args, filepath.Join(t.TempDir(), "none.out"))
			}

			var stdout, stderr bytes.Buffer

			if code := run(args, &stdout, &stderr); code != tt.wantCode {
				t.Fatalf(
					"run() = %d, want %d; stdout:\n%s\nstderr:\n%s",
					code,
					tt.wantCode,
					stdout.String(),
					stderr.String(),
				)
			}

			for _, w := range tt.wantStdout {
				if !strings.Contains(stdout.String(), w) {
					t.Errorf("stdout = %q, want %q", stdout.String(), w)
				}
			}

			if !strings.Contains(stderr.String(), tt.wantStderr) {
				t.Errorf("stderr = %q, want %q", stderr.String(), tt.wantStderr)
			}
		})
	}
}
