// Command starterdeps keeps the dependencies that "vukem new" scaffolds on
// their latest stable release, never a pre-release (rc, beta, alpha, next,
// canary).
//
//	go run ./internal/tools/starterdeps -check   # list current vs latest, exit 1 when behind
//	go run ./internal/tools/starterdeps -write   # update the pins in place
//
// Pins covered: every dependency of internal/cli/starter/package.json and
// internal/cli/scaffolds/inertia-*/package.json, the markdownlint-cli2,
// Biome and React Email versions, and the protobuf, connect and buf versions
// make:proto suggests. Not covered (still bumped by hand or Dependabot): the
// Dockerfile image digests, the pinned GitHub Actions of the generated
// workflows, the package manager versions in pm.go and the framework's own
// contrib tags (stripeVersion, studioVersion).
//
// Latest stable is the registry's "latest" tag when it has no pre-release
// suffix, otherwise the highest version without one. Rules on top of that:
//
//   - htmx follows the newest 4.x (decision F2 in docs/brainstorming.md; npm
//     keeps the 4 line under the "next" tag) and never jumps major. Updating
//     it also downloads htmx.min.js, replaces the vendored file and updates
//     htmxSHA256.
//   - TypeScript stays on 6.x in the Vue and Svelte starters until vue-tsc and
//     svelte-check list 7 in their typescript peerDependencies. An open-ended
//     ">=5" does not count: it says nothing about 7 being supported.
//   - Within one package.json every chosen version must satisfy the
//     peerDependencies of the others, so -write never produces a set that
//     cannot install.
//
// Run from the repository root (or pass -root).
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"text/tabwriter"
)

const (
	htmxPkg  = "htmx.org"
	htmxFile = "internal/cli/starter/resources/public/js/htmx.min.js"
	newGo    = "internal/cli/new.go"
)

func main() {
	os.Exit(run(context.Background(), os.Args[1:], os.Stdout, os.Stderr, defaultURLs))
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer, u urls) int {
	fs := flag.NewFlagSet("starterdeps", flag.ContinueOnError)
	fs.SetOutput(stderr)
	check := fs.Bool("check", false, "list every pin with its latest stable release; exit 1 when one is behind")
	write := fs.Bool("write", false, "update the pins in place")
	root := fs.String("root", ".", "repository root")

	if err := fs.Parse(args); err != nil {
		return 2
	}

	if *check == *write {
		fmt.Fprintln(stderr, "usage: starterdeps -check | -write [-root dir]")
		return 2
	}

	rows, err := plan(ctx, newClient(u), *root)
	if err != nil {
		fmt.Fprintf(stderr, "starterdeps: %v\n", err)
		return 1
	}

	report(stdout, rows)

	behind := slices.ContainsFunc(rows, func(r row) bool { return r.behind() })
	if *check {
		if behind {
			return 1
		}

		return 0
	}

	if err := apply(ctx, newClient(u), *root, rows); err != nil {
		fmt.Fprintf(stderr, "starterdeps: %v\n", err)
		return 1
	}

	return 0
}

type row struct {
	pin
	latest string
}

func (r row) behind() bool { return r.current != r.latest }

func report(w io.Writer, rows []row) {
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "PACKAGE\tFILE\tCURRENT\tLATEST\t")

	for i := range rows {
		r := &rows[i]
		mark := ""
		if r.behind() {
			mark = "behind"
		}

		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", r.pkg, r.file, r.current, r.latest, mark)
	}

	_ = tw.Flush()
}

// plan discovers every pin and resolves its latest stable version.
func plan(ctx context.Context, c *client, root string) ([]row, error) {
	pins, err := discover(root)
	if err != nil {
		return nil, err
	}

	groups := map[string][]pin{}
	order := []string{}

	for _, p := range pins {
		if _, ok := groups[p.group]; !ok {
			order = append(order, p.group)
		}

		groups[p.group] = append(groups[p.group], p)
	}

	var rows []row

	for _, g := range order {
		latest, err := c.resolveGroup(ctx, groups[g])
		if err != nil {
			return nil, fmt.Errorf("%s: %w", g, err)
		}

		for _, p := range groups[g] {
			rows = append(rows, row{p, latest[p.pkg]})
		}
	}

	return rows, nil
}

var starterManifests = []string{"internal/cli/starter/package.json"}

// constPins are the versions written as Go constants.
var constPins = []struct {
	pkg, name, file string
	kind            kind
	major           int
}{
	{"markdownlint-cli2", "markdownlintVersion", "internal/cli/standards.go", npmPkg, 0},
	{"@biomejs/biome", "biomeVersion", "internal/cli/standards.go", npmPkg, 0},
	{"react-email", "reactEmailCLIVersion", "internal/cli/generate.go", npmPkg, 0},
	{"@react-email/components", "reactEmailComponentsVersion", "internal/cli/generate.go", npmPkg, 0},
	{htmxPkg, "htmxVersion", newGo, npmPkg, 4},
	{"google.golang.org/protobuf", "protobufVersion", "internal/cli/rpc.go", goMod, 0},
	{"connectrpc.com/connect", "connectVersion", "internal/cli/rpc.go", goMod, 0},
	{"github.com/bufbuild/buf", "bufVersion", "internal/cli/rpc.go", goMod, 0},
}

