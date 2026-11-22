package vukem

import (
	"bytes"
	"context"
	"os"
	"os/exec"
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

type pagesAuthorWithAVeryLongTypeNameXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXX struct {
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

// pagesLongNames has names and generics wider than the 120 columns of the Biome
// config: the object alias moves under the equals sign, a Record breaks its
// arguments and a nullable array becomes a union (VF-107, Biome 2.6.7).
type pagesLongNames struct {
	ByName map[string]pagesAuthorWithAVeryLongTypeNameXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXX `json:"byName"`
	List   []pagesAuthorWithAVeryLongTypeNameXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXX          `json:"list,omitempty"`
	Plain  string                                                                                                              `json:"plain"`
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

func TestWrapLongUnions(t *testing.T) {
	t.Parallel()

	name := strings.Repeat("N", 95)
	long := `"aaaaaaaaaaaaaaa" | "bbbbbbbbbbbbbbb" | "ccccccccccccccc" | "ddddddddddddddd" | "eeeeeeeeeeeeeee" | "f | g"`

	tests := []struct {
		name, in, want string
	}{
		{"short line untouched", "  a: string | null;", "  a: string | null;"},
		{
			"property",
			"  status: " + long + ";",
			"  status:\n" + `    | "aaaaaaaaaaaaaaa"` + "\n" + `    | "bbbbbbbbbbbbbbb"` + "\n" + `    | "ccccccccccccccc"` + "\n" +
				`    | "ddddddddddddddd"` + "\n" + `    | "eeeeeeeeeeeeeee"` + "\n" + `    | "f | g";`,
		},
		{
			"type alias",
			"export type X = " + long + ";",
			"export type X =\n" + `  | "aaaaaaaaaaaaaaa"` + "\n" + `  | "bbbbbbbbbbbbbbb"` + "\n" + `  | "ccccccccccccccc"` + "\n" +
				`  | "ddddddddddddddd"` + "\n" + `  | "eeeeeeeeeeeeeee"` + "\n" + `  | "f | g";`,
		},
		{
			// Biome 2.6.7: the arguments of Record go one per line, then the union that still does not fit.
			"a long union inside Record breaks the arguments, then the union",
			"  a: Record<string, " + strings.Repeat("string | ", 14) + "number>;",
			"  a: Record<\n    string,\n" + strings.Repeat("    | string\n", 14) + "    | number\n  >;",
		},
		{
			"Record that does not fit breaks its arguments",
			"  m: Record<string, " + name + ">;",
			"  m: Record<\n    string,\n    " + name + "\n  >;",
		},
		{
			"nested Record breaks at each level",
			"  g: Record<string, Record<string, " + name + ">>;",
			"  g: Record<\n    string,\n    Record<string, " + name + ">\n  >;",
		},
		{
			"an alias that does not fit breaks after the equals sign",
			"export type " + name + " = string[];",
			"export type " + name + " =\n  string[];",
		},
		{
			"a nullable array is a union to Biome, a nullable name is not",
			"  h?: " + name + "[] | null;",
			"  h?:\n    | " + name + "[]\n    | null;",
		},
		{
			"a nullable name stays whole",
			"  h?: " + name + "XXXXXXXXXXXXXXXXXXXXXXXXXXXXXXX | null;",
			"  h?: " + name + "XXXXXXXXXXXXXXXXXXXXXXXXXXXXXXX | null;",
		},
		{"a name too long to break stays whole", "  k: " + name + name + ";", "  k: " + name + name + ";"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if diff := cmp.Diff(tt.want, wrapLongUnions(tt.in, 100)); diff != "" {
				t.Errorf("wrapLongUnions mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestWrapLongUnions_ObjectAlias(t *testing.T) {
	t.Parallel()

	long := strings.Repeat("L", 110)
	in := "export type " + long + " = {\n  a: string;\n  b: Record<string, string>;\n};\n\n" +
		"export type Short = {\n  a: string;\n};"
	want := "export type " + long + " =\n  {\n    a: string;\n    b: Record<string, string>;\n  };\n\n" +
		"export type Short = {\n  a: string;\n};"

	if diff := cmp.Diff(want, wrapLongUnions(in, 120)); diff != "" {
		t.Errorf("wrapLongUnions mismatch (-want +got):\n%s", diff)
	}
}

func TestTsPropKey(t *testing.T) {
	t.Parallel()

	for in, want := range map[string]string{"Home": "Home", "ok_key": "ok_key", "Projects/Index": `"Projects/Index"`} {
		if got := tsPropKey(in); got != want {
			t.Errorf("tsPropKey(%q) = %s, want %s", in, got, want)
		}
	}
}

// TestCodegenPages_StableUnderBiome runs the biome binary, when installed,
// over the generated file with the config vukem new writes (VF-107).
func TestCodegenPages_StableUnderBiome(t *testing.T) {
	t.Parallel()

	biome, err := exec.LookPath("biome")
	if err != nil {
		t.Skip("biome is not installed")
	}

	cfg, err := os.ReadFile(filepath.Join("internal", "cli", "standards", "biome.json"))
	if err != nil {
		t.Fatal(err)
	}

	icfg := pagesConfig()
	icfg.Pages = append(icfg.Pages, inertia.Define[struct {
		Owner *pagesAuthorWithAVeryLongTypeNameXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXX `json:"owner"`
	}]("Long/Enum").Info(), inertia.Define[pagesLongNames]("Long/Names/"+strings.Repeat("C", 120)).Info())

	dir := t.TempDir()
	// The config's "vcs" block needs a git repository; the check does not.
	cfg = bytes.Replace(cfg, []byte(`"vcs": { "enabled": true, "clientKind": "git", "useIgnoreFile": true },`), nil, 1)

	if err := os.WriteFile(filepath.Join(dir, "biome.json"), cfg, 0o600); err != nil {
		t.Fatal(err)
	}

	if err := os.MkdirAll(filepath.Join(dir, "resources", "js"), 0o750); err != nil {
		t.Fatal(err)
	}

	got, err := runPagesApp(t, icfg, "codegen:pages", "-o", "-")
	if err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(dir, "resources", "js", "page-props.ts"), []byte(got), 0o600); err != nil {
		t.Fatal(err)
	}

	routes, err := runPagesApp(t, icfg, "codegen:routes", "-o", "-")
	if err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(dir, "resources", "js", "routes.ts"), []byte(routes), 0o600); err != nil {
		t.Fatal(err)
	}

	cmd := exec.CommandContext(t.Context(), biome, "format", "resources/js")
	cmd.Dir = dir

	if out, err := cmd.CombinedOutput(); err != nil {
		t.Errorf("biome would reformat the generated file: %v\n%s\n%s", err, out, got)
	}
}

// Without Biome installed the same wide names still get the shapes it writes.
func TestCodegenPages_LongNamesAreLaidOutLikeBiome(t *testing.T) {
	t.Parallel()

	icfg := pagesConfig()
	icfg.Pages = append(icfg.Pages, inertia.Define[pagesLongNames]("Long/Names/"+strings.Repeat("C", 120)).Info())

	got, err := runPagesApp(t, icfg, "codegen:pages", "-o", "-")
	if err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{
		"Props =\n  {\n    byName: Record<\n      string,\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("page-props lacks %q:\n%s", want, got)
		}
	}

	for line := range strings.SplitSeq(got, "\n") {
		if len([]rune(line)) > biomeLineWidth && !strings.Contains(line, "AVeryLongTypeName") &&
			!strings.HasPrefix(line, "  \"") && !strings.Contains(line, "CCCCCCCC") {
			t.Errorf("line of %d characters passes the width: %.100s", len([]rune(line)), line)
		}
	}
}
