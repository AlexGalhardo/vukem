package cli

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestPackageManager(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		lockfile string
		explicit string
		onPath   []string // the package managers this machine has
		want     string
		wantErr  bool
	}{
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
