package main

import (
	"fmt"
	"io/fs"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// scanDirs are searched for scaffold files that pin images and actions.
var scanDirs = []string{"internal/cli", ".config/docker"}

// scanFiles lists the non-test source and template files under scanDirs.
func scanFiles(root string) ([]string, error) {
	var files []string

	for _, d := range scanDirs {
		err := filepath.WalkDir(
			filepath.Join(root, filepath.FromSlash(d)),
			func(p string, e fs.DirEntry, err error) error {
				if err != nil || e.IsDir() || strings.HasSuffix(p, "_test.go") || strings.HasSuffix(p, ".json") {
					return err
				}

				rel, err := filepath.Rel(root, p)
				files = append(files, filepath.ToSlash(rel))

				return err
			},
		)
		if err != nil {
			return nil, err
		}
	}

	slices.Sort(files)

	return files, nil
}

// refPins builds one pin per (file, name) for every match of ref, where
// group 1 is the name and group 2 the pinned value ("tag@sha256:..." or
// "sha # vX.Y.Z"). A name pinned to different values in one file keeps all
// of them in current, so the pin reads as behind until every one matches.
func refPins(root string, ref *regexp.Regexp, nameGroup, valueGroup int, k kind) ([]pin, error) {
	files, err := scanFiles(root)
	if err != nil {
		return nil, err
	}

	var pins []pin

	for _, f := range files {
		text, err := read(root, f)
		if err != nil {
			return nil, err
		}

		values := map[string][]string{}

		var names []string

		for _, m := range ref.FindAllStringSubmatch(text, -1) {
			name, val := m[nameGroup], m[valueGroup]
			if _, ok := values[name]; !ok {
				names = append(names, name)
			}

			if !slices.Contains(values[name], val) {
				values[name] = append(values[name], val)
			}
		}

		for _, n := range names {
			sep := ":"
			if k == ghAction {
				sep = "@"
			}

			re := regexp.MustCompile(`(` + regexp.QuoteMeta(n+sep) + `)(` + valuePattern(k) + `)()`)
			p := pin{pkg: n, kind: k, file: f, group: f + ":" + n, current: strings.Join(values[n], ","), re: re}
			p.lockMinor = k == dockerImg && lockMinorImages[n]
			pins = append(pins, p)
		}
	}

	return pins, nil
}

func valuePattern(k kind) string {
	if k == ghAction {
		return `[0-9a-f]{40} # \S+`
	}

	return `[A-Za-z0-9._-]+@sha256:[0-9a-f]{64}`
}

// checkGoMinor fails when the golang image line is not the Go minor of the
// repository go.mod, so the unattended bump never moves the toolchain.
func checkGoMinor(root string, pins []pin) error {
	mod, err := read(root, "go.mod")
	if err != nil {
		return nil //nolint:nilerr // a tree without go.mod (tests) has nothing to compare with
	}

	m := regexp.MustCompile(`(?m)^go (\d+)\.(\d+)`).FindStringSubmatch(mod)
	if m == nil {
		return nil
	}

	for i := range pins {
		p := &pins[i]
		if !p.lockMinor || p.pkg != "golang" {
			continue
		}

		if !strings.HasPrefix(p.pkg+":"+p.current, "golang:"+m[1]+"."+m[2]+".") {
			return fmt.Errorf(
				"%s pins %s:%s but go.mod is on Go %s.%s; bump the image by hand with the toolchain",
				p.file,
				p.pkg,
				p.current,
				m[1],
				m[2],
			)
		}
	}

	return nil
}
