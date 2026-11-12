// Command starterdeps keeps the dependencies that "vukem new" scaffolds on
// their latest stable release, never a pre-release (rc, beta, alpha, next,
// canary).
//
//	go run ./internal/tools/starterdeps -check   # list current vs latest, exit 1 when behind
//	go run ./internal/tools/starterdeps -write   # update the pins in place
//
// Pins covered: every dependency of internal/cli/starter/package.json and
// internal/cli/scaffolds/inertia-*/package.json; the Go constants for
// markdownlint-cli2, Biome, React Email, htmx, protobuf, connect, buf, the
// package managers (pm.go) and the contrib tags (stripeVersion,
// studioVersion); every name:tag@sha256 image under internal/cli and
// .config/docker; every "uses: owner/repo@sha # vX.Y.Z" action; and Bun in
// the toolchain Dockerfile (version and both checksums). -write also syncs
// the old versions mentioned in docs/*.md and site/content/docs.
//
// Latest stable is the registry's "latest" tag when it has no pre-release
// suffix, otherwise the highest version without one; for actions it is the
// newest non-draft, non-prerelease release; for images the newest tag of the
// same shape and major line (golang also on its minor, which must match
// go.mod). Rules on top of that:
//
//   - htmx follows the newest 4.x (decision F2 in docs/brainstorming.md; npm
//     keeps the 4 line under the "next" tag) and never jumps major. Updating
//     it also downloads htmx.min.js, replaces the vendored file and updates
//     htmxSHA256.
//   - TypeScript stays on 6.x in the Vue and Svelte starters until vue-tsc and
//     svelte-check list 7 in their typescript peerDependencies. An open-ended
//     ">=5" does not count: it says nothing about 7 being supported.
//   - Bun follows the oven/bun image tag; yarn stays on Classic 1.x.
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
	htmxPkg         = "htmx.org"
	frameworkModule = "github.com/AlexGalhardo/vukem"
	bunImageRepo    = "oven/bun"
	htmxFile        = "internal/cli/starter/resources/public/js/htmx.min.js"
	newGo           = "internal/cli/new.go"
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

	client := newClient(u)
	client.ghToken = os.Getenv("GITHUB_TOKEN")

	rows, err := plan(ctx, client, *root)
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

	docs, err := apply(ctx, client, *root, rows)
	if err != nil {
		fmt.Fprintf(stderr, "starterdeps: %v\n", err)
		return 1
	}

	for _, d := range docs {
		fmt.Fprintf(stdout, "docs updated: %s\n", d)
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
		if groups[g][0].follow != "" {
			continue
		}

		latest, err := c.resolveGroup(ctx, groups[g])
		if err != nil {
			return nil, fmt.Errorf("%s: %w", g, err)
		}

		for _, p := range groups[g] {
			rows = append(rows, row{p, latest[p.pkg]})
		}
	}

	return followRows(ctx, c, pins, rows)
}

// followRows adds the pins that copy the tag version of a Docker pin. A
// follow of "repo#asset" is the SHA-256 of that release asset of the version
// the image carries (the Bun checksums in the toolchain Dockerfile).
func followRows(ctx context.Context, c *client, pins []pin, rows []row) ([]row, error) {
	for i := range pins {
		p := &pins[i]
		if p.follow == "" {
			continue
		}

		repo, asset, hasAsset := strings.Cut(p.follow, "#")

		j := slices.IndexFunc(rows, func(r row) bool { return r.kind == dockerImg && r.pkg == repo })
		if j < 0 {
			return nil, fmt.Errorf("%s follows %s, which is not pinned", p.pkg, repo)
		}

		tag, _, _ := strings.Cut(rows[j].latest, "@")
		latest := tagShape.FindStringSubmatch(tag)[2]

		if hasAsset {
			var err error
			if latest, err = c.bunChecksum(ctx, latest, asset); err != nil {
				return nil, err
			}
		}

		rows = append(rows, row{*p, latest})
	}

	return rows, nil
}

// bunChecksum reads the SHA-256 of a release asset from SHASUMS256.txt.
func (c *client) bunChecksum(ctx context.Context, version, asset string) (string, error) {
	b, err := c.get(ctx, c.urls.bun+"/bun-v"+version+"/SHASUMS256.txt", "")
	if err != nil {
		return "", err
	}

	for line := range strings.SplitSeq(string(b), "\n") {
		sum, name, ok := strings.Cut(strings.TrimSpace(line), "  ")
		if ok && strings.TrimPrefix(name, "*") == asset {
			return sum, nil
		}
	}

	return "", fmt.Errorf("bun %s: no checksum for %s", version, asset)
}

var starterManifests = []string{"internal/cli/starter/package.json"}

