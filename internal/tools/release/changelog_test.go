package main

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

const base = `# Changelog

Intro.

## [Unreleased]

## [0.0.1] - 2026-10-16

### Added

- Foundation.

[Unreleased]: https://github.com/AlexGalhardo/golang-framework/compare/v0.0.1...HEAD
[0.0.1]: https://github.com/AlexGalhardo/golang-framework/releases/tag/v0.0.1
`

func TestInsertRelease(t *testing.T) {
	t.Parallel()

	section := "## [0.0.2] - 2026-10-17\n\n### Added\n\n- **config:** Add loader\n"

	got, err := insertRelease(base, section, "v0.0.2", "v0.0.1")
	if err != nil {
		t.Fatal(err)
	}

	want := `# Changelog

Intro.

## [Unreleased]

## [0.0.2] - 2026-10-17

### Added

- **config:** Add loader

## [0.0.1] - 2026-10-16

### Added

- Foundation.

[Unreleased]: https://github.com/AlexGalhardo/golang-framework/compare/v0.0.2...HEAD
[0.0.2]: https://github.com/AlexGalhardo/golang-framework/compare/v0.0.1...v0.0.2
[0.0.1]: https://github.com/AlexGalhardo/golang-framework/releases/tag/v0.0.1
`
	if diff := cmp.Diff(want, got); diff != "" {
		t.Fatalf("insertRelease() mismatch (-want +got):\n%s", diff)
	}

	// Inserting again is refused; released sections are immutable.
	if _, err := insertRelease(got, section, "v0.0.2", "v0.0.1"); err == nil {
		t.Fatal("duplicate release accepted")
	}

	notes := releaseNotes(got, "v0.0.2")
	if notes != "### Added\n\n- **config:** Add loader\n" {
		t.Fatalf("releaseNotes() = %q", notes)
	}
	if releaseNotes(got, "v0.0.1") != "### Added\n\n- Foundation.\n" {
		t.Fatalf("releaseNotes(last) = %q", releaseNotes(got, "v0.0.1"))
	}
	if releaseNotes(got, "v9.9.9") != "" {
		t.Fatal("releaseNotes(missing) not empty")
	}
}

func TestInsertReleaseFirstAndErrors(t *testing.T) {
	t.Parallel()

	first := "# Changelog\n\n## [Unreleased]\n\n### Added\n\n- pending note\n"
	got, err := insertRelease(first, "## [0.1.0] - 2026-01-01\n\n### Fixed\n\n- x\n", "v0.1.0", "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "[0.1.0]: https://github.com/AlexGalhardo/golang-framework/releases/tag/v0.1.0") ||
		!strings.Contains(got, "## [Unreleased]\n\n### Added\n\n- pending note\n\n## [0.1.0]") {
		t.Fatalf("first release = %q", got)
	}

	if _, err := insertRelease("# Changelog\n", "## [0.1.0]", "v0.1.0", ""); err == nil {
		t.Fatal("missing Unreleased accepted")
	}
	if _, err := insertRelease(base, "## [0.0.3] - x", "v0.0.2", "v0.0.1"); err == nil {
		t.Fatal("mismatched section accepted")
	}
}

func TestValidVersion(t *testing.T) {
	t.Parallel()

	for v, want := range map[string]bool{
		"v0.0.2": true, "v1.0.0-beta.1": true, "v1.0.0-rc.2": true, "v10.20.30": true,
		"0.0.2": false, "v1.0": false, "v01.0.0": false, "vx.y.z": false,
	} {
		if validVersion(v) != want {
			t.Errorf("validVersion(%q) = %v", v, !want)
		}
	}
}
