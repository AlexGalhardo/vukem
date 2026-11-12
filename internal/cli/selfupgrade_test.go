package cli

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fakeRelease struct {
	tag        string
	prerelease bool
	draft      bool
	archive    []byte // served for the archive asset
	sumsFor    []byte // content the checksums.txt entry is computed from
	noEntry    bool
	bundle     bool
	redirectTo string // archive redirects here when set
	pad        int    // extra bytes appended to the archive body
}

func tarGz(t *testing.T, content []byte) []byte {
	t.Helper()

	var buf bytes.Buffer

	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)

	if err := tw.WriteHeader(
		&tar.Header{Name: "vukem_x/vukem", Typeflag: tar.TypeReg, Mode: 0o755, Size: int64(len(content))},
	); err != nil {
		t.Fatal(err)
	}

	_, _ = tw.Write(content)
	_ = tw.Close()
	_ = gz.Close()

	return buf.Bytes()
}

func zipOf(t *testing.T, name string, content []byte) []byte {
	t.Helper()

	var buf bytes.Buffer

	zw := zip.NewWriter(&buf)

	w, err := zw.Create(name)
	if err != nil {
		t.Fatal(err)
	}

	_, _ = w.Write(content)
	_ = zw.Close()

	return buf.Bytes()
}

// newUpgrader starts a TLS server faking the GitHub API and asset hosts.
func newUpgrader(t *testing.T, rel fakeRelease, goos string) (*selfUpgrader, *bytes.Buffer, *[][]string, string) {
	t.Helper()

	ext := ".tar.gz"
	if goos == "windows" {
		ext = ".zip"
	}

	archiveName := fmt.Sprintf("vukem_%s_%s_amd64%s", strings.TrimPrefix(rel.tag, "v"), goos, ext)
	body := append(append([]byte{}, rel.archive...), make([]byte, rel.pad)...)
	sum := sha256.Sum256(cmpOr(rel.sumsFor, rel.archive))
	sums := hex.EncodeToString(sum[:]) + "  " + archiveName + "\n"

	if rel.noEntry {
		sums = hex.EncodeToString(sum[:]) + "  other.tar.gz\n"
	}

	mux := http.NewServeMux()

	var srv *httptest.Server

	mux.HandleFunc("/repos/"+selfUpgradeRepo+"/releases/", func(w http.ResponseWriter, _ *http.Request) {
		assets := []releaseAsset{
			{Name: archiveName, URL: srv.URL + "/dl/archive"},
			{Name: "checksums.txt", URL: srv.URL + "/dl/sums"},
		}
		if rel.bundle {
			assets = append(assets, releaseAsset{Name: "checksums.txt.sigstore.json", URL: srv.URL + "/dl/bundle"})
		}

		_ = json.NewEncoder(w).
			Encode(releaseInfo{Tag: rel.tag, Draft: rel.draft, Prerelease: rel.prerelease, Assets: assets})
	})
	mux.HandleFunc("/dl/archive", func(w http.ResponseWriter, r *http.Request) {
		if rel.redirectTo != "" {
			http.Redirect(w, r, rel.redirectTo, http.StatusFound)
			return
		}

		_, _ = w.Write(body)
	})
	mux.HandleFunc("/dl/sums", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(sums)) })
	mux.HandleFunc("/dl/bundle", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("{}")) })

	srv = httptest.NewTLSServer(mux)
	t.Cleanup(srv.Close)

	host := srv.Listener.Addr().String()
	dir := t.TempDir()
	exe := filepath.Join(dir, "vukem")

	if err := os.WriteFile(exe, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer

	var calls [][]string

	u := &selfUpgrader{
		env: &Env{
			Stdout:  &out,
			Stderr:  &out,
			Version: "v1.0.0",
			Getenv:  func(string) (string, bool) { return "", false },
		},
		apiBase: srv.URL,
		client:  srv.Client(),
		trusted: func(u *url.URL) bool { return u.Scheme == "https" && u.Host == host },
		goos:    goos,
		goarch:  "amd64",
		exe:     func() (string, error) { return exe, nil },
		lookPath: func(string) (string, error) {
			return "", errors.New("not found")
		},
		viaGoInstall: func(string) bool { return false },
		run: func(_ context.Context, name string, args ...string) error {
			calls = append(calls, append([]string{name}, args...))
			return nil
		},
	}

	return u, &out, &calls, exe
}

