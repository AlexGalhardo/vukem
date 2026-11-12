package cli

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// errUpdateBehind makes `update --check` exit 1 without a usage message.
var errUpdateBehind = errors.New("the project is behind: run vukem update")

type moduleVersion struct {
	Path    string `json:"path"`
	Version string `json:"version"`
}

type updateReport struct {
	Framework struct {
		Required string `json:"required"`
		CLI      string `json:"cli"`
		State    string `json:"state"`
	} `json:"framework"`
	Modules  []moduleVersion `json:"modules"`
	Scaffold struct {
		Differ []string `json:"differ"`
	} `json:"scaffold"`
	Changes []depChange `json:"changes"`
}

// scaffoldIgnored are starter files that differ in every project or hold data.
var scaffoldIgnored = []string{"go.mod", "go.sum", "README.md", ".env.example"}

// updateCmd moves the project to this CLI: the framework requirement, the other Go dependencies and the JS
// dependencies. It never rewrites a project file except go.mod, go.sum, package.json and the lockfile: the
// current starter goes to .vukem/upgrade/starter/ for the user to compare.
func updateCmd(set *flag.FlagSet) func(context.Context, *Env, []string) error {
	check := set.Bool("check", false, "report only: write nothing, exit 1 when something is behind")
	dryRun := set.Bool("dry-run", false, "list every change (old -> new) and write nothing")
	goOnly := set.Bool("go-only", false, "update Go dependencies only")
	jsOnly := set.Bool("js-only", false, "update JS dependencies only")
	asJSON := set.Bool("json", false, "print the report as JSON")

	return func(ctx context.Context, env *Env, args []string) error {
		if len(args) > 0 {
			return usagef("update takes no arguments")
		}

		if *goOnly && *jsOnly {
			return usagef("-go-only and -js-only exclude each other")
		}

		o := updateOpts{readOnly: *check || *dryRun, goDeps: !*jsOnly, jsDeps: !*goOnly}

		mod, err := modulePath(env)
		if err != nil {
			return err
		}

		gomod, err := os.ReadFile(env.path("go.mod"))
		if err != nil {
			return err
		}

		cli := version(env)

		required, state, err := frameworkState(gomod, cli)
		if err != nil {
			return err
		}

		changes, err := planUpdates(ctx, env, gomod, state, cli, o)
		if err != nil {
			return err
		}

		report := updateReport{Modules: frameworkModules(gomod), Changes: changes}
		report.Framework.Required, report.Framework.CLI, report.Framework.State = required, cli, state
		report.Scaffold.Differ = []string{}

		if !*dryRun {
			differ, cleanup, err := scaffoldReport(env, mod, *check)
			if err != nil {
				return err
			}

			defer cleanup()

			report.Scaffold.Differ = differ
		}

		if err := writeUpdateReport(env.Stdout, report, o.readOnly, *dryRun, *asJSON); err != nil {
			return err
		}

		if o.readOnly {
			if *check && (state == "behind" || len(changes) > 0) {
				return errUpdateBehind
			}

			return nil
		}

		return runUpdateSteps(ctx, env, report, o, *asJSON)
	}
}

// scaffoldReport renders the reference starter and lists the files that differ; cleanup removes a temporary copy.
func scaffoldReport(env *Env, mod string, check bool) ([]string, func(), error) {
	starter, cleanup, err := renderReferenceStarter(env, mod, check)
	if err != nil {
		return nil, nil, err
	}

	differ, err := scaffoldDiff(env.path("."), starter)
	if err != nil {
		cleanup()

		return nil, nil, err
	}

	return differ, cleanup, nil
}

func writeUpdateReport(w io.Writer, report updateReport, readOnly, dryRun, asJSON bool) error {
	if !asJSON {
		return printUpdate(w, report, readOnly, dryRun)
	}

	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")

	return enc.Encode(report)
}

type updateOpts struct{ readOnly, goDeps, jsDeps bool }

// planUpdates lists every dependency move: the framework core (to the CLI's version), the Go modules and the JS
// packages. It reads the module proxy and the npm registry, never the project's files beyond go.mod and package.json.
func planUpdates(ctx context.Context, env *Env, gomod []byte, state, cli string, o updateOpts) ([]depChange, error) {
	changes := []depChange{}

	if o.goDeps {
		if state == "behind" {
			required, _, _ := frameworkState(gomod, cli)
			changes = append(changes, depChange{"go", frameworkModule, required, releaseVersion(cli)})
		}

		more, err := planGoUpdates(ctx, cmp.Or(env.goProxy, defaultGoProxy), gomod)
		if err != nil {
			return nil, fmt.Errorf("go dependencies: %w", err)
		}

		changes = append(changes, more...)
	}

	raw, err := os.ReadFile(env.path("package.json"))
	if !o.jsDeps || errors.Is(err, fs.ErrNotExist) {
		return changes, nil
	}

	if err != nil {
		return nil, err
	}

	var pkg packageJSON
	if err := json.Unmarshal(raw, &pkg); err != nil {
		return nil, fmt.Errorf("package.json: %w", err)
	}

	more, err := planJSUpdates(ctx, cmp.Or(env.npmRegistry, defaultNPMRegistry), pkg)
	if err != nil {
		return nil, fmt.Errorf("js dependencies: %w", err)
	}

	return append(changes, more...), nil
}

