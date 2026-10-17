package cli

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
)

// jsPM is a JavaScript package manager the CLI can drive.
type jsPM struct {
	name        string
	installArgs []string
	// exec runs a package binary (vite) without a package.json script.
	exec []string
	// cmdShim: on Windows the binary is a .cmd shim (npm, pnpm, yarn), not
	// an .exe (bun).
	cmdShim bool
}

var pmByName = map[string]jsPM{ //nolint:gochecknoglobals // read-only lookup table
	"npm": {
		name:        "npm",
		installArgs: []string{"install", "--no-audit", "--no-fund"},
		exec:        []string{"exec", "--"},
		cmdShim:     true,
	},
	"bun":  {name: "bun", installArgs: []string{"install"}, exec: []string{"x"}},
	"pnpm": {name: "pnpm", installArgs: []string{"install"}, exec: []string{"exec"}, cmdShim: true},
	"yarn": {name: "yarn", installArgs: []string{"install"}, exec: []string{"exec"}, cmdShim: true},
}

// lockfiles map each lockfile to its package manager, checked in order.
var lockfiles = []struct{ file, pm string }{ //nolint:gochecknoglobals // read-only lookup table
	{"bun.lock", "bun"},
	{"bun.lockb", "bun"},
	{"pnpm-lock.yaml", "pnpm"},
	{"yarn.lock", "yarn"},
	{"package-lock.json", "npm"},
}

// binary is the program to run on this OS.
func (p jsPM) binary() string {
	if p.cmdShim && runtime.GOOS == "windows" {
		return p.name + ".cmd"
	}

	return p.name
}

// install is the command line users should run by hand.
func (p jsPM) install() string { return p.name + " install" }

// packageManager picks the package manager for the project in dir: the
// explicit choice (--pm), else the one whose lockfile is in dir, else npm.
func packageManager(dir, explicit string) (jsPM, error) {
	if explicit != "" {
		pm, ok := pmByName[strings.ToLower(explicit)]
		if !ok {
			names := make([]string, 0, len(pmByName))
			for n := range pmByName {
				names = append(names, n)
			}

			slices.Sort(names)

			return jsPM{}, usagef("unknown package manager %q (want %s)", explicit, strings.Join(names, ", "))
		}

		return pm, nil
	}

	for _, l := range lockfiles {
		if _, err := os.Stat(filepath.Join(dir, l.file)); err == nil {
			return pmByName[l.pm], nil
		}
	}

	return pmByName["npm"], nil
}