func cmpOr(a, b []byte) []byte {
	if a != nil {
		return a
	}

	return b
}

func TestSelfUpgrade_Archive(t *testing.T) {
	t.Parallel()

	for _, goos := range []string{"linux", "windows"} {
		t.Run(goos, func(t *testing.T) {
			t.Parallel()

			archive := tarGz(t, []byte("new-binary"))
			if goos == "windows" {
				archive = zipOf(t, "vukem.exe", []byte("new-binary"))
			}

			u, out, calls, exe := newUpgrader(t, fakeRelease{tag: "v1.1.0", archive: archive}, goos)

			if err := u.upgrade(t.Context(), false, ""); err != nil {
				t.Fatal(err)
			}

			got, _ := os.ReadFile(exe)
			if string(got) != "new-binary" {
				t.Fatalf("exe = %q", got)
			}

			_, oldErr := os.Stat(exe + ".old")
			if (goos == "windows") != (oldErr == nil) {
				t.Fatalf("goos %s: .old present = %v", goos, oldErr == nil)
			}

			if !strings.Contains(out.String(), "v1.0.0 -> v1.1.0 (archive)") ||
				!strings.Contains(out.String(), "skipping the signature") {
				t.Fatalf("output = %q", out)
			}

			if len(*calls) != 1 || (*calls)[0][0] != exe || (*calls)[0][1] != "version" {
				t.Fatalf("calls = %v", *calls)
			}
		})
	}
}

