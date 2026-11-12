package cli

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
)

const (
	selfUpgradeRepo       = "AlexGalhardo/vukem"
	selfUpgradeAPI        = "https://api.github.com"
	selfUpgradeCLIPkg     = "github.com/AlexGalhardo/vukem/cmd/vukem"
	selfUpgradeTimeout    = 5 * time.Minute
	maxReleaseJSONBytes   = 1 << 20
	maxChecksumsBytes     = 1 << 20
	maxArchiveBytes       = 128 << 20
	maxBinaryBytes        = 256 << 20
	cosignIdentityPattern = `^https://github\.com/AlexGalhardo/vukem/\.github/workflows/release\.yml@refs/tags/v`
	cosignIssuer          = "https://token.actions.githubusercontent.com"
)

// stableTag matches a release tag without a pre-release or build suffix.
var stableTag = regexp.MustCompile(`^v(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)$`)

type releaseInfo struct {
	Tag        string         `json:"tag_name"`
	Draft      bool           `json:"draft"`
	Prerelease bool           `json:"prerelease"`
	Assets     []releaseAsset `json:"assets"`
}

type releaseAsset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
}

// selfUpgrader holds everything vukem upgrade touches outside the release
// data, so tests can fake the GitHub API, the host policy and the process.
type selfUpgrader struct {
	env     *Env
	apiBase string
	client  *http.Client
	// trusted reports whether a URL may be requested (first hop and redirects).
	trusted func(*url.URL) bool
	goos    string
	goarch  string
	exe     func() (string, error)
	// viaGoInstall reports that the binary at exe was put there by go install.
	viaGoInstall func(exe string) bool
	lookPath     func(string) (string, error)
	run          func(ctx context.Context, name string, args ...string) error
}

// trustedGitHubHost allows HTTPS to GitHub and the hosts it serves release
// assets from, and nothing else.
func trustedGitHubHost(u *url.URL) bool {
	h := strings.ToLower(u.Hostname())

	return u.Scheme == "https" && u.Port() == "" &&
		(h == "github.com" || h == "api.github.com" || strings.HasSuffix(h, ".githubusercontent.com"))
}

func newSelfUpgrader(env *Env) *selfUpgrader {
	u := &selfUpgrader{
		env:      env,
		apiBase:  selfUpgradeAPI,
		client:   &http.Client{},
		trusted:  trustedGitHubHost,
		goos:     runtime.GOOS,
		goarch:   runtime.GOARCH,
		lookPath: exec.LookPath,
		run: func(ctx context.Context, name string, args ...string) error {
			return tool(ctx, env, name, args...)
		},
		exe: func() (string, error) {
			p, err := os.Executable()
			if err != nil {
				return "", err
			}

			return filepath.EvalSymlinks(p)
		},
	}
	u.viaGoInstall = func(exe string) bool { return inGoBin(env, exe) }

	return u
}

// inGoBin reports whether exe lives in GOBIN, the first GOPATH entry's bin
// directory or the default ~/go/bin, which is where go install puts it.
func inGoBin(env *Env, exe string) bool {
	var dirs []string

	if v, ok := env.Getenv("GOBIN"); ok && v != "" {
		dirs = append(dirs, v)
	}

	if v, ok := env.Getenv("GOPATH"); ok && v != "" {
		dirs = append(dirs, filepath.Join(filepath.SplitList(v)[0], "bin"))
	} else if home, err := os.UserHomeDir(); err == nil {
		dirs = append(dirs, filepath.Join(home, "go", "bin"))
	}

	// Both sides are resolved: macOS temp dirs live behind /var -> /private/var
	// and Windows runners hand out 8.3 short names that EvalSymlinks expands.
	dir := resolvedDir(filepath.Dir(exe))

	for _, d := range dirs {
		if resolvedDir(d) == dir {
			return true
		}
	}

	return false
}

func resolvedDir(d string) string {
	if r, err := filepath.EvalSymlinks(d); err == nil {
		d = r
	}

	return filepath.Clean(d)
}

// selfUpgradeCommand updates the vukem CLI itself to the latest stable release.
func selfUpgradeCommand(set *flag.FlagSet) func(context.Context, *Env, []string) error {
	check := set.Bool("check", false, "print the current and the latest version and change nothing")
	pin := set.String("version", "", "install this stable release (vX.Y.Z) instead of the latest")

	return func(ctx context.Context, env *Env, args []string) error {
		if len(args) > 0 {
			return usagef("upgrade takes no arguments")
		}

		return newSelfUpgrader(env).upgrade(ctx, *check, *pin)
	}
}

