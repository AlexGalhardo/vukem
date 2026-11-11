package vukem

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/AlexGalhardo/vukem/inertia"
)

type pagesAuthor struct {
	Name string `json:"name"`
}

type pagesIndexProps struct {
	Projects []pagesAuthor                      `json:"projects"`
	Filter   string                             `json:"filter"`
	Stats    func(context.Context) (int, error) `json:"stats"    inertia:"optional"`
	Charts   []int                              `json:"charts"   inertia:"defer"`
}

type pagesShared struct {
	App  string       `json:"app"`
	User *pagesAuthor `json:"user"`
	Flag *pagesAuthor `json:"flag,omitempty"`
}

func runPagesApp(t *testing.T, icfg inertia.Config, args ...string) (string, error) {
	t.Helper()

	var out bytes.Buffer

	build := func(opts ...Option) (*App, error) {
		return New(append(opts,
			WithLogger(discardLogger()), WithStdout(&out),
			WithProviders(&connectingProvider{}), WithInertia(icfg))...)
	}

	err := RunMain(t.Context(), append([]string{"app"}, args...), testSource(nil).Lookup, build)

	return out.String(), err
}

func pagesConfig() inertia.Config {
	return inertia.Config{
		Pages: []inertia.PageInfo{
			inertia.Define[pagesIndexProps]("Projects/Index").Info(),
			inertia.Define[struct{}]("Home").Info(),
			inertia.Define[struct {
				Project pagesAuthor `json:"project"`
			}]("Projects/Show").Info(),
		},
		SharedType: reflect.TypeFor[pagesShared](),
	}
}

func TestCodegenPagesCommand_Golden(t *testing.T) {
	t.Parallel()

	got, err := runPagesApp(t, pagesConfig(), "codegen:pages", "-o", "-")
	if err != nil {
		t.Fatal(err)
	}

	golden := filepath.Join("testdata", "pages.golden.ts")
	if *update {
		if err := os.WriteFile(golden, []byte(got), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}

	if diff := cmp.Diff(string(want), got); diff != "" {
		t.Errorf("codegen:pages mismatch (-want +got):\n%s", diff)
	}
}

func TestCodegenPagesCommand_OpenSharedProps(t *testing.T) {
	t.Parallel()

	cfg := pagesConfig()
	cfg.SharedType = nil

	got, err := runPagesApp(t, cfg, "codegen:pages", "-o", "-")
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(
		got,
		"export type SharedProps = {\n  errors: Record<string, string | Record<string, string>>;\n  [key: string]: unknown;\n};",
	) {
		t.Errorf("SharedProps without SharedType is not open:\n%s", got)
	}
}

func TestCodegenPagesCommand_Check(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "page-props.ts")

	_, err := runPagesApp(t, pagesConfig(), "codegen:pages", "-check", "-o", path)
	if err == nil || !strings.Contains(err.Error(), `is stale: run "vukem codegen:pages"`) {
		t.Fatalf("-check on a missing file = %v", err)
	}

	if _, err := runPagesApp(t, pagesConfig(), "codegen:pages", "-o", path); err != nil {
		t.Fatal(err)
	}

	if _, err := runPagesApp(t, pagesConfig(), "codegen:pages", "-check", "-o", path); err != nil {
		t.Errorf("-check on a fresh file = %v", err)
	}

	if err := os.WriteFile(path, []byte("// edited\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := runPagesApp(t, pagesConfig(), "codegen:pages", "-check", "-o", path); err == nil {
		t.Error("-check on an edited file = nil")
	}

	if _, err := runPagesApp(t, pagesConfig(), "codegen:pages", "-check", "-o", "-"); err == nil {
		t.Error("-check -o - = nil")
	}
}

func TestCodegenPagesCommand_Errors(t *testing.T) {
	t.Parallel()

	if _, err := runPagesApp(t, inertia.Config{}, "codegen:pages", "-o", "-"); err == nil ||
		!strings.Contains(err.Error(), "no typed pages: list them in inertia.Config.Pages") {
		t.Errorf("no pages: err = %v", err)
	}

	cfg := inertia.Config{Pages: []inertia.PageInfo{
		inertia.Define[struct{}]("Projects/Index").Info(),
		inertia.Define[struct{}]("projects.index").Info(),
	}}

	_, err := runPagesApp(t, cfg, "codegen:pages", "-o", "-")
	if err == nil || !strings.Contains(err.Error(), `"Projects/Index" and "projects.index"`) {
		t.Errorf("duplicate TypeScript name: err = %v", err)
	}
}

func TestPascalCase(t *testing.T) {
	t.Parallel()

	for in, want := range map[string]string{
		"Projects/Index": "ProjectsIndex", "Auth/TwoFactor": "AuthTwoFactor", "home": "Home", "a-b.c": "ABC",
	} {
		if got := pascalCase(in); got != want {
			t.Errorf("pascalCase(%q) = %q, want %q", in, got, want)
		}
	}
}

// A Go type or a component whose TypeScript name is one the file declares
// itself, or that is not an identifier, is an error and not invalid output.
func TestCodegenPagesCommand_ReservedAndInvalidNames(t *testing.T) {
	t.Parallel()

	type Page struct{ X int }

	type PageName struct{ X int }

	type SharedProps struct{ X int }

	type holder struct {
		V Page `json:"v"`
	}

	tests := map[string]inertia.Config{
		"Go type Page": {Pages: []inertia.PageInfo{
			inertia.Define[struct {
				V Page `json:"v"`
			}]("Home").Info(),
		}},
		"Go type PageName": {Pages: []inertia.PageInfo{
			inertia.Define[struct {
				V PageName `json:"v"`
			}]("Home").Info(),
		}},
		"Go type SharedProps": {Pages: []inertia.PageInfo{
			inertia.Define[struct {
				V SharedProps `json:"v"`
			}]("Home").Info(),
		}},
		"component Shared": {
			Pages:      []inertia.PageInfo{inertia.Define[struct{}]("Shared").Info()},
			SharedType: reflect.TypeFor[pagesShared](),
		},
		"component Page": {Pages: []inertia.PageInfo{
			inertia.Define[holder]("Page").Info(),
			inertia.Define[struct{}]("PageX").Info(),
		}},
		"component starting with a digit": {Pages: []inertia.PageInfo{inertia.Define[struct{}]("404").Info()}},
		"component without letters":       {Pages: []inertia.PageInfo{inertia.Define[struct{}]("-").Info()}},
	}

	for name, cfg := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			out, err := runPagesApp(t, cfg, "codegen:pages", "-o", "-")
			if err == nil {
				t.Errorf("codegen:pages accepted it:\n%s", out)
			}
		})
	}
}

// With X-Inertia-Error-Bag the errors prop nests the messages under the bag
// name, so its type admits both shapes.
func TestCodegenPagesCommand_ErrorsTypeAdmitsBags(t *testing.T) {
	t.Parallel()

	got, err := runPagesApp(t, pagesConfig(), "codegen:pages", "-o", "-")
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(got, "errors: Record<string, string | Record<string, string>>;") {
		t.Errorf("SharedProps.errors does not admit an error bag:\n%s", got)
	}
}