func TestSelfUpgrade_WindowsCleansOldOnNextRun(t *testing.T) {
	t.Parallel()

	u, _, _, exe := newUpgrader(t, fakeRelease{tag: "v1.0.0"}, "windows")

	if err := os.WriteFile(exe+".old", []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := u.upgrade(t.Context(), false, ""); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(exe + ".old"); err == nil {
		t.Fatal(".old not removed")
	}
}

func TestSelfUpgrade_AlreadyAndCheck(t *testing.T) {
	t.Parallel()

	u, out, _, _ := newUpgrader(t, fakeRelease{tag: "v1.0.0"}, "linux")
	if err := u.upgrade(t.Context(), false, ""); err != nil || !strings.Contains(out.String(), "already up to date") {
		t.Fatalf("err=%v out=%q", err, out)
	}

	u, out, calls, exe := newUpgrader(t, fakeRelease{tag: "v1.2.0"}, "linux")
	if err := u.upgrade(t.Context(), true, ""); err != nil || out.String() != "current: v1.0.0\nlatest: v1.2.0\n" {
		t.Fatalf("err=%v out=%q", err, out)
	}

	if got, _ := os.ReadFile(exe); string(got) != "old" || len(*calls) != 0 {
		t.Fatal("--check changed something")
	}
}

func TestSelfUpgrade_NeverDowngradesToLatest(t *testing.T) {
	t.Parallel()

	u, out, calls, exe := newUpgrader(t, fakeRelease{tag: "v0.9.0"}, "linux")
	if err := u.upgrade(
		t.Context(),
		false,
		"",
	); err != nil ||
		!strings.Contains(out.String(), "newer than the latest release") {
		t.Fatalf("upgrade() err=%v out=%q, want a newer-than-latest note", err, out)
	}

	if got, _ := os.ReadFile(exe); string(got) != "old" || len(*calls) != 0 {
		t.Fatal("upgrade() replaced a newer CLI with an older release")
	}
}

func TestSelfUpgrade_GoInstall(t *testing.T) {
	t.Parallel()

	u, out, calls, _ := newUpgrader(t, fakeRelease{tag: "v1.1.0"}, "linux")
	u.lookPath = func(string) (string, error) { return "/usr/bin/go", nil }
	u.viaGoInstall = func(string) bool { return true }

	if err := u.upgrade(t.Context(), false, "v1.1.0"); err != nil {
		t.Fatal(err)
	}

	if len(*calls) != 2 ||
		strings.Join((*calls)[0], " ") != "go install github.com/AlexGalhardo/vukem/cmd/vukem@v1.1.0" {
		t.Fatalf("calls = %v", *calls)
	}

	if !strings.Contains(out.String(), "(go install)") {
		t.Fatalf("out = %q", out)
	}
}

func TestSelfUpgrade_Cosign(t *testing.T) {
	t.Parallel()

	archive := tarGz(t, []byte("new"))

	u, _, calls, _ := newUpgrader(t, fakeRelease{tag: "v1.1.0", archive: archive, bundle: true}, "linux")
	u.lookPath = func(n string) (string, error) {
		if n == "cosign" {
			return "/bin/cosign", nil
		}

		return "", errors.New("no")
	}

	if err := u.upgrade(t.Context(), false, ""); err != nil {
		t.Fatal(err)
	}

	if c := (*calls)[0]; c[0] != "cosign" || c[1] != "verify-blob" ||
		!strings.Contains(strings.Join(c, " "), cosignIssuer) {
		t.Fatalf("calls = %v", *calls)
	}

	u, _, _, exe := newUpgrader(t, fakeRelease{tag: "v1.1.0", archive: archive, bundle: true}, "linux")
	u.lookPath = func(string) (string, error) { return "/bin/cosign", nil }
	u.run = func(context.Context, string, ...string) error { return errors.New("bad signature") }

	if err := u.upgrade(t.Context(), false, ""); err == nil || !strings.Contains(err.Error(), "cosign verification") {
		t.Fatalf("err = %v", err)
	}

	if got, _ := os.ReadFile(exe); string(got) != "old" {
		t.Fatal("binary replaced after cosign failure")
	}

	u, _, _, _ = newUpgrader(t, fakeRelease{tag: "v1.1.0", archive: archive}, "linux")
	u.lookPath = func(string) (string, error) { return "/bin/cosign", nil }

	if err := u.upgrade(t.Context(), false, ""); err == nil || !strings.Contains(err.Error(), "sigstore") {
		t.Fatalf("missing bundle must fail closed, err = %v", err)
	}
}

func TestSecurity_A08_SelfUpgradeRefusesChecksumMismatch(t *testing.T) {
	t.Parallel()

	archive := tarGz(t, []byte("new"))

	tests := []struct {
		name string
		rel  fakeRelease
		want string
	}{
		{"mismatch", fakeRelease{tag: "v1.1.0", archive: archive, sumsFor: []byte("other")}, "checksum mismatch"},
		{"missing entry", fakeRelease{tag: "v1.1.0", archive: archive, noEntry: true}, "no entry"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			u, _, _, exe := newUpgrader(t, tc.rel, "linux")

			if err := u.upgrade(t.Context(), false, ""); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}

			if got, _ := os.ReadFile(exe); string(got) != "old" {
				t.Fatal("binary replaced")
			}
		})
	}
}

func TestSecurity_A10_SelfUpgradeRefusesNonGitHubRedirect(t *testing.T) {
	t.Parallel()

	other := httptest.NewTLSServer(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("evil")) }),
	)
	t.Cleanup(other.Close)

	u, _, _, exe := newUpgrader(
		t,
		fakeRelease{tag: "v1.1.0", archive: tarGz(t, []byte("new")), redirectTo: other.URL},
		"linux",
	)

	if err := u.upgrade(t.Context(), false, ""); err == nil || !strings.Contains(err.Error(), "untrusted host") {
		t.Fatalf("err = %v", err)
	}

	if got, _ := os.ReadFile(exe); string(got) != "old" {
		t.Fatal("binary replaced")
	}
}

