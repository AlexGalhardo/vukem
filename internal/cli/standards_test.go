package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// defaultStandards are the files every project gets, whatever flags were
// passed: they describe the indentation and line endings the starter
// already uses, and nothing else.
var defaultStandards = []string{".editorconfig", ".gitattributes"}

// flaggedStandards is what each flag adds on top of defaultStandards.
var flaggedStandards = map[string][]string{
	"--standards": {
		".markdownlint-cli2.jsonc", "CHANGELOG.md", ".config/cliff.toml",
		".githooks/commit-msg", ".githooks/pre-commit", ".betterleaks.toml",
	},
	"--license": {"LICENSE", "CONTRIBUTING.md", "CODE_OF_CONDUCT.md", "SECURITY.md"},
	"--ci":      {".github/workflows/ci.yml", ".github/dependabot.yml", ".betterleaks.toml"},
	"--deploy":  {".railway/railway.ts"},
	"--docker":  {"compose.yaml"},
}

// TestCIAuditsTheLockfile checks the generated workflow audits the committed
// lockfile itself. A package.json script edit cannot turn that step off, and
// the starter does not ship an overrides block.
func TestCIAuditsTheLockfile(t *testing.T) {
	t.Parallel()

	out, err := renderStandardsFile("workflows/ci.yml.tmpl", standardsData{
		Name: "blog", Standards: true, CI: true, Assets: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	text := string(out)
	for _, want := range []string{
		"go tool govulncheck ./...",
		"npm audit --package-lock-only --audit-level=high",
		"bun install --frozen-lockfile",
		"bun audit --audit-level=high",
		`bash -s "bun-v1.4.3"`,
		"commit package-lock.json or bun.lock",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("ci.yml misses %q", want)
		}
	}

	if strings.Contains(text, "audit false") || strings.Contains(text, "|| true") {
		t.Errorf("ci.yml can skip the audit:\n%s", text)
	}

	if strings.Contains(minimalPackageJSON, `"overrides"`) {
		t.Error("generated package.json ships an override")
	}
}

// artifactIgnore reports whether a markdownlint-cli2 ignore of the form
// "**/name/**" covers path. A new docs page does not match that form.
func artifactIgnore(pattern, path string) bool {
	path = strings.TrimPrefix(path, "/")
	pattern = strings.TrimPrefix(pattern, "/")

	dir, ok := strings.CutPrefix(pattern, "**/")
	if !ok {
		return false
	}

	dir, ok = strings.CutSuffix(dir, "/**")
	if !ok || dir == "" {
		return false
	}

	return path == dir || strings.HasPrefix(path, dir+"/") || strings.Contains(path, "/"+dir+"/")
}

func TestMarkdownlintIgnoresArtifactsNotNewDocs(t *testing.T) {
	t.Parallel()

	raw, err := standardsFS.ReadFile("standards/markdownlint-cli2.jsonc")
	if err != nil {
		t.Fatal(err)
	}

	var lines []string

	for line := range strings.SplitSeq(string(raw), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "//") {
			continue
		}

		lines = append(lines, line)
	}

	var cfg struct {
		Ignores []string `json:"ignores"`
	}
	if err := json.Unmarshal([]byte(strings.Join(lines, "\n")), &cfg); err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{
		"**/storage/**", "**/.cache/**", "**/coverage/**", "**/dist/**", "**/out/**", "**/.next/**",
	} {
		if !slices.Contains(cfg.Ignores, want) {
			t.Errorf("ignores miss %s", want)
		}
	}

	for _, banned := range []string{"**/*.md", "docs/**", "**/docs/**"} {
		if slices.Contains(cfg.Ignores, banned) {
			t.Errorf("ignores drop versioned docs via %s", banned)
		}
	}

	hidden := []string{
		"storage/private/README.md",
		"app/storage/note.md",
		".cache/vite/x.md",
		"coverage/index.md",
		"dist/README.md",
		"out/page.md",
		".next/cache.md",
	}
	kept := []string{"README.md", "docs/new-guide.md", "docs/storage-notes.md"}

	for _, path := range hidden {
		matched := false

		for _, pattern := range cfg.Ignores {
			if artifactIgnore(pattern, path) {
				matched = true
			}
		}

		if !matched {
			t.Errorf("%s is linted; it is a local artifact", path)
		}
	}

	for _, path := range kept {
		for _, pattern := range cfg.Ignores {
			if artifactIgnore(pattern, path) {
				t.Errorf("%s matches %s; a new doc must stay in the lint", path, pattern)
			}
		}
	}
}

