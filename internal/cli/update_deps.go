package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	defaultGoProxy     = "https://proxy.golang.org"
	defaultNPMRegistry = "https://registry.npmjs.org"
	// maxRegistryBody bounds one registry response: the abbreviated packument of a big package is a few MiB.
	maxRegistryBody = 32 << 20
	registryTimeout = 30 * time.Second
	registryWorkers = 8
)

// depChange is one planned dependency move: Kind is go or js.
type depChange struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
	Old  string `json:"old"`
	New  string `json:"new"`
}

// registryGet reads a bounded body from a registry URL.
func registryGet(ctx context.Context, rawURL, accept string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, registryTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, http.NoBody)
	if err != nil {
		return nil, err
	}

	if accept != "" {
		req.Header.Set("Accept", accept)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}

	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: %s", rawURL, resp.Status)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxRegistryBody+1))
	if err != nil {
		return nil, err
	}

	if len(body) > maxRegistryBody {
		return nil, fmt.Errorf("GET %s: response over %d bytes", rawURL, maxRegistryBody)
	}

	return body, nil
}

// stableVersion is a plain major.minor.patch: no pre-release, build or +incompatible suffix.
var stableVersion = regexp.MustCompile(`^v?\d+\.\d+\.\d+$`)

// newest returns the highest stable version of vs that keep accepts (nil accepts all), or "".
func newest(vs []string, keep func(string) bool) string {
	best := ""

	for _, v := range vs {
		if !stableVersion.MatchString(v) || (keep != nil && !keep(v)) {
			continue
		}

		if best == "" || compareVersions(v, best) > 0 {
			best = v
		}
	}

	return best
}

// escapeModule applies the module proxy case encoding: an upper-case letter becomes "!" and its lower case.
func escapeModule(path string) string {
	var b strings.Builder

	for _, r := range path {
		if r >= 'A' && r <= 'Z' {
			b.WriteByte('!')
			b.WriteRune(r + ('a' - 'A'))

			continue
		}

		b.WriteRune(r)
	}

	return b.String()
}

// latestGoVersion asks the module proxy for the newest stable tag of path (its major is part of the path).
func latestGoVersion(ctx context.Context, proxy, path string) (string, error) {
	body, err := registryGet(ctx, proxy+"/"+escapeModule(path)+"/@v/list", "")
	if err != nil {
		return "", err
	}

	return newest(strings.Fields(string(body)), nil), nil
}

type goRequire struct {
	Path, Version string
	Indirect      bool
}

// goRequirements lists every requirement of a go.mod, skipping paths with a replace directive.
func goRequirements(gomod []byte) []goRequire {
	replaced := map[string]bool{}

	var out []goRequire

	for line := range strings.SplitSeq(string(gomod), "\n") {
		code, comment, _ := strings.Cut(line, "//")
		code = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(code), "require "))

		if left, _, ok := strings.Cut(code, "=>"); ok {
			if f := strings.Fields(strings.TrimPrefix(left, "replace ")); len(f) > 0 {
				replaced[f[0]] = true
			}

			continue
		}

		if f := strings.Fields(code); len(f) == 2 && strings.HasPrefix(f[1], "v") {
			out = append(out, goRequire{f[0], f[1], strings.Contains(comment, "indirect")})
		}
	}

	return slices.DeleteFunc(out, func(r goRequire) bool { return replaced[r.Path] })
}

func isFrameworkSubmodule(path string) bool {
	return strings.HasPrefix(path, frameworkModule+"/drivers/") || strings.HasPrefix(path, frameworkModule+"/contrib/")
}

// forEachParallel runs fn for every index in a small worker pool and returns the last error.
func forEachParallel(n int, fn func(i int) error) error {
	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		fail error
		sem  = make(chan struct{}, registryWorkers)
	)

	for i := range n {
		sem <- struct{}{}

		wg.Go(func() {
			defer func() { <-sem }()

			if err := fn(i); err != nil {
				mu.Lock()
				fail = err
				mu.Unlock()
			}
		})
	}

	wg.Wait()

	return fail
}

