package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
)

// jsPM is a JavaScript package manager the CLI can drive.
type jsPM struct {
	name        string
	installArgs []string
	// lockfile is the file this manager writes and the generated Docker
	// image installs from.
	lockfile string
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
		lockfile:    "package-lock.json",
		exec:        []string{"exec", "--"},
		cmdShim:     true,
	},
	"bun": {name: "bun", installArgs: []string{"install"}, lockfile: "bun.lock", exec: []string{"x"}},
	"pnpm": {
		name:        "pnpm",
		installArgs: []string{"install"},
		lockfile:    "pnpm-lock.yaml",
		exec:        []string{"exec"},
		cmdShim:     true,
	},
	"yarn": {
		name:        "yarn",
		installArgs: []string{"install"},
		lockfile:    "yarn.lock",
		exec:        []string{"exec"},
		cmdShim:     true,
	},
}

// lockfiles map each lockfile to its package manager, checked in order.
var lockfiles = []struct{ file, pm string }{ //nolint:gochecknoglobals // read-only lookup table
	{"bun.lock", "bun"},
	{"bun.lockb", "bun"},
	{"pnpm-lock.yaml", "pnpm"},
	{"yarn.lock", "yarn"},
	{"package-lock.json", "npm"},
}

// installed is the order tried when a project has no lockfile, which is
// every `vukem new`: its directory does not exist yet, so there is nothing
// to detect and the choice has to come from the machine. The first manager
// on PATH wins, bun first because the starter's dependencies all install
// with it and it needs no corepack; npm stays ahead of pnpm and yarn so a
// plain Node toolchain keeps the documented default.
var installed = []string{"bun", "npm", "pnpm", "yarn"} //nolint:gochecknoglobals // read-only lookup table

// binary is the program to run on this OS.
func (p jsPM) binary() string {
	if p.cmdShim && runtime.GOOS == "windows" {
		return p.name + ".cmd"
	}

	return p.name
}

// install is the command line users should run by hand.
func (p jsPM) install() string { return p.name + " install" }

// hasLockfile reports whether dir holds a lockfile of any supported
// package manager. The generated Docker image installs from one, so
// without it the assets stage has nothing reproducible to install.
func hasLockfile(dir string) bool {
	return slices.ContainsFunc(lockfiles, func(l struct{ file, pm string }) bool {
		_, err := os.Stat(filepath.Join(dir, l.file))

		return err == nil
	})
}

// packageManager picks the package manager for the project in dir: the
// explicit choice (--pm), else the one whose lockfile is in dir, else the
// first one installed on the machine.
func packageManager(dir, explicit string) (jsPM, error) {
	return pickPM(dir, explicit, exec.LookPath)
}

// pickPM is packageManager with an injectable PATH lookup, so tests can
// describe a toolchain instead of depending on the machine's.
func pickPM(dir, explicit string, look func(string) (string, error)) (jsPM, error) {
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

	// A lockfile the project does not have would make the generated
	// Dockerfile install with a manager the app never uses, so the fallback
	// is a manager that exists here and will write that lockfile.
	for _, n := range installed {
		pm := pmByName[n]
		if _, err := look(pm.binary()); err == nil {
			return pm, nil
		}
	}

	return pmByName["npm"], nil
}