// renderReferenceStarter renders the current starter into a temporary
// directory (check) or into .vukem/upgrade/starter/, and returns its cleanup.
func renderReferenceStarter(env *Env, mod string, check bool) (string, func(), error) {
	abs, err := filepath.Abs(env.path("."))
	if err != nil {
		return "", nil, err
	}

	data := starterData{Name: filepath.Base(abs), Module: mod}

	if check {
		tmp, err := os.MkdirTemp("", "vukem-update-")
		if err != nil {
			return "", nil, err
		}

		cleanup := func() { _ = os.RemoveAll(tmp) }

		if err := writeStarter(tmp, data); err != nil {
			cleanup()

			return "", nil, err
		}

		return tmp, cleanup, nil
	}

	dir := env.path(filepath.Join(".vukem", "upgrade", "starter"))
	if err := os.RemoveAll(dir); err != nil {
		return "", nil, err
	}

	if err := writeStarter(dir, data); err != nil {
		return "", nil, err
	}

	return dir, func() {}, nil
}

// runUpdateSteps applies the plan: go get and go mod tidy, then package.json and the package manager's install,
// then go build. A project with nothing to change runs no command and writes nothing.
func runUpdateSteps(ctx context.Context, env *Env, r updateReport, o updateOpts, quiet bool) error {
	if len(r.Changes) == 0 {
		return nil
	}

	// Keep stdout a clean JSON document.
	e := *env
	if quiet {
		e.Stdout = env.Stderr
	}

	for _, s := range updateSteps(r.Changes) {
		if err := tool(ctx, &e, s[0], s[1:]...); err != nil {
			return err
		}
	}

	if slices.ContainsFunc(r.Changes, func(c depChange) bool { return c.Kind == "js" }) {
		if err := updatePackageJSON(ctx, &e, r.Changes); err != nil {
			return err
		}
	}

	if o.goDeps {
		if err := tool(ctx, &e, "go", "build", "./..."); err != nil {
			return err
		}
	}

	_, err := fmt.Fprintln(e.Stdout, "next: go test ./...")

	return err
}

func updatePackageJSON(ctx context.Context, env *Env, changes []depChange) error {
	root, err := os.OpenRoot(env.Dir)
	if err != nil {
		return err
	}

	defer root.Close()

	raw, err := root.ReadFile("package.json")
	if err != nil {
		return err
	}

	info, err := root.Stat("package.json")
	if err != nil {
		return err
	}

	if err := root.WriteFile("package.json", rewritePackageJSON(raw, changes), info.Mode().Perm()); err != nil {
		return err
	}

	install := jsInstaller(env)

	return tool(ctx, env, install[0], install[1:]...)
}

func printUpdate(w io.Writer, r updateReport, readOnly, dryRun bool) error {
	var b strings.Builder

	f := r.Framework

	switch f.State {
	case "replaced":
		b.WriteString("framework: replaced by a local checkout (not compared)\n")
	case "devel":
		fmt.Fprintf(&b, "framework: %s in go.mod, CLI is a development build (not compared)\n", f.Required)
	default:
		note := map[string]string{
			"up_to_date": "up to date",
			"behind":     "behind",
			"ahead":      "ahead of this CLI: install a newer vukem",
		}[f.State]
		fmt.Fprintf(&b, "framework: %s in go.mod, CLI is %s (%s)\n", f.Required, f.CLI, note)
	}

	if len(r.Modules) > 0 {
		b.WriteString("modules:\n")

		for _, m := range r.Modules {
			fmt.Fprintf(&b, "  %s %s\n", m.Path, m.Version)
		}
	}

	if len(r.Changes) == 0 {
		b.WriteString("dependencies: up to date\n")
	} else {
		fmt.Fprintf(&b, "dependencies: %d to change\n", len(r.Changes))

		for _, c := range r.Changes {
			fmt.Fprintf(&b, "  %s %s %s -> %s\n", c.Kind, c.Name, c.Old, c.New)
		}
	}

	if dryRun {
		_, err := io.WriteString(w, b.String())

		return err
	}

	if len(r.Scaffold.Differ) == 0 {
		fmt.Fprintf(&b, "scaffold: no file differs from the %s starter\n", f.CLI)
	} else {
		fmt.Fprintf(&b, "scaffold: %d file(s) differ from the %s starter\n", len(r.Scaffold.Differ), f.CLI)

		for _, p := range r.Scaffold.Differ {
			fmt.Fprintf(&b, "  %s\n", p)
		}

		if !readOnly {
			b.WriteString("compare one with: git diff --no-index .vukem/upgrade/starter/<file> <file>\n")
		}
	}

	fmt.Fprintf(&b, "docs: %s\n", starterUpgradeDocs)

	_, err := io.WriteString(w, b.String())

	return err
}

