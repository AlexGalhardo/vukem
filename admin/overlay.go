package admin

import (
	"errors"
	"io/fs"
	"slices"
)

// overlay serves files from top when present and from base otherwise, so
// an app can override single admin templates (the layout, a page).
type overlay struct{ top, base fs.FS }

func (o overlay) Open(name string) (fs.File, error) {
	f, err := o.top.Open(name)
	if err == nil {
		st, statErr := f.Stat()
		if statErr == nil && !st.IsDir() {
			return f, nil
		}

		_ = f.Close()
	}

	return o.base.Open(name)
}

func (o overlay) ReadDir(name string) ([]fs.DirEntry, error) {
	baseEntries, baseErr := fs.ReadDir(o.base, name)
	topEntries, topErr := fs.ReadDir(o.top, name)

	if baseErr != nil && topErr != nil {
		return nil, errors.Join(baseErr, topErr)
	}

	seen := map[string]bool{}

	var out []fs.DirEntry

	for _, e := range append(topEntries, baseEntries...) {
		if !seen[e.Name()] {
			seen[e.Name()] = true
			out = append(out, e)
		}
	}

	slices.SortFunc(out, func(a, b fs.DirEntry) int {
		switch {
		case a.Name() < b.Name():
			return -1
		case a.Name() > b.Name():
			return 1
		default:
			return 0
		}
	})

	return out, nil
}
