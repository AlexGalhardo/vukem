package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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
		v, rng string
		want   bool
	}{
		{"6.0.3", ">=5.0.0", true},
		{"7.0.2", ">=5.0.0", true},
		{"7.0.2", "^5.0.0 || ^6.0.0", false},
		{"6.9.0", "^5.0.0 || ^6.0.0", true},
		{"0.2.5", "^0.2.3", true},
		{"0.3.0", "^0.2.3", false},
		{"1.2.9", "~1.2.3", true},
		{"1.3.0", "~1.2.3", false},
		{"4.9.9", "4.x", true},
		{"5.0.0", "4.x", false},
		{"3.0.0", ">=2 <3", false},
		{"2.5.0", ">=2 <3", true},
		{"9.9.9", "*", true},
		{"1.0.1", ">1.0.0", true},
	}

	for _, tt := range tests {
		got, err := satisfies(mustVersion(t, tt.v), tt.rng)
		if err != nil || got != tt.want {
			t.Errorf("satisfies(%s, %q) = %v, %v; want %v", tt.v, tt.rng, got, err, tt.want)
		}
	}

	if _, err := satisfies(mustVersion(t, "1.0.0"), "1.0.0 - 2.0.0"); err == nil {
		t.Error("hyphen range should be rejected")
	}
}

func TestNamesMajor(t *testing.T) {
	t.Parallel()

	tests := []struct {
		rng  string
		want bool
	}{
		{">=5.0.0", false},
		{"^5.0.0 || ^6.0.0", false},
		{"^5.0.0 || ^6.0.0 || ^7.0.0", true},
		{"7.x", true},
		{"~7.0", true},
		{"<8", false},
	}

	for _, tt := range tests {
		if got := namesMajor(tt.rng, 7); got != tt.want {
			t.Errorf("namesMajor(%q, 7) = %v, want %v", tt.rng, got, tt.want)
		}
	}
}

func mustVersion(t *testing.T, s string) version {
	t.Helper()

	v, err := parseVersion(s)
	if err != nil {
		t.Fatal(err)
	}

	return v
}

type peers = map[string]string

// npmDoc renders the abbreviated packument: dist-tags.latest and the peers of each version.
func npmDoc(t *testing.T, latest string, versions map[string]peers) string {
	t.Helper()

	vs := map[string]any{}
	for v, p := range versions {
		vs[v] = map[string]any{"peerDependencies": p}
	}

	b, err := json.Marshal(map[string]any{"dist-tags": map[string]string{"latest": latest}, "versions": vs})
	if err != nil {
		t.Fatal(err)
	}

	return string(b)
}

// fakeRegistry serves path -> body; unknown paths are 404.
func fakeRegistry(t *testing.T, routes map[string]string) urls {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, ok := routes[r.URL.Path]
		if !ok {
			http.NotFound(w, r)

			return
		}

		if d, ok := strings.CutPrefix(body, "digest:"); ok {
			w.Header().Set("Docker-Content-Digest", d)

			return
		}

		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)

	return urls{
		npm:      srv.URL + "/npm",
		proxy:    srv.URL + "/proxy",
		htmx:     srv.URL + "/htmx/%s/htmx.min.js",
		hub:      srv.URL + "/hub",
		registry: srv.URL + "/registry",
		auth:     srv.URL + "/auth",
		github:   srv.URL + "/gh",
		bun:      srv.URL + "/bun",
	}
}