// constPins are the versions written as Go constants.
var constPins = []struct {
	pkg, name, file string
	kind            kind
	major           int16
	// follow copies the tag version of that Docker pin (bun), so the CLI, the
	// workflow and the assets image never disagree.
	follow string
	// pattern overrides the Go constant pattern; group 2 is the version.
	pattern string
}{
	{"markdownlint-cli2", "markdownlintVersion", "internal/cli/standards.go", npmPkg, 0, "", ""},
	{"@biomejs/biome", "biomeVersion", "internal/cli/standards.go", npmPkg, 0, "", ""},
	{"react-email", "reactEmailCLIVersion", "internal/cli/generate.go", npmPkg, 0, "", ""},
	{"@react-email/components", "reactEmailComponentsVersion", "internal/cli/generate.go", npmPkg, 0, "", ""},
	{htmxPkg, "htmxVersion", newGo, npmPkg, 4, "", ""},
	{"google.golang.org/protobuf", "protobufVersion", "internal/cli/rpc.go", goMod, 0, "", ""},
	{"connectrpc.com/connect", "connectVersion", "internal/cli/rpc.go", goMod, 0, "", ""},
	{"github.com/bufbuild/buf", "bufVersion", "internal/cli/rpc.go", goMod, 0, "", ""},
	{"npm", "npmVersion", "internal/cli/pm.go", npmPkg, 0, "", ""},
	{"pnpm", "pnpmVersion", "internal/cli/pm.go", npmPkg, 0, "", ""},
	// yarn stays on Classic (1.x): the generated Dockerfile installs with
	// --frozen-lockfile, which Yarn 2+ replaced with --immutable.
	{"yarn", "yarnVersion", "internal/cli/pm.go", npmPkg, 1, "", ""},
	{"bun", "bunVersion", "internal/cli/pm.go", npmPkg, 0, bunImageRepo, ""},
	{
		"bun",
		"bun-v",
		"internal/cli/standards/workflows/ci.yml.tmpl",
		npmPkg,
		0,
		bunImageRepo,
		`(bun-v)([0-9][^"]*)(")`,
	},
	{"bun", "BUN_VERSION", ".config/docker/vukem.Dockerfile", npmPkg, 0, "oven/bun", `(ARG BUN_VERSION=)([^\s]+)()`},
	{
		"bun-linux-x64.zip",
		"BUN_SHA256_AMD64",
		".config/docker/vukem.Dockerfile",
		npmPkg,
		0,
		"oven/bun#bun-linux-x64.zip",
		`(ARG BUN_SHA256_AMD64=)([^\s]+)()`,
	},
	{
		"bun-linux-aarch64.zip",
		"BUN_SHA256_ARM64",
		".config/docker/vukem.Dockerfile",
		npmPkg,
		0,
		"oven/bun#bun-linux-aarch64.zip",
		`(ARG BUN_SHA256_ARM64=)([^\s]+)()`,
	},
	{frameworkModule + "/contrib/stripe", "stripeVersion", "internal/cli/billing.go", goMod, 0, "", ""},
	{frameworkModule + "/contrib/studio", "studioVersion", "internal/cli/studio.go", goMod, 0, "", ""},
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
		if c.pattern != "" {
			re = regexp.MustCompile(c.pattern)
		}

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
				follow:  c.follow,
				re:      re,
			},
		)
	}

	images, err := refPins(root, imageRef, 1, 2, dockerImg)
	if err != nil {
		return nil, err
	}

	if err := checkGoMinor(root, images); err != nil {
		return nil, err
	}

	actions, err := refPins(root, actionRef, 2, 3, ghAction)
	if err != nil {
		return nil, err
	}

	return append(append(pins, images...), actions...), nil
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
func apply(ctx context.Context, c *client, root string, rows []row) ([]string, error) {
	for i := range rows {
		r := &rows[i]
		if !r.behind() {
			continue
		}

		if err := replaceAll(root, r.file, r.re, r.latest); err != nil {
			return nil, fmt.Errorf("%s: %w", r.pkg, err)
		}

		if r.pkg == htmxPkg {
			if err := vendorHTMX(ctx, c, root, r.latest); err != nil {
				return nil, err
			}
		}
	}

	return syncDocs(root, rows)
}

func replaceAll(root, file string, re *regexp.Regexp, version string) error {
	text, err := read(root, file)
	if err != nil {
		return err
	}

	idx := re.FindAllStringSubmatchIndex(text, -1)
	if len(idx) == 0 {
		return fmt.Errorf("%s: no match of %s", file, re)
	}

	for i := range slices.Backward(idx) {
		text = text[:idx[i][4]] + version + text[idx[i][5]:]
	}

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

	return replaceAll(root, newGo, regexp.MustCompile(`(\bhtmxSHA256\s*=\s*")([^"]+)(")`), hex.EncodeToString(sum[:]))
}
