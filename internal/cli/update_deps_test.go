package cli

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestSatisfies(t *testing.T) {
	t.Parallel()

	tests := []struct {
		version, rng string
		want         bool
	}{
		{"5.9.3", ">=5.0.0", true},
		{"6.0.1", "^5.0.0 || ^6.0.0", true},
		{"7.0.0", "^5.0.0 || ^6.0.0", false},
		{"6.1.0", ">=5.0.0 <6.1.0", false},
		{"5.4.9", "~5.4.0", true},
		{"5.5.0", "~5.4.0", false},
		{"5.5.0", "5.x", true},
		{"6.0.0", "5", false},
		{"0.2.5", "^0.2.0", true},
		{"0.3.0", "^0.2.0", false},
		{"9.9.9", "*", true},
		{"5.2.0", ">5.1 <=5.2", true},
	}

	for _, tt := range tests {
		if got := satisfies(tt.version, tt.rng); got != tt.want {
			t.Errorf("satisfies(%q, %q) = %v, want %v", tt.version, tt.rng, got, tt.want)
		}
	}
}

func TestNewestIgnoresPreReleases(t *testing.T) {
	t.Parallel()

	got := newest([]string{"v1.2.0", "v1.10.0", "v1.11.0-rc.1", "v2.0.0+incompatible", "v1.9.9"}, func(v string) bool {
		return strings.HasPrefix(v, "v1.")
	})
	if got != "v1.10.0" {
		t.Fatalf("newest = %q, want v1.10.0", got)
	}
}

func TestRewritePackageJSON(t *testing.T) {
	t.Parallel()

	raw := "{\n\t\"scripts\": {\"vite\": \"vite build\"},\n\t\"dependencies\": {\"vite\":  \"^6.0.0\"},\n" +
		"\t\"devDependencies\": {\"@types/node\": \"~22.1.0\"}\n}\n"
	got := rewritePackageJSON([]byte(raw), []depChange{
		{"js", "vite", "^6.0.0", "7.1.0"}, {"js", "@types/node", "~22.1.0", "24.0.1"},
	})
	want := "{\n\t\"scripts\": {\"vite\": \"vite build\"},\n\t\"dependencies\": {\"vite\":  \"7.1.0\"},\n" +
		"\t\"devDependencies\": {\"@types/node\": \"24.0.1\"}\n}\n"

	if string(got) != want {
		t.Fatalf("rewrite =\n%s\nwant\n%s", got, want)
	}
}

// registryServer fakes the Go proxy and the npm registry on one host: the key is the escaped URL path.
func registryServer(t *testing.T, bodies map[string]string) string {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, ok := bodies[strings.TrimPrefix(r.URL.EscapedPath(), "/")]
		if !ok {
			http.NotFound(w, r)

			return
		}

		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)

	return srv.URL
}

func TestPlanGoUpdates(t *testing.T) {
	t.Parallel()

	proxy := registryServer(t, map[string]string{
		"github.com/!alex!galhardo/vukem/drivers/postgres/@v/list": "v0.7.0\nv0.8.0\nv0.9.0-rc.1\n",
		"github.com/other/lib/@v/list":                             "v1.0.0\nv1.2.0\nv1.3.0-beta.1\nv1.2.1\n",
		"github.com/up/todate/@v/list":                             "v1.0.0\n",
	})

	gomod := []byte(`module example.com/app

go 1.27.4

require (
	github.com/AlexGalhardo/vukem v0.1.0
	github.com/AlexGalhardo/vukem/drivers/postgres v0.7.0 // indirect
	github.com/other/lib v1.0.0
	github.com/up/todate v1.0.0
	github.com/indirect/only v1.0.0 // indirect
	github.com/local/dep v1.0.0
)

replace github.com/local/dep => ../dep
`)

	got, err := planGoUpdates(t.Context(), proxy, gomod)
	if err != nil {
		t.Fatal(err)
	}

	want := []depChange{
		{"go", "github.com/AlexGalhardo/vukem/drivers/postgres", "v0.7.0", "v0.8.0"},
		{"go", "github.com/other/lib", "v1.0.0", "v1.2.1"},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Fatalf("changes (-want +got):\n%s", diff)
	}
}

func packument(peers map[string]string, versions ...string) string {
	var b strings.Builder

	b.WriteString(`{"versions":{`)

	for i, v := range versions {
		if i > 0 {
			b.WriteByte(',')
		}

		b.WriteString(`"` + v + `":{`)

		if p := peers[v]; p != "" {
			b.WriteString(`"peerDependencies":{"typescript":"` + p + `"}`)
		}

		b.WriteByte('}')
	}

	b.WriteString("}}")

	return b.String()
}