func TestNewStandardsFlags(t *testing.T) {
	t.Parallel()

	all := standardsOpts{standards: true, license: "mit", ci: true, deploy: "railway", docker: true}

	tests := []struct {
		name string
		opts standardsOpts
		want []string
		not  []string
	}{
		{
			name: "default",
			want: defaultStandards,
			not: []string{
				".markdownlint-cli2.jsonc", "CHANGELOG.md", ".config/cliff.toml", ".githooks/commit-msg",
				"LICENSE", ".github/workflows/ci.yml", ".railway/railway.ts", "compose.yaml", ".betterleaks.toml",
			},
		},
		{
			name: "standards",
			opts: standardsOpts{standards: true},
			want: append(append([]string{}, defaultStandards...), flaggedStandards["--standards"]...),
			not:  []string{"LICENSE", ".github/workflows/ci.yml", "compose.yaml"},
		},
		{
			name: "license",
			opts: standardsOpts{license: "mit"},
			want: flaggedStandards["--license"],
			not:  []string{"CHANGELOG.md", ".githooks/commit-msg", ".betterleaks.toml"},
		},
		{name: "ci", opts: standardsOpts{ci: true}, want: flaggedStandards["--ci"], not: []string{"compose.yaml"}},
		{
			name: "deploy",
			opts: standardsOpts{deploy: "railway"},
			want: flaggedStandards["--deploy"],
			not:  []string{"compose.yaml"},
		},
		{
			name: "docker",
			opts: standardsOpts{docker: true},
			want: flaggedStandards["--docker"],
			not:  []string{".railway/railway.ts"},
		},
		{
			name: "all",
			opts: all,
			want: func() []string {
				var out []string
				for _, f := range standardsSet(all) {
					out = append(out, f.dst)
				}

				return out
			}(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := map[string]bool{}
			for _, f := range standardsSet(tt.opts) {
				got[f.dst] = true
			}

			for _, rel := range tt.want {
				if !got[rel] {
					t.Errorf("%s not written", rel)
				}
			}

			for _, rel := range tt.not {
				if got[rel] {
					t.Errorf("%s written without its flag", rel)
				}
			}
		})
	}
}

func TestStandardsUsage(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	if code, _, errOut := vukem(t, dir, "new", "blog", "--license", "gpl"); code != 2 ||
		!strings.Contains(errOut, "--license must be one of mit") {
		t.Errorf("--license gpl = %d %q", code, errOut)
	}

	if code, _, errOut := vukem(t, dir, "new", "blog", "--deploy", "fly"); code != 2 ||
		!strings.Contains(errOut, "--deploy must be railway") {
		t.Errorf("--deploy fly = %d %q", code, errOut)
	}

	// The flags are refused before the project directory is created.
	if _, err := os.Stat(filepath.Join(dir, "blog")); err == nil {
		t.Error("blog/ created despite the usage error")
	}
}

// TestStandardsRender renders every standards file for an HTMX project with
// every flag on, and checks what the reports asked for: pinned versions, no
// floating image or action tags, SHA-pinned third-party actions and the
// documentation links in the README.
func TestStandardsRender(t *testing.T) {
	t.Parallel()

	data := newStandardsData(
		starterData{Name: "blog", Module: "github.com/acme/blog"},
		"htmx",
		standardsOpts{standards: true, license: "mit", ci: true, deploy: "railway", docker: true},
		2026,
	)

	if data.Holder != "acme" || data.Repo != "acme/blog" {
		t.Errorf("holder/repo = %q/%q, want acme/acme/blog", data.Holder, data.Repo)
	}

	floating := regexp.MustCompile(`(?i):(latest|edge|nightly|next|canary)\b|@(latest|main|master)\b`)
	// A Go template action the renderer should have replaced; a GitHub
	// expression (${{ github.ref }}) and git-cliff's Tera syntax stay.
	goAction := regexp.MustCompile(`{{-?\s*(\.[A-Z]|if |range |end}}|with |else)`)
	// Every action reference: owner/name@ref.
	uses := regexp.MustCompile(`uses: ([^/\s]+)/(\S+)@(\S+)`)

	for _, f := range standardsSet(
		standardsOpts{standards: true, license: "mit", ci: true, deploy: "railway", docker: true}.withStack("htmx"),
	) {
		out, err := renderStandardsFile(f.src, data)
		if err != nil {
			t.Fatalf("%s: %v", f.src, err)
		}

		body := string(out)
		if m := floating.FindString(body); m != "" {
			t.Errorf("%s pins %q", f.dst, m)
		}

		if m := goAction.FindString(body); strings.HasSuffix(f.src, ".tmpl") && m != "" {
			t.Errorf("%s has the unrendered template action %q", f.dst, m)
		}

		for line := range strings.SplitSeq(body, "\n") {
			m := uses.FindStringSubmatch(line)
			if m == nil {
				continue
			}

			// GitHub's own actions may use a major tag; everyone else needs a
			// full commit SHA with the version in a trailing comment.
			if len(m[3]) != 40 && m[1] != "actions" {
				t.Errorf("%s: %s is not pinned to a commit SHA", f.dst, strings.TrimSpace(line))
			}

			if len(m[3]) == 40 && !strings.Contains(line, "# v") {
				t.Errorf("%s: %s has no version comment", f.dst, strings.TrimSpace(line))
			}
		}
	}
}

