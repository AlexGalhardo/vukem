package main

import (
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

// docEdit rewrites the mention of one old version in documentation.
type docEdit struct {
	re   *regexp.Regexp
	repl string
}

// docEdits builds the replacements for one updated pin. A mention is the
// package name followed, on the same line and within 40 characters without a
// digit in between, by the old version ("htmx 4.0.0", "react-email@6.11.6",
// "buf@v1.74.0", "Bun `1.4.4`"). Images are matched as name:tag. The exact
// old version string must be present, so unrelated numbers never change.
func docEdits(r *row) []docEdit {
	old, next := r.current, r.latest

	switch r.kind {
	case dockerImg:
		old, _, _ = strings.Cut(old, "@")
		next, _, _ = strings.Cut(next, "@")

		if old == next {
			return nil
		}

		return []docEdit{
			{
				regexp.MustCompile(
					`(\b` + regexp.QuoteMeta(r.pkg+":") + `)` + regexp.QuoteMeta(old) + `([^\w.-]|\.[^\w]|\.$|$)`,
				),
				"${1}" + next + "${2}",
			},
		}
	case npmPkg, goMod:
		// the version is the pinned string itself
	case ghAction:
		_, old, _ = strings.Cut(old, " # ")
		_, next, _ = strings.Cut(next, " # ")
	}

	old, next = strings.TrimPrefix(old, "v"), strings.TrimPrefix(next, "v")
	if old == next || old == "" {
		return nil
	}

	names := []string{r.pkg}
	if base, ok := strings.CutSuffix(r.pkg, ".org"); ok {
		names = append(names, base)
	}
	if r.kind == goMod || r.kind == ghAction {
		names = append(names, path.Base(r.pkg))
	}

	var out []docEdit

	for _, n := range names {
		lead := ""
		if c := n[0]; c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' {
			lead = `\b`
		}

		re := regexp.MustCompile(
			"(?im)(" + lead + regexp.QuoteMeta(
				n,
			) + `(?:[^\w\n-][^\n0-9]{0,39}?|-)v?)` + regexp.QuoteMeta(
				old,
			) + `([^\w.]|\.[^\w]|\.$|$)`,
		)
		out = append(out, docEdit{re, "${1}" + next + "${2}"})
	}

	return out
}

// docFiles are the pages a version mention is synced in: docs/*.md and the
// published docs. The site versions archive and the changelog are history
// and stay as written.
func docFiles(root string) ([]string, error) {
	files, err := filepath.Glob(filepath.Join(root, "docs", "*.md"))
	if err != nil {
		return nil, err
	}

	err = filepath.WalkDir(
		filepath.Join(root, "site", "content", "docs"),
		func(p string, d fs.DirEntry, err error) error {
			if err == nil && !d.IsDir() && strings.HasSuffix(p, ".mdx") {
				files = append(files, p)
			}

			return err
		},
	)
	if os.IsNotExist(err) {
		err = nil
	}

	return files, err
}

// syncDocs applies the edits of every updated pin and returns the changed
// files relative to root.
func syncDocs(root string, rows []row) ([]string, error) {
	var edits []docEdit

	for i := range rows {
		if rows[i].behind() {
			edits = append(edits, docEdits(&rows[i])...)
		}
	}

	if len(edits) == 0 {
		return nil, nil
	}

	files, err := docFiles(root)
	if err != nil {
		return nil, err
	}

	var changed []string

	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			return nil, err
		}

		text := string(b)
		for _, e := range edits {
			text = e.re.ReplaceAllString(text, e.repl)
		}

		if text == string(b) {
			continue
		}

		rel, err := filepath.Rel(root, f)
		if err != nil {
			return nil, err
		}

		if err := writeSource(root, filepath.ToSlash(rel), []byte(text)); err != nil {
			return nil, err
		}

		changed = append(changed, filepath.ToSlash(rel))
	}

	return changed, nil
}
