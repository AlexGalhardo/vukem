package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestReleaseNotesRefusesAnEmptySection(t *testing.T) {
	t.Parallel()

	src, err := standardsFS.ReadFile("standards/release-notes.go")
	if err != nil {
		t.Fatal(err)
	}

	withSection := "# Changelog\n\n## [Unreleased]\n\n## [1.2.3] - 2026-11-06\n\nAdded the thing.\n\n## [1.2.2]\n\nOlder.\n"
	empty := "## [1.2.3]\n\n## [1.2.2]\n\nOlder.\n"
	missing := "## [1.2.2]\n\nOlder.\n"

	tests := []struct {
		name, changelog, tag, want string
		ok                         bool
	}{
		{name: "section", changelog: withSection, tag: "v1.2.3", ok: true, want: "\nAdded the thing.\n\n"},
		{name: "empty", changelog: empty, tag: "v1.2.3", ok: false},
		{name: "missing", changelog: missing, tag: "v9.9.9", ok: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "release-notes.go"), src, 0o600); err != nil {
				t.Fatal(err)
			}

			if err := os.WriteFile(filepath.Join(dir, "CHANGELOG.md"), []byte(tt.changelog), 0o600); err != nil {
				t.Fatal(err)
			}

			if err := os.WriteFile(
				filepath.Join(dir, "go.mod"),
				[]byte("module example.com/notes\n\ngo 1.27\n"),
				0o600,
			); err != nil {
				t.Fatal(err)
			}

			cmd := exec.CommandContext(t.Context(), "go", "run", ".", tt.tag)
			cmd.Dir = dir

			out, err := cmd.CombinedOutput()
			if tt.ok {
				if err != nil {
					t.Fatalf("release-notes %s: %v\n%s", tt.tag, err, out)
				}

				got, readErr := os.ReadFile(filepath.Join(dir, "release-notes.md"))
				if readErr != nil {
					t.Fatal(readErr)
				}

				if string(got) != tt.want {
					t.Fatalf("release-notes.md = %q, want %q", got, tt.want)
				}

				if strings.Contains(string(got), "See CHANGELOG") {
					t.Fatal("release notes used a placeholder body")
				}

				return
			}

			if err == nil {
				t.Fatalf("release-notes %s wrote a body, want a failure\n%s", tt.tag, out)
			}

			if _, statErr := os.Stat(filepath.Join(dir, "release-notes.md")); statErr == nil {
				t.Fatal("a refused section still wrote release-notes.md")
			}
		})
	}
}

func TestReleaseWorkflowSkipsGoReleaserWithoutConfig(t *testing.T) {
	t.Parallel()

	out, err := renderStandardsFile("workflows/release.yml.tmpl", standardsData{Name: "blog", CI: true})
	if err != nil {
		t.Fatal(err)
	}

	text := string(out)
	for _, want := range []string{
		"go run ./scripts/release-notes.go",
		"gh release create",
		"--notes-file release-notes.md",
		"steps.goreleaser.outputs.present == 'true'",
		".config/goreleaser.yaml",
		".goreleaser.yaml",
		"version: v2.18.2",
		"registry: ghcr.io",
		"provenance: mode=max",
		"actions/attest-build-provenance@",
		"packages: write",
		"id-token: write",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("release.yml misses %q", want)
		}
	}

	for _, ban := range []string{"See CHANGELOG", "echo \"No notes\"", "|| true"} {
		if strings.Contains(text, ban) {
			t.Errorf("release.yml has a notes fallback %q", ban)
		}
	}
}
