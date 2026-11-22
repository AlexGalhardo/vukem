package openapi_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AlexGalhardo/vukem/openapi"
)

// VF-107: the generated client is what `biome format` writes with the config
// vukem new writes (120 columns), so a file that was generated and then
// formatted does not differ from a fresh one. Checked against Biome 2.6.7, the
// version vukem new pins, when the biome binary is installed.
func TestTypeScriptClientStableUnderBiome(t *testing.T) {
	t.Parallel()

	biome, err := exec.LookPath("biome")
	if err != nil {
		t.Skip("biome is not installed")
	}

	cfg, err := os.ReadFile(filepath.Join("..", "internal", "cli", "standards", "biome.json"))
	if err != nil {
		t.Fatal(err)
	}

	// The config's "vcs" block needs a git repository; the check does not.
	cfg = bytes.Replace(cfg, []byte(`"vcs": { "enabled": true, "clientKind": "git", "useIgnoreFile": true },`), nil, 1)

	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "resources", "js"), 0o750); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(dir, "biome.json"), cfg, 0o600); err != nil {
		t.Fatal(err)
	}

	doc := openapi.Generate(routes(), openapi.Info{PathPrefix: "/api"})

	for name, opts := range map[string]openapi.ClientOptions{
		"result.ts":   {},
		"throw.ts":    {Style: openapi.StyleThrow},
		"tanstack.ts": {TanStack: true},
	} {
		got, err := openapi.TypeScriptClientWith(doc, opts)
		if err != nil {
			t.Fatal(err)
		}

		if err := os.WriteFile(filepath.Join(dir, "resources", "js", name), []byte(got), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	cmd := exec.CommandContext(t.Context(), biome, "format", "resources/js")
	cmd.Dir = dir

	if out, err := cmd.CombinedOutput(); err != nil {
		t.Errorf("biome would reformat the generated client: %v\n%s", err, out)
	}
}

// Without Biome installed, the shapes it writes: no code line wider than 120
// columns, the error statuses one per line, a union that fits on one line, and
// the object of ApiProblem.issues written out.
func TestTypeScriptClientLayoutMatchesBiome(t *testing.T) {
	t.Parallel()

	doc := openapi.Generate(routes(), openapi.Info{PathPrefix: "/api"})

	for name, opts := range map[string]openapi.ClientOptions{
		"result":   {},
		"throw":    {Style: openapi.StyleThrow},
		"tanstack": {TanStack: true},
	} {
		got, err := openapi.TypeScriptClientWith(doc, opts)
		if err != nil {
			t.Fatal(err)
		}

		for line := range strings.SplitSeq(got, "\n") {
			comment := strings.HasPrefix(strings.TrimSpace(line), "/**") ||
				strings.HasPrefix(strings.TrimSpace(line), "*") ||
				strings.HasPrefix(strings.TrimSpace(line), "//")
			if len([]rune(line)) > 120 && !comment {
				t.Errorf(
					"%s: a code line of %d characters passes the Biome width: %.100s",
					name,
					len([]rune(line)),
					line,
				)
			}
		}

		if !strings.Contains(got, "issues?: {\n    path: string;\n    code: string;") {
			t.Errorf("%s: ApiProblem.issues is not written out", name)
		}

		if strings.Contains(got, "\n\n\n") {
			t.Errorf("%s: two blank lines in a row", name)
		}

		if name != "throw" {
			if strings.Contains(got, "  | 400 | 401") || !strings.Contains(got, "  | 400\n  | 401\n") {
				t.Errorf("%s: HttpErrorStatus is not one member per line", name)
			}

			if !strings.Contains(got, "export type ProductsDestroyError = { status: HttpErrorStatus;") {
				t.Errorf("%s: a union that fits is not on one line", name)
			}
		}
	}
}
