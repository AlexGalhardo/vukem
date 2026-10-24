package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

const corePath = "github.com/AlexGalhardo/vukem"

var (
	updatedRe = regexp.MustCompile(`(?m)^(\| Updated \| )\d{4}-\d{2}-\d{2}`)
	requireRe = regexp.MustCompile(
		`(?m)^(\s*(?:require\s+)?` + regexp.QuoteMeta(corePath) + `\s+)(v\d+\.\d+\.\d+[0-9A-Za-z.-]*)`,
	)
)

// updatePlanStatus rewrites the "Updated" row of the PLAN.md status table.
// The table never names a release: docs do not cite the current version.
func updatePlanStatus(plan, date string) (string, error) {
	if !updatedRe.MatchString(plan) {
		return "", errors.New("PLAN.md status table has no \"Updated\" row")
	}

	return updatedRe.ReplaceAllString(plan, "${1}"+date), nil
}

// bumpRequire sets the core module requirement in a go.mod to version.
func bumpRequire(gomod, version string) string {
	return requireRe.ReplaceAllStringFunc(gomod, func(m string) string {
		sub := requireRe.FindStringSubmatch(m)
		// v0.0.0 marks a module that resolves the core only through replace.
		if sub[2] == "v0.0.0" {
			return m
		}

		return sub[1] + version
	})
}

// nestedModules lists the go.mod files of contrib/*, drivers/* and examples/*.
func nestedModules(root string) []string {
	var mods []string

	for _, group := range []string{"contrib", "drivers", "examples"} {
		m, _ := filepath.Glob(filepath.Join(root, group, "*", "go.mod"))
		mods = append(mods, m...)
	}

	return mods
}

// bumpModules raises the core requirement of every nested module and returns
// the module directories it changed.
func bumpModules(root, version string) ([]string, error) {
	var changed []string

	for _, path := range nestedModules(root) {
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}

		updated := bumpRequire(string(b), version)
		if updated == string(b) {
			continue
		}

		if err := os.WriteFile(path, []byte(updated), 0o600); err != nil { //nolint:gosec // G703: glob of the repo tree
			return nil, err
		}

		changed = append(changed, filepath.Dir(path))
	}

	return changed, nil
}

// tidyModules refreshes go.sum of the bumped modules. It needs the tag to be
// resolvable from the proxy, which is why it runs only after the push.
func tidyModules(dirs []string) error {
	for _, d := range dirs {
		cmd := exec.Command("go", "mod", "tidy") //nolint:noctx // maintainer tool
		cmd.Dir = d
		cmd.Env = append(os.Environ(), "GOWORK=off")

		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("go mod tidy in %s: %w: %s", d, err, strings.TrimSpace(string(out)))
		}
	}

	return nil
}