func (u *selfUpgrader) upgrade(ctx context.Context, check bool, pin string) error {
	if pin != "" && !stableTag.MatchString(pin) {
		return usagef("--version must be a stable release like v1.2.3, got %q", pin)
	}

	ctx, cancel := context.WithTimeout(ctx, selfUpgradeTimeout)
	defer cancel()

	exe, err := u.exe()
	if err != nil {
		return fmt.Errorf("locate the running vukem: %w", err)
	}

	if u.goos == "windows" {
		_ = os.Remove(exe + ".old")
	}

	rel, err := u.release(ctx, pin)
	if err != nil {
		return err
	}

	current := version(u.env)
	out := u.env.Stdout

	if check {
		fmt.Fprintf(out, "current: %s\nlatest: %s\n", current, rel.Tag)
		return nil
	}

	if current == rel.Tag {
		fmt.Fprintf(out, "vukem %s is already up to date\n", current)
		return nil
	}

	// Only an explicit --version may go back: "latest" never downgrades a newer
	// release build. devel and pseudo-versions are not releases and still upgrade.
	if pin == "" && stableTag.MatchString(current) && compareVersions(current, rel.Tag) > 0 {
		fmt.Fprintf(out, "vukem %s is newer than the latest release %s; nothing to do\n", current, rel.Tag)
		return nil
	}

	method := "archive"
	if _, lookErr := u.lookPath("go"); lookErr == nil && u.viaGoInstall(exe) {
		method = "go install"
		err = u.run(ctx, "go", "install", selfUpgradeCLIPkg+"@"+rel.Tag)
	} else {
		err = u.installArchive(ctx, rel, exe)
	}

	if err != nil {
		return fmt.Errorf("upgrade vukem to %s: %w", rel.Tag, err)
	}

	fmt.Fprintf(out, "vukem upgraded: %s -> %s (%s)\n", current, rel.Tag, method)

	return u.run(ctx, exe, "version")
}

func (u *selfUpgrader) release(ctx context.Context, pin string) (*releaseInfo, error) {
	endpoint := "/repos/" + selfUpgradeRepo + "/releases/latest"
	if pin != "" {
		endpoint = "/repos/" + selfUpgradeRepo + "/releases/tags/" + pin
	}

	body, err := u.get(ctx, u.apiBase+endpoint, maxReleaseJSONBytes)
	if err != nil {
		return nil, fmt.Errorf("fetch the release: %w", err)
	}

	var rel releaseInfo
	if err := json.Unmarshal(body, &rel); err != nil {
		return nil, fmt.Errorf("decode the release: %w", err)
	}

	if rel.Draft || rel.Prerelease || !stableTag.MatchString(rel.Tag) {
		return nil, fmt.Errorf("release %q is not a stable release", rel.Tag)
	}

	if pin != "" && rel.Tag != pin {
		return nil, fmt.Errorf("asked for %s, got release %q", pin, rel.Tag)
	}

	return &rel, nil
}

func (u *selfUpgrader) installArchive(ctx context.Context, rel *releaseInfo, exe string) error {
	ext, bin := ".tar.gz", "vukem"
	if u.goos == "windows" {
		ext, bin = ".zip", "vukem.exe"
	}

	name := fmt.Sprintf("vukem_%s_%s_%s%s", strings.TrimPrefix(rel.Tag, "v"), u.goos, u.goarch, ext)

	assetURL, ok := rel.asset(name)
	if !ok {
		return fmt.Errorf("release %s has no archive %s", rel.Tag, name)
	}

	sums, err := u.checksums(ctx, rel)
	if err != nil {
		return err
	}

	want, ok := sums[name]
	if !ok {
		return fmt.Errorf("checksums.txt has no entry for %s", name)
	}

	data, err := u.get(ctx, assetURL, maxArchiveBytes)
	if err != nil {
		return fmt.Errorf("download %s: %w", name, err)
	}

	got := sha256.Sum256(data)
	if subtle.ConstantTimeCompare([]byte(hex.EncodeToString(got[:])), []byte(want)) != 1 {
		return fmt.Errorf("checksum mismatch for %s: refusing to install", name)
	}

	binary, err := extractBinary(data, ext, bin)
	if err != nil {
		return err
	}

	return replaceExecutable(exe, binary, u.goos)
}

func (r *releaseInfo) asset(name string) (string, bool) {
	for _, a := range r.Assets {
		if a.Name == name {
			return a.URL, true
		}
	}

	return "", false
}

// checksums downloads checksums.txt, verifies its cosign signature when cosign
// is installed, and returns the lowercase SHA-256 by file name.
func (u *selfUpgrader) checksums(ctx context.Context, rel *releaseInfo) (map[string]string, error) {
	sumsURL, ok := rel.asset("checksums.txt")
	if !ok {
		return nil, fmt.Errorf("release %s has no checksums.txt", rel.Tag)
	}

	body, err := u.get(ctx, sumsURL, maxChecksumsBytes)
	if err != nil {
		return nil, fmt.Errorf("download checksums.txt: %w", err)
	}

	if _, err := u.lookPath("cosign"); err != nil {
		fmt.Fprintln(
			u.env.Stdout,
			"note: cosign not found, skipping the signature check (checksums are still verified)",
		)
	} else if err := u.verifySignature(ctx, rel, body); err != nil {
		return nil, err
	}

	sums := map[string]string{}

	for line := range strings.SplitSeq(string(body), "\n") {
		f := strings.Fields(line)
		if len(f) == 2 {
			sums[strings.TrimPrefix(f[1], "*")] = strings.ToLower(f[0])
		}
	}

	return sums, nil
}

