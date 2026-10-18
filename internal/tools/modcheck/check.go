package main

import (
	"bufio"
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
)

// moduleParents are the directories holding publishable nested modules.
var moduleParents = []string{"contrib", "drivers"}

// repoRoot walks up from the working directory to the directory holding
// go.work, which is the repository root.
func repoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}

	for {
		if _, err := os.Stat(filepath.Join(dir, "go.work")); err == nil {
			return dir, nil
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("no go.work found in any parent directory")
		}

		dir = parent
	}
}

// discoverModules returns every drivers/* and contrib/* directory holding a
// go.mod, as slash-separated paths relative to root, sorted.
func discoverModules(root string) ([]string, error) {
	var dirs []string

	for _, parent := range moduleParents {
		entries, err := os.ReadDir(filepath.Join(root, parent))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}

		if err != nil {
			return nil, err
		}

		for _, e := range entries {
			if !e.IsDir() {
				continue
			}

			dir := parent + "/" + e.Name()
			if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(dir), "go.mod")); err != nil {
				continue
			}

			dirs = append(dirs, dir)
		}
	}

	slices.SortFunc(dirs, cmp.Compare)

	return dirs, nil
}

// workspaceModules is the set of directories go.work uses, as
// slash-separated paths relative to the root ("drivers/postgres").
func workspaceModules(root string) (map[string]bool, error) {
	b, err := os.ReadFile(filepath.Join(root, "go.work"))
	if err != nil {
		return nil, err
	}

	return parseWorkUse(b), nil
}

// parseWorkUse reads the use directives of a go.work file, both the single
// form ("use ./drivers/redis") and the block form.
func parseWorkUse(work []byte) map[string]bool {
	out := map[string]bool{}
	block := false

	sc := bufio.NewScanner(bytes.NewReader(work))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())

		switch {
		case block && line == ")":
			block = false
		case block:
			add(out, line)
		case line == "use (":
			block = true
		case strings.HasPrefix(line, "use "):
			add(out, strings.TrimSpace(strings.TrimPrefix(line, "use ")))
		}
	}

	return out
}

// add records one use path, normalised to the form discoverModules returns.
func add(out map[string]bool, path string) {
	path, _, _ = strings.Cut(path, "//")

	path = strings.Trim(strings.TrimSpace(path), `"`)
	path = strings.TrimPrefix(path, "./")

	if path != "" {
		out[path] = true
	}
}

// notTags are build constraints that are not -tags values: the build
// environment decides them, so passing them would describe a different
// toolchain rather than a different configuration of this one.
var notTags = []string{
	"aix", "android", "cgo", "darwin", "dragonfly", "freebsd", "gc", "gccgo", "hurd", "illumos", "ios", "js",
	"linux", "netbsd", "openbsd", "plan9", "solaris", "unix", "wasip1", "windows",
	"386", "amd64", "arm", "arm64", "loong64", "mips", "mips64", "mips64le", "mipsle", "ppc64", "ppc64le",
	"riscv64", "s390x", "wasm", "race", "msan", "asan", "boringcrypto",
}

// customTags are the build tags a module's own Go files declare and that a
// caller has to pass with -tags for those files to be compiled at all. A
// negated constraint (!vukem_nosqlite) is left out: its default build is
// the one without the tag, so enabling it would remove code rather than add
// any.
func customTags(root, dir string) ([]string, error) {
	// Read through an os.Root: the walk and the read are separate
	// syscalls, and a symlink swapped in between them would otherwise
	// escape the module directory (docs/security.md).
	modRoot, err := os.OpenRoot(filepath.Join(root, filepath.FromSlash(dir)))
	if err != nil {
		return nil, err
	}

	defer modRoot.Close()

	var tags []string

	err = fs.WalkDir(modRoot.FS(), ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".go") {
			return err
		}

		b, err := fs.ReadFile(modRoot.FS(), p)
		if err != nil {
			return err
		}

		for _, t := range constraintTags(b) {
			if !slices.Contains(tags, t) {
				tags = append(tags, t)
			}
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	slices.Sort(tags)

	return tags, nil
}

// constraintTags reads the positive identifiers of a file's //go:build line.
func constraintTags(src []byte) []string {
	var out []string

	sc := bufio.NewScanner(bytes.NewReader(src))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())

		// The constraint has to precede the package clause.
		if strings.HasPrefix(line, "package ") {
			return out
		}

		expr, ok := strings.CutPrefix(line, "//go:build")
		if !ok {
			continue
		}

		for _, word := range strings.FieldsFunc(expr, func(r rune) bool {
			return r == '(' || r == ')' || r == '&' || r == '|' || r == ' ' || r == '\t'
		}) {
			if strings.HasPrefix(word, "!") || slices.Contains(notTags, word) || strings.HasPrefix(word, "go1.") {
				continue
			}

			if !slices.Contains(out, word) {
				out = append(out, word)
			}
		}
	}

	return out
}

// vetWithoutWorkspace type-checks a module, test files included, with the
// workspace switched off so only its own go.mod can satisfy its imports. It
// returns the command output when the check fails.
func vetWithoutWorkspace(ctx context.Context, root, dir string, tags []string) (string, error) {
	args := []string{"vet"}
	if len(tags) > 0 {
		args = append(args, "-tags", strings.Join(tags, ","))
	}

	args = append(args, "./...")

	cmd := exec.CommandContext(ctx, "go", args...)
	cmd.Dir = filepath.Join(root, filepath.FromSlash(dir))
	// GOFLAGS=-mod=readonly keeps the gate honest: a missing requirement is
	// reported instead of quietly added to go.mod.
	cmd.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=-mod=readonly")

	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("go %s in %s: %w", strings.Join(args, " "), dir, err)
	}

	return string(out), nil
}
