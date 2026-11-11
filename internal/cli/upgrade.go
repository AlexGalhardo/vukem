package cli

import (
	"bytes"
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

// errUpgradeBehind makes `upgrade --check` exit 1 without a usage message.
var errUpgradeBehind = errors.New("the project is behind this CLI: run vukem upgrade")

type moduleVersion struct {
	Path    string `json:"path"`
	Version string `json:"version"`
}

type upgradeReport struct {
	Framework struct {
		Required string `json:"required"`
		CLI      string `json:"cli"`
		State    string `json:"state"`
	} `json:"framework"`
	Modules  []moduleVersion `json:"modules"`
	Scaffold struct {
		Differ []string `json:"differ"`
	} `json:"scaffold"`
}

// scaffoldIgnored are starter files that differ in every project or hold data.
var scaffoldIgnored = []string{"go.mod", "go.sum", "README.md", ".env.example"}

// upgradeCmd reports how the project differs from this CLI. It never rewrites
// a project file: the current starter goes to .vukem/upgrade/starter/ for the
// user to compare, and only a project that is behind gets its go.mod raised.
func upgradeCmd(set *flag.FlagSet) func(context.Context, *Env, []string) error {
	check := set.Bool("check", false, "report only: write nothing, exit 1 when the project is behind")
	asJSON := set.Bool("json", false, "print the report as JSON")

	return func(ctx context.Context, env *Env, args []string) error {
		if len(args) > 0 {
			return usagef("upgrade takes no arguments")
		}

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

		starter, cleanup, err := renderReferenceStarter(env, mod, *check)
		if err != nil {
			return err
		}

		defer cleanup()

		differ, err := scaffoldDiff(env.path("."), starter)
		if err != nil {
			return err
		}

		report := upgradeReport{Modules: frameworkModules(gomod)}
		report.Framework.Required, report.Framework.CLI, report.Framework.State = required, cli, state
		report.Scaffold.Differ = differ

		if *asJSON {
			enc := json.NewEncoder(env.Stdout)
			enc.SetIndent("", "  ")

			err = enc.Encode(report)
		} else {
			err = printUpgrade(env.Stdout, report, *check)
		}

		if err != nil {
			return err
		}

		if *check {
			if state == "behind" {
				return errUpgradeBehind
			}

			return nil
		}

		return runUpgradeSteps(ctx, env, state, cli, *asJSON)
	}
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
		tmp, err := os.MkdirTemp("", "vukem-upgrade-")
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

func runUpgradeSteps(ctx context.Context, env *Env, state, cli string, quiet bool) error {
	steps := upgradeSteps(state, cli)
	if len(steps) == 0 {
		return nil
	}

	// Keep stdout a clean JSON document.
	e := *env
	if quiet {
		e.Stdout = env.Stderr
	}

	for _, s := range steps {
		if err := tool(ctx, &e, s[0], s[1:]...); err != nil {
			return err
		}
	}

	_, err := fmt.Fprintf(e.Stdout, "go.mod now requires %s\n", releaseVersion(cli))

	return err
}

func printUpgrade(w io.Writer, r upgradeReport, check bool) error {
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

	if len(r.Scaffold.Differ) == 0 {
		fmt.Fprintf(&b, "scaffold: no file differs from the %s starter\n", f.CLI)
	} else {
		fmt.Fprintf(&b, "scaffold: %d file(s) differ from the %s starter\n", len(r.Scaffold.Differ), f.CLI)

		for _, p := range r.Scaffold.Differ {
			fmt.Fprintf(&b, "  %s\n", p)
		}

		if !check {
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

// upgradeSteps returns the commands to run for a project in state: only a
// project that is behind gets its requirement raised.
func upgradeSteps(state, cli string) [][]string {
	if state != "behind" {
		return nil
	}

	return [][]string{
		{"go", "get", frameworkModule + "@" + releaseVersion(cli)},
		{"go", "mod", "tidy"},
	}
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