func (u *selfUpgrader) verifySignature(ctx context.Context, rel *releaseInfo, sums []byte) error {
	bundleURL, ok := rel.asset("checksums.txt.sigstore.json")
	if !ok {
		return fmt.Errorf("release %s has no checksums.txt.sigstore.json to verify with cosign", rel.Tag)
	}

	bundle, err := u.get(ctx, bundleURL, maxChecksumsBytes)
	if err != nil {
		return fmt.Errorf("download the cosign bundle: %w", err)
	}

	dir, err := os.MkdirTemp("", "vukem-upgrade-")
	if err != nil {
		return err
	}

	defer func() { _ = os.RemoveAll(dir) }()

	sumsFile, bundleFile := filepath.Join(dir, "checksums.txt"), filepath.Join(dir, "checksums.txt.sigstore.json")
	if err := os.WriteFile(sumsFile, sums, 0o600); err != nil {
		return err
	}

	if err := os.WriteFile(bundleFile, bundle, 0o600); err != nil {
		return err
	}

	err = u.run(ctx, "cosign", "verify-blob", sumsFile, "--bundle", bundleFile,
		"--certificate-identity-regexp", cosignIdentityPattern, "--certificate-oidc-issuer", cosignIssuer)
	if err != nil {
		return fmt.Errorf("cosign verification of checksums.txt failed: %w", err)
	}

	return nil
}

// get fetches an https URL on a trusted host, following redirects only to
// trusted hosts, and refuses a body over limit bytes.
func (u *selfUpgrader) get(ctx context.Context, rawURL string, limit int64) ([]byte, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil || !u.trusted(parsed) {
		return nil, fmt.Errorf("refusing to request untrusted URL %q", rawURL)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, http.NoBody)
	if err != nil {
		return nil, err
	}

	req.Header.Set("User-Agent", "vukem-cli/"+version(u.env))
	req.Header.Set("Accept", "application/vnd.github+json, application/octet-stream")

	if tok, _ := u.env.Getenv("GITHUB_TOKEN"); tok != "" {
		// net/http drops Authorization when a redirect leaves the host.
		req.Header.Set("Authorization", "Bearer "+tok)
	}

	client := *u.client
	client.CheckRedirect = func(r *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return errors.New("stopped after 5 redirects")
		}

		if !u.trusted(r.URL) {
			return fmt.Errorf("refusing redirect to untrusted host %q", r.URL.Host)
		}

		return nil
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}

	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: %s", parsed.Host+parsed.Path, resp.Status)
	}

	return readLimited(resp.Body, limit)
}

func readLimited(r io.Reader, limit int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, err
	}

	if int64(len(data)) > limit {
		return nil, fmt.Errorf("download exceeds the %d byte limit", limit)
	}

	return data, nil
}

// extractBinary returns the regular file named bin from a tar.gz or zip
// archive. Nothing is written using the archive's own paths.
func extractBinary(data []byte, ext, bin string) ([]byte, error) {
	if ext == ".zip" {
		zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			return nil, fmt.Errorf("read archive: %w", err)
		}

		for _, f := range zr.File {
			if f.Mode().IsRegular() && path.Base(f.Name) == bin {
				return readZipFile(f)
			}
		}

		return nil, fmt.Errorf("archive has no %s", bin)
	}

	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("read archive: %w", err)
	}

	tr := tar.NewReader(gz)

	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("archive has no %s", bin)
		}

		if err != nil {
			return nil, fmt.Errorf("read archive: %w", err)
		}

		if h.Typeflag == tar.TypeReg && path.Base(h.Name) == bin {
			return readLimited(tr, maxBinaryBytes)
		}
	}
}

func readZipFile(f *zip.File) ([]byte, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}

	defer func() { _ = rc.Close() }()

	return readLimited(rc, maxBinaryBytes)
}

// replaceExecutable writes binary next to exe and renames it over exe. A
// running Windows .exe cannot be overwritten but can be renamed, so the old one
// moves to exe.old (removed by the next run) first.
func replaceExecutable(exe string, binary []byte, goos string) error {
	mode := os.FileMode(0o755)
	if fi, err := os.Stat(exe); err == nil {
		mode = fi.Mode().Perm()
	}

	next := exe + ".new"
	if err := os.WriteFile(next, binary, mode); err != nil {
		return fmt.Errorf("write the new binary: %w", err)
	}

	if goos != "windows" {
		if err := os.Rename(next, exe); err != nil {
			_ = os.Remove(next)
			return fmt.Errorf("replace %s: %w", exe, err)
		}

		return nil
	}

	old := exe + ".old"
	_ = os.Remove(old)

	if err := os.Rename(exe, old); err != nil {
		_ = os.Remove(next)
		return fmt.Errorf("move the running binary aside: %w", err)
	}

	if err := os.Rename(next, exe); err != nil {
		_ = os.Rename(old, exe)
		_ = os.Remove(next)

		return fmt.Errorf("install the new binary: %w", err)
	}

	return nil
}
