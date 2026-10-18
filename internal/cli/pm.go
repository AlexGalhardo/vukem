package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
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
	// version is written into the generated package.json's packageManager
	// field. Pinned exactly, like every other version this CLI generates,
	// because Corepack installs whatever that field names.
	version string
	// exec runs a package binary (vite) without a package.json script.
	exec []string
	// cmdShim: on Windows the binary is a .cmd shim (npm, pnpm, yarn), not
	// an .exe (bun).
	cmdShim bool
}

// Package-manager versions written into the generated package.json, each
// the exact latest stable release; bump them with the toolchain. yarn stays
// on Classic because the generated Dockerfile installs with
// --frozen-lockfile, which Yarn 2+ replaced with --immutable, and bun
// matches bunImage so the image and the developer's machine agree.
const (
	npmVersion  = "12.2.0"
	bunVersion  = "1.4.3"
	pnpmVersion = "12.14.0"
	yarnVersion = "1.22.22"
)

var pmByName = map[string]jsPM{ //nolint:gochecknoglobals // read-only lookup table
	"npm": {
		name:        "npm",
		installArgs: []string{"install", "--no-audit", "--no-fund"},
		lockfile:    "package-lock.json",
		version:     npmVersion,
		exec:        []string{"exec", "--"},
		cmdShim:     true,
	},
	"bun": {
		name:        "bun",
		installArgs: []string{"install"},
		lockfile:    "bun.lock",
		version:     bunVersion,
		exec:        []string{"x"},
	},
	"pnpm": {
		name:        "pnpm",
		installArgs: []string{"install"},
		lockfile:    "pnpm-lock.yaml",
		version:     pnpmVersion,
		exec:        []string{"exec"},
		cmdShim:     true,
	},
	"yarn": {
		name:        "yarn",
		installArgs: []string{"install"},
		lockfile:    "yarn.lock",
		version:     yarnVersion,
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

// spec is the value of package.json's packageManager field, the form
// Corepack reads: "<name>@<exact version>".
func (p jsPM) spec() string { return p.name + "@" + p.version }

// manifest is the part of package.json this CLI reads.
type manifest struct {
	PackageManager string `json:"packageManager"`
}

// setPackageManager records pm in the project's package.json, so every
// later command reads the project's own choice instead of guessing. The
// field is the ecosystem's own (Corepack reads it), and package.json is
// where the project already declares its JavaScript toolchain, so no
// vukem-specific file has to become a second source of truth. The
// insertion is textual, like patchPackageJSON's, to leave the rest of the
// file and its key order alone.
func setPackageManager(env *Env, pm jsPM) error {
	const rel = "package.json"

	src, err := os.ReadFile(env.path(rel))
	if errors.Is(err, fs.ErrNotExist) {
		return nil // --api projects have no front end
	}

	if err != nil {
		return err
	}

	var m manifest
	if err := json.Unmarshal(src, &m); err == nil && m.PackageManager != "" {
		return nil
	}

	const anchor = "{\n"

	i := bytes.Index(src, []byte(anchor))
	if i < 0 {
		return fmt.Errorf("%s: no object to add packageManager to", rel)
	}

	at := i + len(anchor)
	line := `  "packageManager": "` + pm.spec() + `",` + "\n"

	return writeFile(env.path(rel), slices.Concat(src[:at], []byte(line), src[at:]))
}

// declaredPM is the package manager the project declares in package.json.
// Corepack treats that field as authoritative, so this CLI does too: it is
// the project's own choice, while a lockfile is only evidence of the last
// install — and after --no-install there is no lockfile at all.
func declaredPM(dir string) (jsPM, bool) {
	src, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if err != nil {
		return jsPM{}, false
	}

	var m manifest
	if err := json.Unmarshal(src, &m); err != nil || m.PackageManager == "" {
		return jsPM{}, false
	}

	name, _, _ := strings.Cut(m.PackageManager, "@")

	pm, ok := pmByName[name]

	return pm, ok
}

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
// explicit choice (--pm), else the one package.json declares, else the one
// whose lockfile is in dir, else the first one installed on the machine.
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

	if pm, ok := declaredPM(dir); ok {
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