func read(root, file string) (string, error) {
	b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(file)))

	return string(b), err
}

func discover(root string) ([]pin, error) {
	manifests := slices.Clone(starterManifests)

	scaffolds, err := filepath.Glob(filepath.Join(root, "internal", "cli", "scaffolds", "inertia-*", "package.json"))
	if err != nil {
		return nil, err
	}

	slices.Sort(scaffolds)

	for _, s := range scaffolds {
		rel, err := filepath.Rel(root, s)
		if err != nil {
			return nil, err
		}

		manifests = append(manifests, filepath.ToSlash(rel))
	}

	var pins []pin

	for _, m := range manifests {
		ps, err := manifestPins(root, m)
		if err != nil {
			return nil, err
		}

		pins = append(pins, ps...)
	}

	for _, c := range constPins {
		re := regexp.MustCompile(`(\b` + c.name + `\s*=\s*")([^"]+)(")`)

		text, err := read(root, c.file)
		if err != nil {
			return nil, err
		}

		m := re.FindStringSubmatch(text)
		if m == nil {
			return nil, fmt.Errorf("%s: %s not found", c.file, c.name)
		}

		pins = append(
			pins,
			pin{
				pkg:     c.pkg,
				kind:    c.kind,
				file:    c.file,
				group:   c.file + ":" + c.name,
				current: m[2],
				major:   c.major,
				re:      re,
			},
		)
	}

	return pins, nil
}

// manifestPins turns every dependency of a package.json into a pin; the
// manifest is one peer group.
func manifestPins(root, file string) ([]pin, error) {
	text, err := read(root, file)
	if err != nil {
		return nil, err
	}

	var m struct {
		Dependencies    map[string]string `json:"dependencies"`
		DevDependencies map[string]string `json:"devDependencies"`
	}
	if err := json.Unmarshal([]byte(text), &m); err != nil {
		return nil, fmt.Errorf("%s: %w", file, err)
	}

	cur := map[string]string{}
	for _, deps := range []map[string]string{m.Dependencies, m.DevDependencies} {
		maps.Copy(cur, deps)
	}

	names := make([]string, 0, len(cur))
	for k := range cur {
		names = append(names, k)
	}

	slices.Sort(names)

	var tools []string

	for _, t := range []string{"vue-tsc", "svelte-check"} {
		if _, ok := cur[t]; ok {
			tools = append(tools, t)
		}
	}

	pins := make([]pin, 0, len(names))

	for _, n := range names {
		p := pin{
			pkg:     n,
			kind:    npmPkg,
			file:    file,
			group:   file,
			current: cur[n],
			re:      regexp.MustCompile(`("` + regexp.QuoteMeta(n) + `"\s*:\s*")([^"]+)(")`),
		}
		if n == "typescript" && len(tools) > 0 {
			p.holdMajor, p.holdUntil = tsHoldMajor, tools
		}

		pins = append(pins, p)
	}

	return pins, nil
}

// apply rewrites every pin that is behind and, for htmx, the vendored file.
func apply(ctx context.Context, c *client, root string, rows []row) error {
	for i := range rows {
		r := &rows[i]
		if !r.behind() {
			continue
		}

		if err := replaceOnce(root, r.file, r.re, r.latest); err != nil {
			return fmt.Errorf("%s: %w", r.pkg, err)
		}

		if r.pkg == htmxPkg {
			if err := vendorHTMX(ctx, c, root, r.latest); err != nil {
				return err
			}
		}
	}

	return nil
}

func replaceOnce(root, file string, re *regexp.Regexp, version string) error {
	text, err := read(root, file)
	if err != nil {
		return err
	}

	idx := re.FindAllStringSubmatchIndex(text, -1)
	if len(idx) != 1 {
		return fmt.Errorf("%s: want exactly one match of %s, got %d", file, re, len(idx))
	}

	m := idx[0]
	text = text[:m[4]] + version + text[m[5]:]

	return writeSource(root, file, []byte(text))
}

// writeSource keeps the 0644 mode of tracked source files.
func writeSource(root, file string, data []byte) error {
	path := filepath.Join(root, filepath.FromSlash(file))

	return os.WriteFile(path, data, 0o644) //nolint:gosec // G306: tracked source file
}

// vendorHTMX replaces the vendored htmx.min.js with the release's file and
// records its hash; TestVendoredHTMX checks the pair.
func vendorHTMX(ctx context.Context, c *client, root, version string) error {
	b, err := c.get(ctx, fmt.Sprintf(c.urls.htmx, version), "")
	if err != nil {
		return err
	}

	if !strings.HasPrefix(string(b), "var htmx=") {
		return errors.New("downloaded htmx.min.js does not look like htmx")
	}

	if err := writeSource(root, htmxFile, b); err != nil {
		return err
	}

	sum := sha256.Sum256(b)

	return replaceOnce(root, newGo, regexp.MustCompile(`(\bhtmxSHA256\s*=\s*")([^"]+)(")`), hex.EncodeToString(sum[:]))
}
