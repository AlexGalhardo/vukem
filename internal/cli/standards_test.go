package cli

import (
	"os"
	"path/filepath"
	"regexp"
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
		".githooks/commit-msg", ".githooks/pre-commit",
	},
	"--license": {"LICENSE", "CONTRIBUTING.md", "CODE_OF_CONDUCT.md", "SECURITY.md"},
	"--ci":      {".github/workflows/ci.yml", ".github/dependabot.yml"},
	"--deploy":  {".railway/railway.ts"},
	"--docker":  {"compose.yaml"},
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
				"LICENSE", ".github/workflows/ci.yml", ".railway/railway.ts", "compose.yaml",
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
			not:  []string{"CHANGELOG.md", ".githooks/commit-msg"},
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
		standardsOpts{standards: true, license: "mit", ci: true, deploy: "railway", docker: true},
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
