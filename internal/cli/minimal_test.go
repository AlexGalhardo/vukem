package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

// TestNewMinimalProjectEndToEnd creates a database-less project: no
// database code, and builds that leave the SQLite driver out.
func TestNewMinimalProjectEndToEnd(t *testing.T) {
	t.Parallel()
	requireGo(t)

	repo, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()

	code, out, errOut := vukem(t, dir, "new", "site", "--minimal", "--module", "example.com/site",
		"--framework", repo, "--no-install")
	if code != 0 {
		t.Fatalf("vukem new --minimal = %d\n%s%s", code, out, errOut)
	}

	if strings.Contains(out, "vukem migrate") {
		t.Errorf("next steps mention migrate:\n%s", out)
	}

	project := filepath.Join(dir, "site")

	for _, gone := range []string{
		"database", "app/models", "app/requests", "app/controllers/post_controller.go",
		"app/controllers/upload_controller.go", "resources/views/posts", "tests/posts_test.go",
	} {
		if _, err := os.Stat(filepath.Join(project, filepath.FromSlash(gone))); err == nil {
			t.Errorf("%s still exists in the minimal starter", gone)
		}
	}

	if env := read(t, project, ".env.example"); strings.Contains(env, "DATABASE_URL") {
		t.Errorf(".env.example = %q", env)
	}

	if tags := read(t, project, buildTagsFile); !strings.Contains(tags, "vukem_nosqlite") {
		t.Errorf("%s = %q", buildTagsFile, tags)
	}

	// The image must build with the same tags as vukem build.
	if df := read(
		t,
		project,
		"Dockerfile",
	); !strings.Contains(df, buildTagsFile) ||
		!strings.Contains(df, `-tags "$tags"`) {
		t.Errorf("Dockerfile ignores %s: %q", buildTagsFile, df)
	}

	goCmd := func(args ...string) string {
		t.Helper()

		cmd := exec.CommandContext(t.Context(), "go", args...)
		cmd.Dir = project
		cmd.Env = append(os.Environ(), "GOWORK=off")

		out, err := combinedOutput(cmd)
		if err != nil {
			t.Fatalf("go %s: %v\n%s", strings.Join(args, " "), err, out)
		}

		return string(out)
	}

	if deps := goCmd("list", "-deps", "-tags", "vukem_nosqlite", "./cmd/app"); strings.Contains(deps, "modernc.org") {
		t.Errorf("cmd/app with vukem_nosqlite still depends on modernc.org:\n%s", deps)
	}

	goCmd("test", "./...")

	// route:list builds the app with the project's tags and runs it.
	if code, out, errOut := vukem(t, project, "route:list"); code != 0 || !strings.Contains(out, "home") ||
		strings.Contains(out, "posts.index") {
		t.Fatalf("route:list = %d\n%s%s", code, out, errOut)
	}

	if mod := goCmd("version", "-m", filepath.Join(project, cacheDir, "bin", exe("app"))); strings.Contains(
		mod, "modernc.org/sqlite",
	) {
		t.Errorf("the built app links SQLite:\n%s", mod)
	}
}

func TestNewMinimalRejectsOtherStacks(t *testing.T) {
	t.Parallel()

	for _, flag := range []string{"--api", "--templ"} {
		if code, _, errOut := vukem(t, t.TempDir(), "new", "x", "--minimal", flag); code != 2 ||
			!strings.Contains(errOut, "--minimal") {
			t.Errorf("new --minimal %s = %d %q", flag, code, errOut)
		}
	}
}

func TestProjectBuildTags(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		content string // "" means no file
		want    string
		wantErr bool
	}{
		{"no file", "", "", false},
		{"one tag", "vukem_nosqlite\n", "vukem_nosqlite", false},
		{"comments and separators", "# leave SQLite out\r\nvukem_nosqlite, other\n", "vukem_nosqlite,other", false},
		{"invalid tag", "-ldflags=-X\n", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			if tt.content != "" {
				writeFiles(t, dir, map[string]string{buildTagsFile: tt.content})
			}

			got, err := projectBuildTags(&Env{Dir: dir})
			if (err != nil) != tt.wantErr {
				t.Fatalf("projectBuildTags() error = %v, wantErr %v", err, tt.wantErr)
			}

			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("projectBuildTags() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
