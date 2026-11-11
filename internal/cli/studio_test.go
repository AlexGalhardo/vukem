package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestStudioInstallArgs(t *testing.T) {
	t.Parallel()

	got := strings.Join(studioInstallArgs(), " ")
	want := "install github.com/AlexGalhardo/vukem/contrib/studio/cmd/vukem-studio@" + studioVersion

	if got != want {
		t.Errorf("studioInstallArgs = %q, want %q", got, want)
	}

	if !regexp.MustCompile(`^v\d+\.\d+\.\d+$`).MatchString(studioVersion) {
		t.Errorf("studioVersion = %q, want a release tag vX.Y.Z", studioVersion)
	}
}

// TestStudioVersionIsLatestTag keeps vukem studio on the newest contrib/studio
// release: bump studioVersion after tagging the module. It skips without git
// or tags locally; unlike the stripe pin it also skips when the checkout has no
// contrib/studio tag at all, because the first release has to be tagged
// before the pin can name it.
func TestStudioVersionIsLatestTag(t *testing.T) {
	t.Parallel()

	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not installed")
	}

	out, err := exec.CommandContext(t.Context(), git, "tag", "-l", "contrib/studio/v*", "--sort=-v:refname").Output()
	if err != nil {
		t.Skipf("git tag: %v", err)
	}

	tags := strings.Fields(string(out))
	if len(tags) == 0 {
		t.Skip("contrib/studio has no release tag yet")
	}

	if latest := strings.TrimPrefix(tags[0], "contrib/studio/"); studioVersion != latest {
		t.Errorf("studioVersion = %s, want the latest contrib/studio tag %s", studioVersion, latest)
	}
}

// TestStudioUsesBinFlag runs a stand-in vukem-studio: it must receive --dir
// (defaulting to the project) and the flags that were given, and nothing
// is installed.
func TestStudioUsesBinFlag(t *testing.T) {
	t.Parallel()
	requireGo(t)

	dir := stdlibApp(t, echoApp)

	bin, err := appBinary(t.Context(), &Env{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}

	code, out, errOut := vukem(t, dir, "studio", "--bin", bin, "--port", "0", "--no-open")
	if code != 0 {
		t.Fatalf("vukem studio --bin = %d %q %q", code, out, errOut)
	}

	for _, want := range []string{"--port=0", "--no-open=true", "--dir="} {
		if !strings.Contains(out, want) {
			t.Errorf("stand-in studio got %q, want it to contain %q", out, want)
		}
	}

	if strings.Contains(out, "--bin") {
		t.Errorf("--bin was passed on to vukem-studio: %q", out)
	}

	if _, err := os.Stat(filepath.Join(dir, ".vukem", "bin", exe("vukem-studio"))); err == nil {
		t.Error("a binary was installed although --bin was given")
	}

	// An explicit --dir is kept.
	code, out, _ = vukem(t, dir, "studio", "--bin", bin, "--dir", "other")
	if code != 0 || !strings.Contains(out, "--dir=other") || strings.Count(out, "--dir=") != 1 {
		t.Errorf("explicit --dir: %d %q", code, out)
	}
}
