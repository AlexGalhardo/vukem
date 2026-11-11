package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestStubPublishOne(t *testing.T) {
	t.Parallel()

	dir := project(t)

	code, out, errOut := vukem(t, dir, "stub:publish", "controller")
	if code != 0 || !strings.Contains(out, "created stubs/controller.go.tmpl") {
		t.Fatalf("code=%d out=%q err=%q", code, out, errOut)
	}

	want, err := stubFS.ReadFile("stubs/controller.go.tmpl")
	if err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(filepath.Join(dir, "stubs", "controller.go.tmpl"))
	if err != nil {
		t.Fatal(err)
	}

	if diff := cmp.Diff(string(want), string(got)); diff != "" {
		t.Fatalf("published stub differs (-want +got):\n%s", diff)
	}
}

func TestStubPublishAll(t *testing.T) {
	t.Parallel()

	dir := project(t)

	if code, out, errOut := vukem(t, dir, "stub:publish"); code != 0 {
		t.Fatalf("code=%d out=%q err=%q", code, out, errOut)
	}

	names, err := stubNames()
	if err != nil {
		t.Fatal(err)
	}

	entries, err := os.ReadDir(filepath.Join(dir, "stubs"))
	if err != nil {
		t.Fatal(err)
	}

	if len(entries) != len(names) || len(names) == 0 {
		t.Fatalf("published %d files, want %d", len(entries), len(names))
	}
}

func TestStubPublishRefusesToOverwrite(t *testing.T) {
	t.Parallel()

	dir := project(t)

	if code, _, _ := vukem(t, dir, "stub:publish", "controller"); code != 0 {
		t.Fatalf("first publish code = %d", code)
	}

	if code, _, errOut := vukem(
		t,
		dir,
		"stub:publish",
		"controller",
	); code != 1 ||
		!strings.Contains(errOut, "already exists") {
		t.Fatalf("second publish code=%d err=%q", code, errOut)
	}

	if code, _, errOut := vukem(t, dir, "stub:publish", "controller", "--force"); code != 0 {
		t.Fatalf("--force code=%d err=%q", code, errOut)
	}
}

func TestStubPublishIsAllOrNothing(t *testing.T) {
	t.Parallel()

	dir := project(t)

	if err := os.MkdirAll(filepath.Join(dir, "stubs"), 0o750); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(dir, "stubs", "model.go.tmpl"), []byte("mine"), 0o600); err != nil {
		t.Fatal(err)
	}

	if code, _, _ := vukem(t, dir, "stub:publish", "controller", "model"); code != 1 {
		t.Fatalf("code = %d, want 1", code)
	}

	if _, err := os.Stat(filepath.Join(dir, "stubs", "controller.go.tmpl")); !os.IsNotExist(err) {
		t.Fatalf("controller was published anyway: %v", err)
	}
}

func TestStubPublishUnknownName(t *testing.T) {
	t.Parallel()

	if code, _, errOut := vukem(
		t,
		project(t),
		"stub:publish",
		"nope",
	); code != 2 ||
		!strings.Contains(errOut, "controller.go.tmpl") {
		t.Fatalf("code=%d err=%q, want 2 listing the valid names", code, errOut)
	}
}

func TestStubPublishedStubWins(t *testing.T) {
	t.Parallel()

	dir := project(t)

	if code, _, errOut := vukem(t, dir, "stub:publish", "controller"); code != 0 {
		t.Fatalf("publish: %d %q", code, errOut)
	}

	p := filepath.Join(dir, "stubs", "controller.go.tmpl")

	src, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(p, append(src, "\n// PUBLISHED-STUB-MARKER\n"...), 0o600); err != nil {
		t.Fatal(err)
	}

	if code, _, errOut := vukem(t, dir, "make:controller", "Foo"); code != 0 {
		t.Fatalf("make:controller: %d %q", code, errOut)
	}

	got, err := os.ReadFile(filepath.Join(dir, "app", "controllers", "foo_controller.go"))
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(string(got), "PUBLISHED-STUB-MARKER") {
		t.Fatal("make:controller ignored the published stub")
	}
}

func TestStubPublishOutsideProject(t *testing.T) {
	t.Parallel()

	if code, _, errOut := vukem(
		t,
		t.TempDir(),
		"stub:publish",
	); code != 1 ||
		!strings.Contains(errOut, "no go.mod here") {
		t.Fatalf("code=%d err=%q", code, errOut)
	}
}
