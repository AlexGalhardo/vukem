package main

import (
	"archive/tar"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestLatestStable(t *testing.T) {
	t.Parallel()

	tags := []string{
		"v0.19.0", "v0.20.1", "v0.20.0", "v1.0.0-rc.1", "v1.0.0", "v1.2.0", "v1.10.0", "v1.10.0-beta.1",
		"v2.0.0", "drivers/redis/v1.3.0", "drivers/redis/v0.1.0", "drivers/redis/v1.3.0+meta",
		"drivers/postgres/v0.3.0", "contrib/x/v1.0.01",
	}

	tests := []struct {
		name, prefix, major string
		want                string
		wantOK              bool
	}{
		{"core v1 picks highest numerically", "", "v1", "v1.10.0", true},
		{"core v0", "", "v0", "v0.20.1", true},
		{"core ignores nested tags", "", "v3", "", false},
		{"nested module v1", "drivers/redis/", "v1", "drivers/redis/v1.3.0", true},
		{"nested module without v1", "drivers/postgres/", "v1", "", false},
		{"leading zero is not semver", "contrib/x/", "v1", "", false},
		{"bad major", "", "1", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, ok := latestStable(tags, tt.prefix, tt.major)
			if got != tt.want || ok != tt.wantOK {
				t.Errorf("latestStable(%q, %q) = %q, %v, want %q, %v", tt.prefix, tt.major, got, ok, tt.want, tt.wantOK)
			}
		})
	}
}

func TestParseReport(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want []change
	}{
		{
			name: "full report",
			in: "Incompatible changes:\r\n- ./auth/hash.DefaultArgon2: removed\n- ./.New: changed from func() to func(int)\n" +
				"Compatible changes:\n- ./orm.(*Query).Each: added\n",
			want: []change{
				{Package: "./auth/hash", Subject: "DefaultArgon2", Message: "removed", Incompatible: true},
				{Package: ".", Subject: "New", Message: "changed from func() to func(int)", Incompatible: true},
				{Package: "./orm", Subject: "(*Query).Each", Message: "added"},
			},
		},
		{
			name: "incompatible only has no headers",
			in:   "- ./web.Ctx.Old: removed\n",
			want: []change{{Package: "./web", Subject: "Ctx.Old", Message: "removed", Incompatible: true}},
		},
		{name: "empty", in: "", want: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if diff := cmp.Diff(tt.want, parseReport(tt.in)); diff != "" {
				t.Errorf("parseReport() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestDiscoverModules(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	write := func(rel, content string) {
		t.Helper()

		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			t.Fatal(err)
		}

		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	write("go.mod", "// core\r\nmodule example.com/core\r\n\r\ngo 1.27\r\n")
	write("drivers/redis/go.mod", "module example.com/core/drivers/redis\n")
	write("drivers/a/go.mod", "module \"example.com/core/drivers/a\"\n")
	write("drivers/nomod/x.go", "package nomod\n")
	write("contrib/grpc/go.mod", "module example.com/core/contrib/grpc\n")
	write("internal/bench/go.mod", "module example.com/core/internal/bench\n")

	got, err := discoverModules(root)
	if err != nil {
		t.Fatal(err)
	}

	want := []module{
		{Dir: ".", Path: "example.com/core"},
		{Dir: "contrib/grpc", Path: "example.com/core/contrib/grpc"},
		{Dir: "drivers/a", Path: "example.com/core/drivers/a"},
		{Dir: "drivers/redis", Path: "example.com/core/drivers/redis"},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("discoverModules() mismatch (-want +got):\n%s", diff)
	}

	if p := (module{Dir: "drivers/a"}).tagPrefix(); p != "drivers/a/" {
		t.Errorf("tagPrefix() = %q, want %q", p, "drivers/a/")
	}
}

func TestRenderMarkdown(t *testing.T) {
	t.Parallel()

	results := []result{
		{
			Module: module{Dir: ".", Path: "example.com/core"},
			Base:   "v1.0.0",
			Changes: []change{
				{Package: "./orm", Subject: "Old", Message: "removed", Incompatible: true},
				{Package: "./orm", Subject: "New", Message: "added"},
				{Package: ".", Subject: "Run", Message: "added"},
			},
		},
		{Module: module{Dir: "drivers/x", Path: "example.com/core/drivers/x"}, Base: "drivers/x/v1.0.0"},
		{Module: module{Dir: "drivers/y", Path: "example.com/core/drivers/y"}, Skipped: "no stable v1 tag"},
	}

	var buf bytes.Buffer
	if err := renderMarkdown(&buf, "API diff", "apicheck -report r.md", results); err != nil {
		t.Fatal(err)
	}

	want := "# API diff\n\n" +
		"Generated with `apicheck -report r.md`; each change is reviewed in [review.md](review.md).\n\n" +
		"| Module | Base tag | Incompatible | Compatible |\n| --- | --- | --- | --- |\n" +
		"| `example.com/core` | `v1.0.0` | 1 | 2 |\n" +
		"| `example.com/core/drivers/x` | `drivers/x/v1.0.0` | 0 | 0 |\n" +
		"| `example.com/core/drivers/y` | none (no stable v1 tag) | - | - |\n" +
		"\n## example.com/core\n\nCompared with `v1.0.0`.\n" +
		"\n### example.com/core\n\nCompatible:\n\n- `Run`: added\n" +
		"\n### example.com/core/orm\n\nIncompatible:\n\n- `Old`: removed\n\nCompatible:\n\n- `New`: added\n" +
		"\n## example.com/core/drivers/x\n\nCompared with `drivers/x/v1.0.0`.\n\nNo API changes.\n"
	if diff := cmp.Diff(want, buf.String()); diff != "" {
		t.Errorf("renderMarkdown() mismatch (-want +got):\n%s", diff)
	}
}

func TestExtractTar(t *testing.T) {
	t.Parallel()

	build := func(t *testing.T, entries map[string]string) *bytes.Buffer {
		t.Helper()

		var buf bytes.Buffer

		tw := tar.NewWriter(&buf)
		for name, body := range entries {
			hdr := &tar.Header{Name: name, Mode: 0o600, Size: int64(len(body)), Typeflag: tar.TypeReg}
			if err := tw.WriteHeader(hdr); err != nil {
				t.Fatal(err)
			}

			if _, err := tw.Write([]byte(body)); err != nil {
				t.Fatal(err)
			}
		}

		if err := tw.Close(); err != nil {
			t.Fatal(err)
		}

		return &buf
	}

	t.Run("writes nested files", func(t *testing.T) {
		t.Parallel()

		dst := t.TempDir()
		if err := extractTar(build(t, map[string]string{"drivers/x/go.mod": "module x\n"}), dst); err != nil {
			t.Fatal(err)
		}

		b, err := os.ReadFile(filepath.Join(dst, "drivers", "x", "go.mod"))
		if err != nil || string(b) != "module x\n" {
			t.Errorf("extracted go.mod = %q, %v, want %q", b, err, "module x\n")
		}
	})

	t.Run("rejects escaping entries", func(t *testing.T) {
		t.Parallel()

		err := extractTar(build(t, map[string]string{"../evil.go": "package evil\n"}), t.TempDir())
		if err == nil || !strings.Contains(err.Error(), "escapes") {
			t.Errorf("extractTar(../evil.go) = %v, want an escape error", err)
		}
	})
}