func TestResolveGroup(t *testing.T) {
	t.Parallel()

	tsHeld := pin{pkg: "typescript", holdMajor: tsHoldMajor, holdUntil: []string{"vue-tsc"}}

	tests := []struct {
		name   string
		routes map[string]string
		pins   []pin
		want   map[string]string
	}{
		{
			name: "latest tag that is a pre-release falls back to the highest stable",
			routes: map[string]string{"/npm/a": npmDoc(t, "3.0.0-rc.1", map[string]peers{
				"2.0.0": nil, "2.1.0": nil, "3.0.0-rc.1": nil, "2.2.0-beta.4": nil, "3.0.0-next.2": nil,
			})},
			pins: []pin{{pkg: "a"}},
			want: map[string]string{"a": "2.1.0"},
		},
		{
			name: "stable latest tag wins",
			routes: map[string]string{"/npm/a": npmDoc(t, "2.0.0", map[string]peers{
				"1.0.0": nil, "2.0.0": nil, "2.0.1-canary.1": nil,
			})},
			pins: []pin{{pkg: "a"}},
			want: map[string]string{"a": "2.0.0"},
		},
		{
			name: "htmx stays on 4.x even though latest is 2 and 5 exists",
			routes: map[string]string{"/npm/htmx.org": npmDoc(t, "2.0.11", map[string]peers{
				"2.0.11": nil, "4.0.0": nil, "4.0.1": nil, "4.1.0-beta1": nil, "5.0.0": nil,
			})},
			pins: []pin{{pkg: "htmx.org", major: 4}},
			want: map[string]string{"htmx.org": "4.0.1"},
		},
		{
			name: "typescript held on 6 while vue-tsc only says >=5",
			routes: map[string]string{
				"/npm/typescript": npmDoc(t, "7.0.2", map[string]peers{"6.0.3": nil, "7.0.2": nil}),
				"/npm/vue-tsc":    npmDoc(t, "3.3.14", map[string]peers{"3.3.14": {"typescript": ">=5.0.0"}}),
			},
			pins: []pin{tsHeld, {pkg: "vue-tsc"}},
			want: map[string]string{"typescript": "6.0.3", "vue-tsc": "3.3.14"},
		},
		{
			name: "typescript moves to 7 once vue-tsc names it",
			routes: map[string]string{
				"/npm/typescript": npmDoc(t, "7.0.2", map[string]peers{"6.0.3": nil, "7.0.2": nil}),
				"/npm/vue-tsc":    npmDoc(t, "4.0.0", map[string]peers{"4.0.0": {"typescript": "^6.0.0 || ^7.0.0"}}),
			},
			pins: []pin{tsHeld, {pkg: "vue-tsc"}},
			want: map[string]string{"typescript": "7.0.2", "vue-tsc": "4.0.0"},
		},
		{
			name: "peer range lowers a pin so the set installs",
			routes: map[string]string{
				"/npm/plugin": npmDoc(t, "1.5.0", map[string]peers{"1.5.0": {"vite": "^7.0.0"}}),
				"/npm/vite":   npmDoc(t, "8.0.0", map[string]peers{"7.2.0": nil, "7.9.1": nil, "8.0.0": nil}),
			},
			pins: []pin{{pkg: "plugin"}, {pkg: "vite"}},
			want: map[string]string{"plugin": "1.5.0", "vite": "7.9.1"},
		},
		{
			name: "go module picks the highest stable tag",
			routes: map[string]string{
				"/proxy/github.com/!masterminds/semver/@v/list": "v1.74.0\nv1.75.0\nv1.76.0-rc.1\nv1.75.0+incompatible\n",
			},
			pins: []pin{{pkg: "github.com/Masterminds/semver", kind: goMod}},
			want: map[string]string{"github.com/Masterminds/semver": "v1.75.0"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			for i := range tt.pins {
				tt.pins[i].group = "g"
			}

			got, err := newClient(fakeRegistry(t, tt.routes)).resolveGroup(context.Background(), tt.pins)
			if err != nil {
				t.Fatal(err)
			}

			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("resolveGroup mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestResolveGroupUnsatisfiable(t *testing.T) {
	t.Parallel()

	reg := fakeRegistry(t, map[string]string{
		"/npm/plugin": npmDoc(t, "1.0.0", map[string]peers{"1.0.0": {"vite": "^9.0.0"}}),
		"/npm/vite":   npmDoc(t, "8.0.0", map[string]peers{"8.0.0": nil}),
	})

	_, err := newClient(
		reg,
	).resolveGroup(context.Background(), []pin{{pkg: "plugin", group: "g"}, {pkg: "vite", group: "g"}})
	if err == nil || !strings.Contains(err.Error(), "peerDependencies") {
		t.Fatalf("err = %v, want a peerDependencies error", err)
	}
}

const (
	oldDigest = "00000000000000000000000000000000000000000000000000000000000000aa"
	newDigest = "sha256:11111111111111111111111111111111111111111111111111111111111111bb"
	oldSHA    = "3d3c42e5aac5ba805825da76410c181273ba90b1"
	newSHA    = "4444444444444444444444444444444444444444"
	htmxBody  = "var htmx=new-4.0.1"
	oldHTMX   = "var htmx=old"
)

// fixture writes the smallest tree discover needs and returns its root.
func fixture(t *testing.T) string {
	t.Helper()

	root := t.TempDir()
	files := map[string]string{
		"internal/cli/starter/package.json": `{
  "devDependencies": {
    "vite": "8.3.4"
  }
}
`,
		"internal/cli/scaffolds/inertia-vue/package.json": `{
  "devDependencies": {
    "typescript": "6.0.3",
    "vue-tsc": "3.3.13"
  }
}
`,
		"internal/cli/standards.go":                            "const markdownlintVersion = \"0.23.3\"\nconst biomeVersion = \"2.6.4\"\n",
		"internal/cli/generate.go":                             "const (\n\treactEmailCLIVersion = \"6.11.6\"\n\treactEmailComponentsVersion = \"1.0.12\"\n)\n",
		"internal/cli/new.go":                                  "const (\n\thtmxVersion = \"4.0.0\"\n\thtmxSHA256  = \"00\"\n)\n",
		"internal/cli/pm.go":                                   "const (\n\tnpmVersion  = \"12.2.0\"\n\tbunVersion  = \"1.4.4\"\n\tpnpmVersion = \"12.14.0\"\n\tyarnVersion = \"1.22.22\"\n)\n",
		"internal/cli/billing.go":                              "const stripeVersion = \"v0.5.3\"\n",
		"internal/cli/studio.go":                               "const studioVersion = \"v0.1.0\"\n",
		"internal/cli/docker.go":                               "const bunImage = \"oven/bun:1.4.4-alpine@sha256:" + oldDigest + "\"\n",
		"internal/cli/docker/Dockerfile.tmpl":                  "FROM golang:1.27.4-alpine3.24@sha256:" + oldDigest + " AS build\nFROM alpine:3.24.2@sha256:" + oldDigest + "\n",
		"internal/cli/standards/workflows/ci.yml.tmpl":         "- uses: actions/checkout@" + oldSHA + " # v7.0.1\n- uses: actions/checkout@" + oldSHA + " # v7.0.1\n- uses: docker/login-action@" + oldSHA + " # v4.2.0\n  run: bash -s \"bun-v1.4.4\"\n",
		"docs/maintenance.md":                                  "The starter pins htmx 4.0.0 and `react-email@6.11.6`. See buf@v1.74.0 and golang:1.27.4-alpine3.24.\nThe Bun version in that step is `1.4.4`; the tailwind note says 4.0.0 alone.\n",
		"site/content/docs/x.mdx":                              "Image oven/bun:1.4.4-alpine pins actions/checkout v7.0.1.\n",
		"site/content/versions/y.mdx":                          "htmx 4.0.0 stays as released.\n",
		"CHANGELOG.md":                                         "htmx 4.0.0 stays as released.\n",
		"go.mod":                                               "module x\n\ngo 1.27.4\n",
		".config/docker/vukem.Dockerfile":                      "ARG GO_IMAGE=golang:1.27.4@sha256:" + oldDigest + "\nARG BUN_VERSION=1.4.4\nARG BUN_SHA256_AMD64=aaa\nARG BUN_SHA256_ARM64=bbb\n",
		"internal/cli/rpc.go":                                  "const (\n\tprotobufVersion = \"v1.36.12\"\n\tconnectVersion  = \"v1.21.1\"\n\tbufVersion      = \"v1.74.0\"\n)\n",
		"internal/cli/starter/resources/public/js/htmx.min.js": oldHTMX,
	}

	for name, body := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}

		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	return root
}

func fixtureRoutes(t *testing.T) map[string]string {
	t.Helper()

	same := func(v string) string { return npmDoc(t, v, map[string]peers{v: nil}) }

	return map[string]string{
		"/npm/vite": npmDoc(
			t,
			"9.0.0-rc.1",
			map[string]peers{"8.3.4": nil, "8.4.0": nil, "9.0.0-rc.1": nil},
		),
		"/npm/typescript": npmDoc(t, "7.0.2", map[string]peers{"6.0.3": nil, "7.0.2": nil}),
		"/npm/vue-tsc": npmDoc(
			t,
			"3.3.13",
			map[string]peers{"3.3.13": {"typescript": ">=5.0.0"}},
		),
		"/npm/markdownlint-cli2":       same("0.23.3"),
		"/npm/@biomejs/biome":          same("2.6.4"),
		"/npm/react-email":             same("6.11.6"),
		"/npm/@react-email/components": same("1.0.12"),
		"/npm/htmx.org": npmDoc(
			t,
			"2.0.11",
			map[string]peers{"2.0.11": nil, "4.0.0": nil, "4.0.1": nil, "5.0.0": nil},
		),
		"/proxy/google.golang.org/protobuf/@v/list": "v1.36.12\n",
		"/proxy/connectrpc.com/connect/@v/list":     "v1.21.1\n",
		"/proxy/github.com/bufbuild/buf/@v/list":    "v1.74.0\n",
		"/npm/npm":                                  npmDoc(t, "12.2.0", map[string]peers{"12.2.0": nil}),
		"/npm/pnpm":                                 npmDoc(t, "12.14.0", map[string]peers{"12.14.0": nil}),
		"/npm/yarn":                                 npmDoc(t, "4.9.0", map[string]peers{"1.22.22": nil, "4.9.0": nil}),
		"/proxy/github.com/!alex!galhardo/vukem/contrib/stripe/@v/list": "v0.5.3\nv0.6.0\nv0.7.0-rc.1\n",
		"/proxy/github.com/!alex!galhardo/vukem/contrib/studio/@v/list": "v0.1.0\n",
		"/hub/v2/repositories/oven/bun/tags": hubTags(
			"1.4.4-alpine",
			"1.4.5-alpine",
			"1.4.5",
			"2.0.0-alpine",
			"1.5.0-alpine",
			"canary-alpine",
		),
		"/hub/v2/repositories/library/golang/tags": hubTags(
			"1.27.4-alpine3.24",
			"1.27.5-alpine3.24",
			"1.27.5-alpine3.25",
			"1.28.0-alpine3.25",
			"1.27.6-rc1-alpine3.25",
			"1.27.5-bookworm",
			"1.27.5",
			"1.28.1",
			"1.27.6-rc1",
		),
		"/hub/v2/repositories/library/alpine/tags": hubTags(
			"3.24.2",
			"3.24.4",
			"3.25.0",
			"4.0.0",
			"edge",
		),
		"/auth": `{"token":"t"}`,
		"/registry/v2/oven/bun/manifests/1.5.0-alpine":            "digest:" + newDigest,
		"/registry/v2/library/golang/manifests/1.27.5-alpine3.25": "digest:" + newDigest,
		"/registry/v2/library/golang/manifests/1.27.5":            "digest:" + newDigest,
		"/bun/bun-v1.5.0/SHASUMS256.txt":                          "x64sum  bun-linux-x64.zip\narm64sum  bun-linux-aarch64.zip\nother  bun-darwin-x64.zip\n",
		"/registry/v2/library/alpine/manifests/3.25.0":            "digest:" + newDigest,
		"/gh/repos/actions/checkout/releases":                     `[{"tag_name":"v7.1.0-rc.1","prerelease":true},{"tag_name":"v7.0.1"},{"tag_name":"v8.0.0","draft":true},{"tag_name":"v7.2.0"},{"tag_name":"v7"}]`,
		"/gh/repos/actions/checkout/git/ref/tags/v7.2.0":          `{"object":{"type":"commit","sha":"` + newSHA + `"}}`,
		"/gh/repos/docker/login-action/releases":                  `[{"tag_name":"v4.2.0"},{"tag_name":"v4.9.0"}]`,
		"/gh/repos/docker/login-action/git/ref/tags/v4.9.0": `{"object":{"type":"tag","sha":"` + strings.Repeat(
			"9",
			40,
		) + `"}}`,
		"/gh/repos/docker/login-action/git/tags/" + strings.Repeat("9", 40): `{"object":{"type":"commit","sha":"` + strings.Repeat(
			"5",
			40,
		) + `"}}`,
		"/htmx/4.0.1/htmx.min.js": htmxBody,
	}
}

// hubTags renders one Docker Hub tags page.
func hubTags(names ...string) string {
	var b strings.Builder

	b.WriteString(`{"next":null,"results":[`)

	for i, n := range names {
		if i > 0 {
			b.WriteString(",")
		}

		b.WriteString(`{"name":"` + n + `"}`)
	}

	b.WriteString("]}")

	return b.String()
}

func TestCheckAndWrite(t *testing.T) {
	t.Parallel()

	root := fixture(t)
	u := fakeRegistry(t, fixtureRoutes(t))

	var out, errOut bytes.Buffer
	if code := run(t.Context(), []string{"-check", "-root", root}, &out, &errOut, u); code != 1 {
		t.Fatalf("-check exit = %d, want 1 (vite and htmx are behind)\n%s%s", code, out.String(), errOut.String())
	}

	if !strings.Contains(out.String(), "8.4.0") || strings.Contains(out.String(), "9.0.0-rc.1") {
		t.Errorf("-check output should list the stable vite only:\n%s", out.String())
	}

	out.Reset()

	if code := run(t.Context(), []string{"-write", "-root", root}, &out, &errOut, u); code != 0 {
		t.Fatalf("-write exit = %d\n%s", code, errOut.String())
	}

	assertFile(t, root, "internal/cli/starter/package.json", "\"vite\": \"8.4.0\"")
	assertFile(
		t,
		root,
		"internal/cli/scaffolds/inertia-vue/package.json",
		"\"typescript\": \"6.0.3\"",
		"\"vue-tsc\": \"3.3.13\"",
	)
	assertFile(t, root, "internal/cli/new.go", "htmxVersion = \"4.0.1\"")
	assertFile(t, root, "internal/cli/starter/resources/public/js/htmx.min.js", htmxBody)

	assertFile(t, root, "internal/cli/pm.go", "bunVersion  = \"1.5.0\"", "yarnVersion = \"1.22.22\"")
	assertFile(t, root, "internal/cli/billing.go", "v0.6.0")
	assertFile(t, root, "internal/cli/docker.go", "oven/bun:1.5.0-alpine@"+newDigest)
	assertFile(
		t,
		root,
		"internal/cli/docker/Dockerfile.tmpl",
		"golang:1.27.5-alpine3.25@"+newDigest,
		"alpine:3.25.0@"+newDigest,
	)
	assertFile(
		t,
		root,
		"internal/cli/standards/workflows/ci.yml.tmpl",
		"bun-v1.5.0",
		"checkout@"+newSHA+" # v7.2.0",
		"login-action@"+strings.Repeat("5", 40)+" # v4.9.0",
	)
	assertFile(
		t,
		root,
		"docs/maintenance.md",
		"htmx 4.0.1",
		"react-email@6.11.6",
		"buf@v1.74.0",
		"golang:1.27.5-alpine3.25",
		"`1.5.0`",
		"4.0.0 alone",
	)
	assertFile(t, root, "site/content/docs/x.mdx", "oven/bun:1.5.0-alpine", "checkout v7.2.0")
	assertFile(t, root, "site/content/versions/y.mdx", "htmx 4.0.0")
	assertFile(t, root, "CHANGELOG.md", "htmx 4.0.0")
	assertFile(
		t,
		root,
		".config/docker/vukem.Dockerfile",
		"golang:1.27.5@"+newDigest,
		"ARG BUN_VERSION=1.5.0",
		"ARG BUN_SHA256_AMD64=x64sum",
		"ARG BUN_SHA256_ARM64=arm64sum",
	)

	sum := sha256.Sum256([]byte(htmxBody))
	assertFile(t, root, "internal/cli/new.go", hex.EncodeToString(sum[:]))

	if code := run(t.Context(), []string{"-check", "-root", root}, &out, &errOut, u); code != 0 {
		t.Fatalf("-check after -write exit = %d, want 0\n%s%s", code, out.String(), errOut.String())
	}
}

func TestRunUsage(t *testing.T) {
	t.Parallel()

	for _, args := range [][]string{nil, {"-check", "-write"}} {
		if code := run(t.Context(), args, &bytes.Buffer{}, &bytes.Buffer{}, defaultURLs); code != 2 {
			t.Errorf("run(%v) = %d, want 2", args, code)
		}
	}
}

func assertFile(t *testing.T, root, name string, wants ...string) {
	t.Helper()

	b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
	if err != nil {
		t.Fatal(err)
	}

	for _, w := range wants {
		if !strings.Contains(string(b), w) {
			t.Errorf("%s does not contain %q:\n%s", name, w, b)
		}
	}
}

func TestGoImageMustMatchGoMod(t *testing.T) {
	t.Parallel()

	root := fixture(t)
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module x\n\ngo 1.28.0\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var out, errOut bytes.Buffer
	if code := run(
		t.Context(),
		[]string{"-check", "-root", root},
		&out,
		&errOut,
		fakeRegistry(t, fixtureRoutes(t)),
	); code != 1 ||
		!strings.Contains(errOut.String(), "go.mod is on Go 1.28") {
		t.Fatalf("code = %d, stderr = %q, want a go.mod mismatch error", code, errOut.String())
	}
}