func TestPlanJSUpdates(t *testing.T) {
	t.Parallel()

	registry := registryServer(t, map[string]string{
		"vite":       packument(nil, "6.0.0", "7.0.0", "8.0.0-beta.1"),
		"htmx.org":   packument(nil, "2.0.4", "4.0.1", "4.1.0", "4.2.0-rc.1", "5.0.0"),
		"typescript": packument(nil, "5.9.3", "6.0.2", "7.0.0"),
		"vue-tsc":    packument(map[string]string{"3.1.0": ">=5.0.0 <7.0.0"}, "3.0.0", "3.1.0"),
		"@types%2Fx": packument(nil, "1.0.0"),
		"same":       packument(nil, "1.0.0"),
	})

	pkg := packageJSON{
		Dependencies: map[string]string{
			"htmx.org": "^2.0.4",
			"same":     "1.0.0",
			"local":    "workspace:*",
			"tag":      "latest",
		},
		DevDependencies: map[string]string{
			"vite":       "^6.0.0",
			"typescript": "~5.8.0",
			"vue-tsc":    "3.0.0",
			"@types/x":   "~0.9.0",
		},
	}

	got, err := planJSUpdates(t.Context(), registry, pkg)
	if err != nil {
		t.Fatal(err)
	}

	want := []depChange{
		{"js", "@types/x", "~0.9.0", "1.0.0"},
		{"js", "htmx.org", "^2.0.4", "4.1.0"},
		{"js", "typescript", "~5.8.0", "6.0.2"},
		{"js", "vite", "^6.0.0", "7.0.0"},
		{"js", "vue-tsc", "3.0.0", "3.1.0"},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Fatalf("changes (-want +got):\n%s", diff)
	}
}

func TestPlanJSUpdatesNoTypeScriptFits(t *testing.T) {
	t.Parallel()

	registry := registryServer(t, map[string]string{
		"typescript":   packument(nil, "7.0.0"),
		"svelte-check": packument(map[string]string{"4.0.0": "<6.0.0"}, "4.0.0"),
	})

	_, err := planJSUpdates(t.Context(), registry, packageJSON{
		DevDependencies: map[string]string{"typescript": "5.0.0", "svelte-check": "3.0.0"},
	})
	if err == nil || !strings.Contains(err.Error(), "no stable typescript") {
		t.Fatalf("err = %v", err)
	}
}

func TestRegistryGetBoundsAndStatus(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/big" {
			_, _ = w.Write(make([]byte, maxRegistryBody+1))

			return
		}

		http.Error(w, "no", http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)

	for _, path := range []string{"/big", "/err"} {
		if _, err := registryGet(t.Context(), srv.URL+path, ""); err == nil {
			t.Errorf("registryGet(%s) succeeded", path)
		}
	}
}

func TestJSInstaller(t *testing.T) {
	t.Parallel()

	for file, want := range map[string]string{
		"bun.lock": "bun", "pnpm-lock.yaml": "pnpm", "yarn.lock": "yarn", "package-lock.json": "npm", "": "npm",
	} {
		dir := t.TempDir()

		if file != "" {
			if err := os.WriteFile(filepath.Join(dir, file), nil, 0o600); err != nil {
				t.Fatal(err)
			}
		}

		if got := jsInstaller(&Env{Dir: dir}); got[0] != want || got[1] != "install" {
			t.Errorf("lockfile %q: installer = %v, want %s install", file, got, want)
		}
	}
}

func TestUpdateDryRunWritesNothing(t *testing.T) {
	t.Parallel()

	base := registryServer(t, map[string]string{
		"github.com/other/lib/@v/list": "v1.0.0\nv1.1.0\n",
		"vite":                         packument(nil, "7.0.0"),
	})

	dir := t.TempDir()
	files := map[string]string{
		"go.mod":       string(requireGoMod("v0.1.0", "require github.com/other/lib v1.0.0\n")),
		"package.json": `{"devDependencies":{"vite":"^6.0.0"}}`,
		"bun.lock":     "x",
	}

	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	before := fileHashes(t, dir)

	var out, errOut strings.Builder

	code := Run(t.Context(), []string{"update", "--dry-run"}, Env{
		Dir: dir, Stdout: &out, Stderr: &errOut, Now: fixedNow, Version: "v0.2.0", goProxy: base, npmRegistry: base,
	})
	if code != 0 {
		t.Fatalf("code=%d err=%q", code, errOut.String())
	}

	for _, want := range []string{
		"go github.com/AlexGalhardo/vukem v0.1.0 -> v0.2.0",
		"go github.com/other/lib v1.0.0 -> v1.1.0",
		"js vite ^6.0.0 -> 7.0.0",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, out.String())
		}
	}

	if diff := cmp.Diff(before, fileHashes(t, dir)); diff != "" {
		t.Fatalf("dry-run changed a file (-before +after):\n%s", diff)
	}

	if _, err := os.Stat(filepath.Join(dir, ".vukem")); !os.IsNotExist(err) {
		t.Fatalf(".vukem was created: %v", err)
	}
}

func TestUpgradeNameDoesNotUpdate(t *testing.T) {
	t.Parallel()

	dir := updateProject(t)
	before := fileHashes(t, dir)

	var out, errOut strings.Builder

	code := Run(
		t.Context(),
		[]string{"upgrade"},
		Env{Dir: dir, Stdout: &out, Stderr: &errOut, Now: fixedNow, Version: "v0.2.0"},
	)
	if code == 0 || !strings.Contains(errOut.String(), "vukem update") {
		t.Fatalf("code=%d out=%q err=%q", code, out.String(), errOut.String())
	}

	if diff := cmp.Diff(before, fileHashes(t, dir)); diff != "" {
		t.Fatalf("upgrade changed a file (-before +after):\n%s", diff)
	}
}