// planGoUpdates moves the framework submodules and every other direct requirement to its newest stable tag.
// The core framework module follows the CLI and is handled by the caller.
func planGoUpdates(ctx context.Context, proxy string, gomod []byte) ([]depChange, error) {
	var reqs []goRequire

	for _, r := range goRequirements(gomod) {
		if r.Path != frameworkModule && (!r.Indirect || isFrameworkSubmodule(r.Path)) {
			reqs = append(reqs, r)
		}
	}

	latest := make([]string, len(reqs))

	err := forEachParallel(len(reqs), func(i int) (err error) {
		latest[i], err = latestGoVersion(ctx, proxy, reqs[i].Path)

		return err
	})
	if err != nil {
		return nil, err
	}

	out := []depChange{}

	for i, r := range reqs {
		if latest[i] != "" && compareVersions(latest[i], r.Version) > 0 {
			out = append(out, depChange{"go", r.Path, r.Version, latest[i]})
		}
	}

	return out, nil
}

type npmVersionInfo struct {
	PeerDependencies map[string]string `json:"peerDependencies"`
}

type npmPackument struct {
	Versions map[string]npmVersionInfo `json:"versions"`
}

func fetchPackument(ctx context.Context, registry, name string) (npmPackument, error) {
	var p npmPackument

	body, err := registryGet(ctx, registry+"/"+url.PathEscape(name), "application/vnd.npm.install-v1+json")
	if err != nil {
		return p, err
	}

	if err := json.Unmarshal(body, &p); err != nil {
		return p, fmt.Errorf("npm package %s: %w", name, err)
	}

	return p, nil
}

func (p npmPackument) versions() []string {
	out := make([]string, 0, len(p.Versions))
	for v := range p.Versions {
		out = append(out, v)
	}

	return out
}

// jsMajorPins keeps a package on one major line (decision F2: htmx follows 4.x).
var jsMajorPins = map[string]int{"htmx.org": 4}

// tsCheckers are the tools whose peerDependencies cap the TypeScript version.
var tsCheckers = []string{"vue-tsc", "svelte-check"}

var depSpec = regexp.MustCompile(`^[\^~]?\d+\.\d+\.\d+$`)

// packageJSON holds the dependency sections of a package.json that registry updates may touch.
type packageJSON struct {
	Dependencies         map[string]string `json:"dependencies"`
	DevDependencies      map[string]string `json:"devDependencies"`
	OptionalDependencies map[string]string `json:"optionalDependencies"`
}

// specs returns name -> spec for every dependency written as a plain (optionally ^ or ~) version;
// workspace, file, git, tag and range specs are left alone.
func (p packageJSON) specs() map[string]string {
	out := map[string]string{}

	for _, m := range []map[string]string{p.Dependencies, p.DevDependencies, p.OptionalDependencies} {
		for name, spec := range m {
			if depSpec.MatchString(spec) {
				out[name] = spec
			}
		}
	}

	return out
}

// planJSUpdates resolves the newest stable version of every dependency in pkg.
func planJSUpdates(ctx context.Context, registry string, pkg packageJSON) ([]depChange, error) {
	specs := pkg.specs()
	names := make([]string, 0, len(specs))

	for n := range specs {
		names = append(names, n)
	}

	slices.Sort(names)

	packs := make([]npmPackument, len(names))

	err := forEachParallel(len(names), func(i int) (err error) {
		packs[i], err = fetchPackument(ctx, registry, names[i])

		return err
	})
	if err != nil {
		return nil, err
	}

	target := map[string]string{}

	for i, n := range names {
		pin, pinned := jsMajorPins[n]

		target[n] = newest(packs[i].versions(), func(v string) bool {
			return !pinned || versionParts(v)[0] == pin
		})
	}

	if err := capTypeScript(names, packs, target); err != nil {
		return nil, err
	}

	out := []depChange{}

	for _, n := range names {
		old, to := specs[n], target[n]
		// Never downgrade, and rewrite a ^ or ~ range to the exact version even when it is the same release.
		if to == "" || old == to || compareVersions(to, strings.TrimLeft(old, "^~")) < 0 {
			continue
		}

		out = append(out, depChange{"js", n, old, to})
	}

	return out, nil
}

