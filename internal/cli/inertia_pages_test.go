package cli

import (
	"path/filepath"
	"testing"
)

// The Inertia starters are born with typed pages: the committed
// resources/js/page-props.ts must be what codegen:pages writes from the Go
// declarations, for every frontend.
func TestInertiaStartersPagePropsAreInSync(t *testing.T) {
	t.Parallel()
	requireGo(t)

	repo, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}

	for _, st := range inertiaStarters {
		t.Run(st.name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()

			if code, out, errOut := vukem(t, dir, "new", "app", "--inertia", st.name, "--module", "example.com/app",
				"--framework", repo, "--no-install", "--pm", "npm"); code != 0 {
				t.Fatalf("vukem new --inertia %s = %d\n%s%s", st.name, code, out, errOut)
			}

			project := filepath.Join(dir, "app")

			requireFile(t, project, "app/pages/pages.go")
			requireFile(t, project, "resources/js/page-props.ts")

			if code, out, errOut := vukem(t, project, "codegen:pages", "-check"); code != 0 {
				t.Fatalf("codegen:pages -check = %d (page-props.ts is out of date)\n%s%s", code, out, errOut)
			}
		})
	}
}

func TestAddLocalImport(t *testing.T) {
	t.Parallel()

	const module = "example.com/app"

	tests := []struct {
		name, in, want string
	}{
		{
			name: "sorted inside the local group",
			in:   "package b\n\nimport (\n\tvukem \"x/vukem\"\n\n\t\"example.com/app/app/jobs\"\n\t\"example.com/app/database\"\n)\n",
			want: "package b\n\nimport (\n\tvukem \"x/vukem\"\n\n\t\"example.com/app/app/jobs\"\n\t\"example.com/app/app/pages\"\n\t\"example.com/app/database\"\n)\n",
		},
		{
			name: "after the last local import",
			in:   "package b\n\nimport (\n\tvukem \"x/vukem\"\n\n\t\"example.com/app/app/jobs\"\n)\n",
			want: "package b\n\nimport (\n\tvukem \"x/vukem\"\n\n\t\"example.com/app/app/jobs\"\n\t\"example.com/app/app/pages\"\n)\n",
		},
		{
			name: "a new group when the project has no local import",
			in:   "package b\n\nimport (\n\tvukem \"x/vukem\"\n)\n",
			want: "package b\n\nimport (\n\tvukem \"x/vukem\"\n\n\t\"example.com/app/app/pages\"\n)\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := string(addLocalImport([]byte(tt.in), module, module+"/app/pages")); got != tt.want {
				t.Errorf("addLocalImport() =\n%s\nwant\n%s", got, tt.want)
			}
		})
	}
}
