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
		want     string
		wantErr  bool
	}{
		{name: "default npm", want: "npm"},
		{name: "bun.lock", lockfile: "bun.lock", want: "bun"},
		{name: "legacy bun.lockb", lockfile: "bun.lockb", want: "bun"},
		{name: "pnpm", lockfile: "pnpm-lock.yaml", want: "pnpm"},
		{name: "yarn", lockfile: "yarn.lock", want: "yarn"},
		{name: "npm lockfile", lockfile: "package-lock.json", want: "npm"},
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

			pm, err := packageManager(dir, tt.explicit)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("packageManager(%q) = %+v, want error", tt.explicit, pm)
				}

				return
			}

			if err != nil || pm.name != tt.want {
				t.Fatalf("packageManager() = %+v, %v, want %s", pm, err, tt.want)
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
