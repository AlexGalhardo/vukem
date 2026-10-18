package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

func TestPackageManager(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		lockfile string
		declared string // the packageManager field of package.json
		explicit string
		onPath   []string // the package managers this machine has
		want     string
		wantErr  bool
	}{
		// The project's own declaration outranks the machine and the
		// lockfile: without it, vukem new --pm npm --no-install followed by
		// vukem build advised "run bun install" on a machine with bun.
		{name: "declared", declared: "npm@12.2.0", onPath: []string{"bun"}, want: "npm"},
		{name: "declared without a version", declared: "pnpm", onPath: []string{"bun"}, want: "pnpm"},
		{
			name:     "declared beats the lockfile",
			lockfile: "bun.lock",
			declared: "yarn@1.22.22",
			onPath:   []string{"bun"},
			want:     "yarn",
		},
		{name: "explicit beats the declaration", declared: "npm@12.2.0", explicit: "bun", want: "bun"},
		{
			name:     "an unknown declaration falls through to the lockfile",
			lockfile: "pnpm-lock.yaml",
			declared: "deno@2.6.1",
			onPath:   []string{"bun"},
			want:     "pnpm",
		},
		// No lockfile: the choice comes from the machine, so the generated
		// Dockerfile installs with the manager that wrote the lockfile (A78).
		{name: "no lockfile prefers bun", onPath: []string{"bun", "npm", "yarn"}, want: "bun"},
		{name: "no lockfile without bun", onPath: []string{"npm", "pnpm", "yarn"}, want: "npm"},
		{name: "no lockfile only pnpm", onPath: []string{"pnpm"}, want: "pnpm"},
		{name: "no lockfile empty toolchain", want: "npm"},
		{name: "bun.lock", lockfile: "bun.lock", onPath: []string{"npm"}, want: "bun"},
		{name: "legacy bun.lockb", lockfile: "bun.lockb", onPath: []string{"npm"}, want: "bun"},
		{name: "pnpm", lockfile: "pnpm-lock.yaml", onPath: []string{"npm"}, want: "pnpm"},
		{name: "yarn", lockfile: "yarn.lock", onPath: []string{"npm"}, want: "yarn"},
		{name: "npm lockfile", lockfile: "package-lock.json", onPath: []string{"bun"}, want: "npm"},
		{name: "explicit wins", lockfile: "yarn.lock", explicit: "bun", want: "bun"},
		{name: "unknown explicit", explicit: "deno", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			if tt.lockfile != "" {
				if err := os.WriteFile(filepath.Join(dir, tt.lockfile), nil, 0o600); err != nil {
					t.Fatal(err)
				}
			}

			if tt.declared != "" {
				pkg := `{"private": true, "packageManager": "` + tt.declared + `"}`
				if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(pkg), 0o600); err != nil {
					t.Fatal(err)
				}
			}

			look := func(bin string) (string, error) {
				for _, n := range tt.onPath {
					if pmByName[n].binary() == bin {
						return bin, nil
					}
				}

				return "", os.ErrNotExist
			}

			pm, err := pickPM(dir, tt.explicit, look)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("pickPM(%q) = %+v, want error", tt.explicit, pm)
				}

				return
			}

			if err != nil || pm.name != tt.want {
				t.Fatalf("pickPM() = %+v, %v, want %s", pm, err, tt.want)
			}
		})
	}
}

func TestPackageManagerCommands(t *testing.T) {
	t.Parallel()

	bun := pmByName["bun"]
	if got := bun.binary(); got != "bun" {
		t.Errorf("bun binary = %q, want bun on every OS (bun.exe needs no .cmd)", got)
	}

	npm := pmByName["npm"]

	want := "npm"
	if runtime.GOOS == "windows" {
		want = "npm.cmd"
	}

	if got := npm.binary(); got != want {
		t.Errorf("npm binary = %q, want %q", got, want)
	}

	if got := bun.installArgs; len(got) != 1 || got[0] != "install" {
		t.Errorf("bun install args = %v", got)
	}
}

// Every pinned package-manager version is an exact latest stable release:
// the field Corepack reads must name one, and a floating or pre-release
// spec would make the install it drives unreproducible.
func TestPackageManagerSpecsArePinned(t *testing.T) {
	t.Parallel()

	exact := regexp.MustCompile(`^(npm|bun|pnpm|yarn)@\d+\.\d+\.\d+$`)

	for name, pm := range pmByName {
		if got := pm.spec(); !exact.MatchString(got) {
			t.Errorf("%s spec = %q, want <name>@<major.minor.patch>", name, got)
		}

		if pm.lockfile == "" {
			t.Errorf("%s has no lockfile name", name)
		}
	}
}

// A generated project records its package manager, so every later command
// reads the project's choice instead of guessing from a lockfile that
// --no-install has not written yet.
func TestSetPackageManagerRecordsTheChoice(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	env := &Env{Dir: dir}

	pkg := "{\n  \"private\": true,\n  \"type\": \"module\"\n}\n"
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(pkg), 0o600); err != nil {
		t.Fatal(err)
	}

	npm := pmByName["npm"]
	if err := setPackageManager(env, npm); err != nil {
		t.Fatal(err)
	}

	got := read(t, dir, "package.json")
	if !strings.Contains(got, `"packageManager": "`+npm.spec()+`",`) {
		t.Fatalf("package.json = %s", got)
	}

	if !json.Valid([]byte(got)) {
		t.Fatalf("package.json is not valid JSON:\n%s", got)
	}

	// The project now answers for itself, whatever this machine has.
	pm, err := pickPM(dir, "", func(string) (string, error) { return "bun", nil })
	if err != nil || pm.name != "npm" {
		t.Fatalf("pickPM() = %+v, %v, want npm", pm, err)
	}

	// Running it again neither duplicates the field nor changes it.
	before := got
	if err := setPackageManager(env, pmByName["bun"]); err != nil {
		t.Fatal(err)
	}

	if after := read(t, dir, "package.json"); after != before {
		t.Errorf("package.json changed on a second run:\n%s", after)
	}

	// An --api project has no package.json, and that is not an error.
	if err := setPackageManager(&Env{Dir: t.TempDir()}, npm); err != nil {
		t.Errorf("setPackageManager without a package.json = %v", err)
	}
}