func TestAskStandardsAnswers(t *testing.T) {
	t.Parallel()

	for name, tt := range map[string]struct {
		in          string
		interactive bool
		start       standardsOpts
		want        standardsOpts
	}{
		"yes and open source":   {in: "1\n2\n", interactive: true, want: standardsOpts{standards: true, ci: true, license: "mit"}},
		"no and private":        {in: "2\n1\n", interactive: true, want: standardsOpts{}},
		"enter takes defaults":  {in: "\n\n", interactive: true, want: standardsOpts{standards: true, ci: true}},
		"end of input defaults": {in: "", interactive: true, want: standardsOpts{standards: true, ci: true}},
		"not a terminal":        {in: "1\n2\n", interactive: false, want: standardsOpts{}},
		// A flag settles its own question: only the open source one is asked.
		"standards given":  {in: "2\n", interactive: true, start: standardsOpts{standards: true}, want: standardsOpts{standards: true, license: "mit"}},
		"license given":    {in: "2\n", interactive: true, start: standardsOpts{license: "mit"}, want: standardsOpts{license: "mit"}},
		"everything given": {in: "", interactive: true, start: standardsOpts{ci: true, license: "mit"}, want: standardsOpts{ci: true, license: "mit"}},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var out bytes.Buffer

			env := &Env{Stdin: strings.NewReader(tt.in), Stdout: &out, Interactive: tt.interactive}
			got := tt.start
			askStandards(env, &got)

			if got != tt.want {
				t.Errorf("askStandards(%q) = %+v, want %+v", tt.in, got, tt.want)
			}

			if !tt.interactive && out.Len() != 0 {
				t.Errorf("a non-interactive run printed %q", out.String())
			}
		})
	}
}

func TestStandardsDerivedFiles(t *testing.T) {
	t.Parallel()

	dsts := func(o standardsOpts) map[string]bool {
		out := map[string]bool{}
		for _, f := range standardsSet(o) {
			out[f.dst] = true
		}

		return out
	}

	// CI or a license brings the GitHub templates; --standards on a front-end stack brings Biome, not on an API.
	if got := dsts(standardsOpts{ci: true}.withStack("htmx")); !got[".github/pull_request_template.md"] ||
		!got[".github/ISSUE_TEMPLATE/bug_report.yml"] || got["biome.json"] {
		t.Errorf("--ci: %v", got)
	}

	if got := dsts(
		standardsOpts{license: "mit"}.withStack("htmx"),
	); !got[".github/CODEOWNERS"] ||
		!got["CODE_OF_CONDUCT.md"] {
		t.Errorf("--license: %v", got)
	}

	if got := dsts(
		standardsOpts{standards: true}.withStack("htmx"),
	); !got["biome.json"] ||
		got[".github/pull_request_template.md"] {
		t.Errorf("--standards on htmx: %v", got)
	}

	if got := dsts(standardsOpts{standards: true}.withStack("api")); got["biome.json"] {
		t.Errorf("--standards on api wrote biome.json: %v", got)
	}

	if got := dsts(standardsOpts{}.withStack("htmx")); got["biome.json"] || got[".github/CODEOWNERS"] {
		t.Errorf("no options: %v", got)
	}
}

func TestPatchPackageJSONAddsBiomeOnlyWhenAsked(t *testing.T) {
	t.Parallel()

	const pkg = "{\n  \"name\": \"app\",\n  \"scripts\": {\n    \"build\": \"x\"\n  },\n  \"devDependencies\": {\n    \"tailwindcss\": \"4.0.0\"\n  }\n}\n"

	for _, biome := range []bool{false, true} {
		dir := t.TempDir()
		writeTestFile(t, dir, "package.json", pkg)

		env := &Env{Dir: dir, Stdout: io.Discard}
		if err := patchPackageJSON(env, true, biome); err != nil {
			t.Fatal(err)
		}

		raw, err := os.ReadFile(filepath.Join(dir, "package.json"))
		if err != nil {
			t.Fatal(err)
		}

		var parsed struct {
			Scripts         map[string]string `json:"scripts"`
			DevDependencies map[string]string `json:"devDependencies"`
		}
		if err := json.Unmarshal(raw, &parsed); err != nil {
			t.Fatalf("biome=%v: package.json is not valid JSON: %v\n%s", biome, err, raw)
		}

		if _, has := parsed.DevDependencies["@biomejs/biome"]; has != biome {
			t.Errorf("biome=%v: devDependency present = %v", biome, has)
		}

		if biome && (parsed.DevDependencies["@biomejs/biome"] != biomeVersion || parsed.Scripts["lint:js"] == "") {
			t.Errorf("biome=%v: %+v", biome, parsed)
		}

		if parsed.DevDependencies["markdownlint-cli2"] != markdownlintVersion {
			t.Errorf("markdownlint-cli2 = %q", parsed.DevDependencies["markdownlint-cli2"])
		}
	}
}
