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