// goModRequires yields the requirement lines of a go.mod as (path, version),
// skipping replace directives and anything with "=>".
func goModRequires(gomod []byte) [][2]string {
	var out [][2]string

	for line := range strings.SplitSeq(string(gomod), "\n") {
		line, _, _ = strings.Cut(line, "//")
		line = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "require "))

		if strings.Contains(line, "=>") || strings.HasPrefix(line, "replace ") {
			continue
		}

		if f := strings.Fields(line); len(f) == 2 && strings.HasPrefix(f[1], "v") {
			out = append(out, [2]string{f[0], f[1]})
		}
	}

	return out
}

func hasFrameworkReplace(gomod []byte) bool {
	for line := range strings.SplitSeq(string(gomod), "\n") {
		line = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "replace "))
		if strings.HasPrefix(line, frameworkModule+" ") && strings.Contains(line, "=>") {
			return true
		}
	}

	return false
}

// frameworkState compares the framework version the project requires with the
// CLI's. state is up_to_date, behind, ahead, replaced or devel.
func frameworkState(gomod []byte, cli string) (required, state string, err error) {
	for _, r := range goModRequires(gomod) {
		if r[0] == frameworkModule {
			required = r[1]
		}
	}

	if required == "" {
		return "", "", fmt.Errorf("this project does not require %s", frameworkModule)
	}

	switch {
	case hasFrameworkReplace(gomod):
		return required, "replaced", nil
	case releaseVersion(cli) == "latest":
		return required, "devel", nil
	}

	switch c := compareVersions(required, cli); {
	case c < 0:
		return required, "behind", nil
	case c > 0:
		return required, "ahead", nil
	}

	return required, "up_to_date", nil
}

// compareVersions compares major.minor.patch numerically. Pre-release and
// build suffixes are ignored: the framework publishes no release candidates.
func compareVersions(a, b string) int {
	return slices.Compare(versionParts(a), versionParts(b))
}

func versionParts(v string) []int {
	v = strings.TrimPrefix(v, "v")
	v, _, _ = strings.Cut(v, "-")
	v, _, _ = strings.Cut(v, "+")

	parts := make([]int, 3)

	for i, s := range strings.SplitN(v, ".", 3) {
		parts[i], _ = strconv.Atoi(s)
	}

	return parts
}

// updateSteps returns the Go commands for the planned changes: one go get for every module, then go mod tidy.
func updateSteps(changes []depChange) [][]string {
	get := []string{"go", "get"}

	for _, c := range changes {
		if c.Kind == "go" {
			get = append(get, c.Name+"@"+c.New)
		}
	}

	if len(get) == 2 {
		return nil
	}

	return [][]string{get, {"go", "mod", "tidy"}}
}

// frameworkModules lists the drivers/* and contrib/* modules a project requires.
func frameworkModules(gomod []byte) []moduleVersion {
	mods := []moduleVersion{}

	for _, r := range goModRequires(gomod) {
		if strings.HasPrefix(r[0], frameworkModule+"/drivers/") ||
			strings.HasPrefix(r[0], frameworkModule+"/contrib/") {
			mods = append(mods, moduleVersion{Path: r[0], Version: r[1]})
		}
	}

	return mods
}

// scaffoldDiff lists, sorted and with forward slashes, the starter files that
// also exist in the project with different content (line endings ignored).
func scaffoldDiff(projectDir, starterDir string) ([]string, error) {
	// Reads go through os.Root so a link inside either tree cannot point the comparison elsewhere.
	starter, err := os.OpenRoot(starterDir)
	if err != nil {
		return nil, err
	}

	defer starter.Close()

	project, err := os.OpenRoot(projectDir)
	if err != nil {
		return nil, err
	}

	defer project.Close()

	differ := []string{}

	err = fs.WalkDir(starter.FS(), ".", func(rel string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}

		if slices.Contains(scaffoldIgnored, rel) || strings.HasPrefix(rel, "storage/") {
			return nil
		}

		mine, err := project.ReadFile(rel)
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}

		if err != nil {
			return err
		}

		theirs, err := starter.ReadFile(rel)
		if err != nil {
			return err
		}

		if !bytes.Equal(normalizeEOL(mine), normalizeEOL(theirs)) {
			differ = append(differ, rel)
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	slices.Sort(differ)

	return differ, nil
}

func normalizeEOL(b []byte) []byte { return bytes.ReplaceAll(b, []byte("\r\n"), []byte("\n")) }