func TestSecurity_A10_SelfUpgradeHostPolicy(t *testing.T) {
	t.Parallel()

	tests := map[string]bool{
		"https://api.github.com/x":                       true,
		"https://github.com/a/b":                         true,
		"https://objects.githubusercontent.com/x":        true,
		"https://release-assets.githubusercontent.com/x": true,
		"http://github.com/x":                            false,
		"https://evilgithub.com/x":                       false,
		"https://github.com.evil.com/x":                  false,
		"https://githubusercontent.com.evil.com/x":       false,
		"https://objects.githubusercontent.com:8443/x":   false,
		"https://example.com/x":                          false,
	}

	for raw, want := range tests {
		u, _ := url.Parse(raw)
		if got := trustedGitHubHost(u); got != want {
			t.Errorf("trustedGitHubHost(%s) = %v, want %v", raw, got, want)
		}
	}
}

func TestSecurity_A08_SelfUpgradeRefusesPrerelease(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		rel  fakeRelease
		pin  string
		want string
	}{
		{"prerelease flag", fakeRelease{tag: "v1.2.0", prerelease: true}, "", "not a stable release"},
		{"draft", fakeRelease{tag: "v1.2.0", draft: true}, "", "not a stable release"},
		{"suffix tag", fakeRelease{tag: "v1.2.0-rc.1"}, "", "not a stable release"},
		{"pinned suffix", fakeRelease{tag: "v1.2.0-rc.1"}, "v1.2.0-rc.1", "--version must be a stable release"},
		{"pinned mismatch", fakeRelease{tag: "v1.3.0"}, "v1.2.0", "asked for v1.2.0"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			u, _, calls, exe := newUpgrader(t, tc.rel, "linux")

			if err := u.upgrade(t.Context(), false, tc.pin); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}

			if got, _ := os.ReadFile(exe); string(got) != "old" || len(*calls) != 0 {
				t.Fatal("something was installed")
			}
		})
	}
}

func TestSecurity_A10_SelfUpgradeCapsDownloadSize(t *testing.T) {
	t.Parallel()

	if _, err := readLimited(bytes.NewReader(make([]byte, 11)), 10); err == nil {
		t.Fatal("11 bytes over a 10 byte limit must fail")
	}

	if _, err := readLimited(bytes.NewReader(make([]byte, 10)), 10); err != nil {
		t.Fatal(err)
	}

	big := fakeRelease{tag: "v1.1.0", archive: tarGz(t, []byte("new")), pad: maxArchiveBytes}
	u, _, _, _ := newUpgrader(t, big, "linux")

	if err := u.upgrade(t.Context(), false, ""); err == nil || !strings.Contains(err.Error(), "limit") {
		t.Fatalf("err = %v", err)
	}
}

func TestSecurity_A08_SelfUpgradeTokenNeverPrinted(t *testing.T) {
	t.Parallel()

	var auth string

	u, out, _, _ := newUpgrader(t, fakeRelease{tag: "v1.0.0"}, "linux")
	u.env.Getenv = func(k string) (string, bool) { return "ghp_secret", k == "GITHUB_TOKEN" }
	u.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		auth = r.Header.Get("Authorization")
		return nil, errors.New("stop")
	})

	err := u.upgrade(t.Context(), false, "")
	if auth != "Bearer ghp_secret" {
		t.Fatalf("Authorization = %q", auth)
	}

	if err == nil || strings.Contains(err.Error(), "ghp_secret") || strings.Contains(out.String(), "ghp_secret") {
		t.Fatalf("token leaked or no error: %v %q", err, out)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestInGoBin(t *testing.T) {
	t.Parallel()

	bin := t.TempDir()
	env := &Env{Getenv: func(k string) (string, bool) { return bin, k == "GOBIN" }}

	if !inGoBin(env, filepath.Join(bin, "vukem")) || inGoBin(env, filepath.Join(t.TempDir(), "vukem")) {
		t.Fatal("GOBIN detection wrong")
	}
}
