package cli

import (
	"bytes"
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"text/template"
)

// standardsFS holds the project-standard templates: editor and lint
// configuration, the changelog and its git-cliff config, the git hooks, the
// open-source markdown files, the CI workflow and the deploy configuration.
//
//go:embed all:standards
var standardsFS embed.FS

// markdownlintVersion is the exact markdownlint-cli2 release the lint:md
// script and the devDependency pin.
const markdownlintVersion = "0.23.3"

// licenses maps the SPDX identifiers --license accepts to their template.
var licenses = map[string]string{"mit": "MIT"}

// standardsOpts is what the developer selected on the command line. Only
// .editorconfig is unconditional: it describes the indentation the starter
// already uses and costs nothing, while every other set below is a project
// decision (publishing, CI minutes, a platform, a container runtime).
type standardsOpts struct {
	standards bool   // --standards: markdownlint, changelog, git-cliff, git hooks, SemVer
	license   string // --license: SPDX id; LICENSE plus the community health files
	ci        bool   // --ci: the CI workflow and Dependabot
	deploy    string // --deploy: the platform configuration ("railway")
	docker    bool   // --docker: compose.yaml and its .env block
}

// standardsData is the template data for the standards files.
type standardsData struct {
	Name string
	Year int
	// Holder is the copyright holder: the GitHub owner from the module path
	// when there is one, else the project name.
	Holder string
	// Owner and Repo are set when the module path is a github.com path.
	Owner string
	Repo  string
	// License is the display name of the chosen license ("MIT"), or empty.
	License string
	// Deploy is the chosen platform, or empty.
	Deploy string

	Standards bool
	CI        bool
	Docker    bool

	Database bool // the stack has a database (not --minimal)
	Assets   bool // the stack builds front-end assets (not --api)
	Templ    bool
}

// standardsFile is one template and where it lands in the project.
type standardsFile struct {
	src, dst string
	exec     bool
}

func newStandardsData(sd starterData, kind string, o standardsOpts, year int) standardsData {
	d := standardsData{
		Name: sd.Name, Year: year, Holder: sd.Name,
		License: licenses[o.license], Deploy: o.deploy,
		Standards: o.standards, CI: o.ci, Docker: o.docker,
		Database: kind != "minimal", Assets: kind != "api", Templ: kind == "templ",
	}

	if rest, ok := strings.CutPrefix(sd.Module, "github.com/"); ok {
		if owner, repo, ok := strings.Cut(rest, "/"); ok && owner != "" && repo != "" {
			d.Owner, d.Repo, d.Holder = owner, owner+"/"+repo, owner
		}
	}

	return d
}

// standardsSet lists the files the selected flags write, in the order they
// are reported.
func standardsSet(o standardsOpts) []standardsFile {
	files := []standardsFile{
		{src: "editorconfig", dst: ".editorconfig"},
		{src: "gitattributes", dst: ".gitattributes"},
	}

	if o.standards {
		files = append(files,
			standardsFile{src: "markdownlint-cli2.jsonc", dst: ".markdownlint-cli2.jsonc"},
			standardsFile{src: "CHANGELOG.md.tmpl", dst: "CHANGELOG.md"},
			standardsFile{src: "cliff.toml", dst: ".config/cliff.toml"},
			standardsFile{src: "githooks/commit-msg", dst: ".githooks/commit-msg", exec: true},
			standardsFile{src: "githooks/pre-commit", dst: ".githooks/pre-commit", exec: true},
		)
	}

	if _, ok := licenses[o.license]; ok {
		files = append(files,
			standardsFile{src: "LICENSE-" + o.license + ".tmpl", dst: "LICENSE"},
			standardsFile{src: "CONTRIBUTING.md.tmpl", dst: "CONTRIBUTING.md"},
			standardsFile{src: "CODE_OF_CONDUCT.md.tmpl", dst: "CODE_OF_CONDUCT.md"},
			standardsFile{src: "SECURITY.md.tmpl", dst: "SECURITY.md"},
		)
	}

	if o.ci {
		files = append(files,
			standardsFile{src: "workflows/ci.yml.tmpl", dst: ".github/workflows/ci.yml"},
			standardsFile{src: "dependabot.yml.tmpl", dst: ".github/dependabot.yml"},
		)
	}

	if o.deploy == "railway" {
		files = append(files, standardsFile{src: "railway.ts.tmpl", dst: ".railway/railway.ts"})
	}

	if o.docker {
		files = append(files, standardsFile{src: "compose.yaml.tmpl", dst: "compose.yaml"})
	}

	return files
}

// checkStandards validates the standards flags before anything is written.
func checkStandards(o standardsOpts) error {
	if o.license != "" {
		if _, ok := licenses[o.license]; !ok {
			return usagef("--license must be one of %s, not %q", strings.Join(licenseIDs(), ", "), o.license)
		}
	}

	if o.deploy != "" && o.deploy != "railway" {
		return usagef("--deploy must be railway, not %q", o.deploy)
	}

	return nil
}

func licenseIDs() []string {
	ids := make([]string, 0, len(licenses))
	for id := range licenses {
		ids = append(ids, id)
	}

	slices.Sort(ids)

	return ids
}

