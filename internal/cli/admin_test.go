package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallAdminUsage(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeTestFile(t, dir, "go.mod", "module example.com/app\n")
	writeTestFile(t, dir, "routes/web.go", "package routes\n")

	if code, _, errOut := vukem(t, dir, "install:admin"); code != 2 || !strings.Contains(errOut, "install:auth") {
		t.Errorf("install:admin without auth = %d %q", code, errOut)
	}

	if code, _, errOut := vukem(t, dir, "make:admin-resource", "Post"); code != 2 ||
		!strings.Contains(errOut, "install:admin") {
		t.Errorf("make:admin-resource without the panel = %d %q", code, errOut)
	}

	writeTestFile(t, dir, "app/admin/registry.go", "package admin\n")

	if code, _, errOut := vukem(t, dir, "make:admin-resource", "Ghost"); code != 2 ||
		!strings.Contains(errOut, "no models.Ghost") {
		t.Errorf("make:admin-resource for a missing model = %d %q", code, errOut)
	}
}

func writeTestFile(t *testing.T, dir, rel, content string) {
	t.Helper()

	p := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestAdminEndToEnd adds the admin panel to a new project, generates
// resources with and without a request schema, and runs the project's
// tests.
func TestAdminEndToEnd(t *testing.T) {
	t.Parallel()
	requireGo(t)

	repo, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()

	if code, out, errOut := vukem(
		t, dir, "new", "shop", "--module", "example.com/shop", "--framework", repo, "--no-install",
	); code != 0 {
		t.Fatalf("vukem new = %d\n%s%s", code, out, errOut)
	}

	project := filepath.Join(dir, "shop")

	steps := [][]string{
		{"install:auth"},
		{"install:admin"},
		{"make:admin-resource", "Post"},
		{"make:model", "Tag", "-m"},
		{"make:admin-resource", "Tag"},
	}
	for _, args := range steps {
		if code, out, errOut := vukem(t, project, args...); code != 0 {
			t.Fatalf("vukem %s = %d\n%s%s", strings.Join(args, " "), code, out, errOut)
		}
	}

	registry := read(t, project, "app/admin/registry.go")
	for _, want := range []string{"Post(),", "Tag(),", "authz.For[*models.Post](PostPolicy{Admins: admins})"} {
		if !strings.Contains(registry, want) {
			t.Errorf("app/admin/registry.go lacks %q:\n%s", want, registry)
		}
	}

	if !strings.Contains(read(t, project, "app/admin/post.go"), "requests.PostSchema") {
		t.Error("the Post resource does not use requests.PostSchema")
	}

	if !strings.Contains(read(t, project, "database/migrations/registry.go"), "CreateAdminAuditLogsTable()") {
		t.Error("the audit log migration is not registered")
	}

	if !strings.Contains(read(t, project, ".env.example"), "ADMIN_EMAILS=") {
		t.Error(".env.example lacks ADMIN_EMAILS")
	}

	if code, _, errOut := vukem(t, project, "install:admin"); code != 1 || !strings.Contains(errOut, "already exist") {
		t.Errorf("second install:admin = %d %s", code, errOut)
	}

	cmd := exec.CommandContext(t.Context(), "go", "test", "./...")
	cmd.Dir = project
	cmd.Env = append(os.Environ(), "GOWORK=off")

	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("tests after install:admin: %v\n%s", err, out)
	}

	if code, out, errOut := vukem(t, project, "route:list"); code != 0 || !strings.Contains(out, "admin.posts.index") {
		t.Fatalf("route:list = %d\n%s%s", code, out, errOut)
	}
}
