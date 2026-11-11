package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestStarterTypedViews pins the typed views of the starters: the stacks that
// render html/template pages are born with app/views/views.go and
// vukem.WithViewPages, and `view:check` passes on them. The stacks without
// html/template pages (api, templ, inertia) have neither.
func TestStarterTypedViews(t *testing.T) {
	t.Parallel()
	requireGo(t)

	repo, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name  string
		flags []string
		typed bool
	}{
		{name: "htmx", typed: true},
		{name: "minimal", flags: []string{"--minimal"}, typed: true},
		{name: "api", flags: []string{"--api"}},
		{name: "inertia", flags: []string{"--inertia", "react"}},
		{name: "templ", flags: []string{"--templ"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			args := append(
				[]string{
					"new",
					"app",
					"--module",
					"example.com/app",
					"--framework",
					repo,
					"--no-install",
					"--pm",
					"npm",
				},
				tt.flags...)

			if code, out, errOut := vukem(t, dir, args...); code != 0 {
				t.Fatalf("vukem %v = %d\n%s%s", args, code, out, errOut)
			}

			project := filepath.Join(dir, "app")
			app := read(t, project, "bootstrap/app.go")
			_, statErr := os.Stat(filepath.Join(project, "app", "views", "views.go"))

			if !tt.typed {
				if statErr == nil || strings.Contains(app, "WithViewPages") || strings.Contains(app, "/app/views") {
					t.Errorf("a stack without html/template pages has typed views (stat err %v):\n%s", statErr, app)
				}

				return
			}

			if statErr != nil || !strings.Contains(app, "vukem.WithViewPages(views.All()...)") {
				t.Fatalf("typed views missing (stat err %v):\n%s", statErr, app)
			}

			cmd := exec.CommandContext(t.Context(), "go", "build", "./...")
			cmd.Dir = project
			cmd.Env = append(os.Environ(), "GOWORK=off")

			if out, err := combinedOutput(cmd); err != nil {
				t.Fatalf("go build: %v\n%s", err, out)
			}

			if code, out, errOut := vukem(t, project, "view:check"); code != 0 {
				t.Fatalf("view:check = %d\n%s%s", code, out, errOut)
			}
		})
	}
}