// capTypeScript lowers target["typescript"] to the newest version every present checker (vue-tsc, svelte-check)
// accepts in its peerDependencies; it fails when none does.
func capTypeScript(names []string, packs []npmPackument, target map[string]string) error {
	ts := slices.Index(names, "typescript")
	if ts < 0 {
		return nil
	}

	var ranges []string

	for _, c := range tsCheckers {
		if i := slices.Index(names, c); i >= 0 && target[c] != "" {
			if r := packs[i].Versions[target[c]].PeerDependencies["typescript"]; r != "" {
				ranges = append(ranges, r)
			}
		}
	}

	if len(ranges) == 0 {
		return nil
	}

	best := newest(packs[ts].versions(), func(v string) bool {
		return slices.IndexFunc(ranges, func(r string) bool { return !satisfies(v, r) }) < 0
	})
	if best == "" {
		return fmt.Errorf("no stable typescript satisfies the peer range(s) %s", strings.Join(ranges, ", "))
	}

	target["typescript"] = best

	return nil
}

// satisfies reports whether a stable version matches an npm range: "||" alternatives of space-separated
// comparators (>=, >, <=, <, =, ^, ~, x-ranges).
// shortcut: no hyphen ranges and no pre-release rules; add them if a peer range needs them.
func satisfies(version, rng string) bool {
	v := versionParts(version)

	for alt := range strings.SplitSeq(rng, "||") {
		if slices.IndexFunc(strings.Fields(alt), func(c string) bool { return !comparatorMatches(v, c) }) < 0 {
			return true
		}
	}

	return false
}

// comparatorMatches checks one comparator; a partial version ("1.2", "1.x") spans its whole range.
func comparatorMatches(v []int, c string) bool {
	op := ""

	for _, o := range []string{">=", "<=", ">", "<", "=", "^", "~"} {
		if rest, ok := strings.CutPrefix(c, o); ok {
			op, c = o, rest

			break
		}
	}

	var parts []int // numeric prefix: a wildcard ends it

	for s := range strings.SplitSeq(strings.TrimPrefix(c, "v"), ".") {
		n, err := strconv.Atoi(s)
		if err != nil {
			break
		}

		parts = append(parts, n)
	}

	lo := append(slices.Clone(parts), 0, 0, 0)[:3] // lowest version of the range
	hi := nextAfter(parts)                         // first version past it

	switch op {
	case ">=":
		return slices.Compare(v, lo) >= 0
	case ">":
		return slices.Compare(v, hi) >= 0
	case "<=":
		return slices.Compare(v, hi) < 0
	case "<":
		return slices.Compare(v, lo) < 0
	case "^":
		return slices.Compare(v, lo) >= 0 && slices.Compare(v, caretCeiling(lo)) < 0
	case "~":
		return slices.Compare(v, lo) >= 0 && slices.Compare(v, nextAfter(parts[:min(len(parts), 2)])) < 0
	}

	return slices.Compare(v, lo) >= 0 && slices.Compare(v, hi) < 0
}

// nextAfter returns the first version past a (partial) version: [1 2] -> 1.3.0, [1] -> 2.0.0, [1 2 3] -> 1.2.4,
// [] (a wildcard) -> a version above any real one.
func nextAfter(p []int) []int {
	if len(p) == 0 {
		return []int{1 << 30, 0, 0}
	}

	out := append(slices.Clone(p), 0, 0, 0)[:3]
	out[len(p)-1]++

	for j := len(p); j < 3; j++ {
		out[j] = 0
	}

	return out
}

func caretCeiling(lo []int) []int {
	switch {
	case lo[0] > 0:
		return []int{lo[0] + 1, 0, 0}
	case lo[1] > 0:
		return []int{0, lo[1] + 1, 0}
	}

	return []int{0, 0, lo[2] + 1}
}

// jsInstaller maps the project's lockfile to its package manager's install command; npm without a lockfile.
func jsInstaller(env *Env) []string {
	for _, l := range []struct{ file, cmd string }{
		{"bun.lock", "bun"},
		{"bun.lockb", "bun"},
		{"pnpm-lock.yaml", "pnpm"},
		{"yarn.lock", "yarn"},
		{"package-lock.json", "npm"},
	} {
		if _, err := os.Stat(env.path(l.file)); err == nil {
			return []string{l.cmd, "install"}
		}
	}

	return []string{"npm", "install"}
}

// rewritePackageJSON sets each changed dependency to its exact new version, keeping the file's formatting.
func rewritePackageJSON(raw []byte, changes []depChange) []byte {
	text := string(raw)

	for _, c := range changes {
		re := regexp.MustCompile(`("` + regexp.QuoteMeta(c.Name) + `"\s*:\s*)"` + regexp.QuoteMeta(c.Old) + `"`)
		text = re.ReplaceAllString(text, `${1}"`+c.New+`"`)
	}

	return []byte(text)
}