// writeStandards renders the selected standards files into the project and
// wires up the pieces that are not a plain file: the lint script in
// package.json, the Compose block in .env.example and the README section
// that lists what was written.
func writeStandards(env *Env, data standardsData, o standardsOpts) error {
	for _, f := range standardsSet(o) {
		if err := renderStandards(env, f, data); err != nil {
			return err
		}
	}

	if o.standards {
		if err := patchPackageJSON(env, data.Assets); err != nil {
			return err
		}
	}

	if o.docker {
		if err := appendStandards(env, ".env.example", "env.docker.tmpl", data); err != nil {
			return err
		}
	}

	return appendStandards(env, "README.md", "readme-section.md.tmpl", data)
}

func renderStandards(env *Env, f standardsFile, data standardsData) error {
	out, err := renderStandardsFile(f.src, data)
	if err != nil {
		return err
	}

	dst := env.path(filepath.FromSlash(f.dst))
	if err := os.MkdirAll(filepath.Dir(dst), 0o750); err != nil {
		return err
	}

	// Git only runs a hook it can execute.
	perm := os.FileMode(0o600)
	if f.exec {
		perm = 0o700
	}

	if err := os.WriteFile(dst, out, perm); err != nil {
		return err
	}

	fmt.Fprintln(env.Stdout, "created", f.dst)

	return nil
}

// appendStandards adds a rendered template to the end of an existing
// project file, skipping it when the file is not there (an API project has
// no .env.example section to extend if the starter changes).
func appendStandards(env *Env, rel, src string, data standardsData) error {
	p := env.path(filepath.FromSlash(rel))

	cur, err := os.ReadFile(p)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}

	if err != nil {
		return err
	}

	out, err := renderStandardsFile(src, data)
	if err != nil {
		return err
	}

	if !bytes.HasSuffix(cur, []byte("\n")) {
		cur = append(cur, '\n')
	}

	return writeFile(p, append(cur, out...))
}

func renderStandardsFile(src string, data standardsData) ([]byte, error) {
	b, err := standardsFS.ReadFile("standards/" + src)
	if err != nil {
		return nil, err
	}

	if !strings.HasSuffix(src, ".tmpl") {
		return b, nil
	}

	t, err := template.New(src).Option("missingkey=error").Parse(string(b))
	if err != nil {
		return nil, fmt.Errorf("standards %s: %w", src, err)
	}

	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return nil, fmt.Errorf("standards %s: %w", src, err)
	}

	return buf.Bytes(), nil
}

// minimalPackageJSON is the package.json an --api project gets with
// --standards: markdownlint is a Node tool, so the lint script needs one.
const minimalPackageJSON = `{
  "private": true,
  "version": "0.1.0",
  "scripts": {
    "lint:md": "markdownlint-cli2 \"**/*.md\""
  },
  "devDependencies": {
    "markdownlint-cli2": "` + markdownlintVersion + `"
  }
}
`

// patchPackageJSON adds the SemVer version and the lint:md script to the
// project's package.json, or writes a minimal one when the stack has no
// front end. Insertions are textual so the rest of the file, including the
// key order a package manager expects, is untouched.
func patchPackageJSON(env *Env, assets bool) error {
	const rel = "package.json"

	p := env.path(rel)

	src, err := os.ReadFile(p)
	if errors.Is(err, fs.ErrNotExist) || !assets {
		if err := writeFile(p, []byte(minimalPackageJSON)); err != nil {
			return err
		}

		fmt.Fprintln(env.Stdout, "created", rel)

		return nil
	}

	if err != nil {
		return err
	}

	for _, ins := range []struct{ anchor, line, skipIf string }{
		{"{\n", `  "version": "0.1.0",`, `"version"`},
		{"  \"scripts\": {\n", `    "lint:md": "markdownlint-cli2 \"**/*.md\"",`, `"lint:md"`},
		{"  \"devDependencies\": {\n", `    "markdownlint-cli2": "` + markdownlintVersion + `",`, `"markdownlint-cli2"`},
	} {
		if bytes.Contains(src, []byte(ins.skipIf)) {
			continue
		}

		i := bytes.Index(src, []byte(ins.anchor))
		if i < 0 {
			return fmt.Errorf("%s: no %q to add %s after", rel, ins.anchor, ins.skipIf)
		}

		at := i + len(ins.anchor)
		src = slices.Concat(src[:at], []byte(ins.line+"\n"), src[at:])
	}

	if err := writeFile(p, src); err != nil {
		return err
	}

	fmt.Fprintln(env.Stdout, "updated", rel)

	return nil
}

// installHooksPath points the new repository at .githooks, so a clone needs
// no hook manager and no install step. It runs after git init and only
// warns on failure, like git init itself.
//
// The hooks are also staged with --chmod=+x: Git ignores the file mode on
// Windows (core.filemode=false), so without it they would be committed as
// 0644 and every clone on Linux or macOS would skip them silently.
func installHooksPath(ctx context.Context, env *Env) {
	if !exists(env, filepath.Join(".githooks", "commit-msg")) {
		return
	}

	for _, args := range [][]string{
		{"config", "core.hooksPath", ".githooks"},
		{"add", "--chmod=+x", ".githooks/commit-msg", ".githooks/pre-commit"},
	} {
		cmd := exec.CommandContext(ctx, "git", args...)
		cmd.Dir = env.Dir

		if out, err := cmd.CombinedOutput(); err != nil {
			fmt.Fprintf(env.Stderr, "git %s failed (continuing): %v\n%s", strings.Join(args, " "), err, out)
			return
		}
	}

	fmt.Fprintln(env.Stdout, "git hooks enabled (core.hooksPath=.githooks)")
}
