package storage

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// Local is a disk on the local filesystem. Every operation goes through
// an os.Root, so symlinks and ".." cannot leave the directory.
type Local struct {
	root *os.Root
	dir  string
	base string
}

// NewLocal opens (creating it if needed) dir as a disk; baseURL prefixes
// public URLs ("" for a private disk). Close it on shutdown.
func NewLocal(dir, baseURL string) (*Local, error) {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, fmt.Errorf("storage: %w", err)
	}

	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, fmt.Errorf("storage: %w", err)
	}

	return &Local{root: root, dir: dir, base: baseURL}, nil
}

// Close releases the directory handle.
func (l *Local) Close() error { return l.root.Close() }

// tmpMarker separates a file name from the random suffix of its
// temporary copy (".name.tmp-XXXXXXXX").
const tmpMarker = ".tmp-"

// isTemp reports whether name is a Put temporary file (left behind only
// when the process dies mid-write).
func isTemp(name string) bool {
	return strings.HasPrefix(name, ".") && strings.Contains(name, tmpMarker)
}

// Put implements Disk. The file is written to a hidden temporary name
// (".name.tmp-…", neither listed nor served) and renamed, so readers never
// see partial content.
func (l *Local) Put(_ context.Context, p string, r io.Reader) error {
	c, err := Clean(p)
	if err != nil {
		return err
	}

	if d := path.Dir(c); d != "." {
		if err := l.root.MkdirAll(d, 0o750); err != nil {
			return fmt.Errorf("storage: %w", err)
		}
	}

	// A dot name keeps the partial file out of Handler (dotfiles are never
	// served) and List until the rename publishes it.
	tmp := path.Join(path.Dir(c), "."+path.Base(c)+tmpMarker+rand.Text()[:8])

	f, err := l.root.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o640)
	if err != nil {
		return fmt.Errorf("storage: %w", err)
	}

	if _, err := io.Copy(f, r); err != nil {
		_ = f.Close()
		_ = l.root.Remove(tmp)

		return fmt.Errorf("storage: write %s: %w", c, err)
	}

	if err := f.Close(); err != nil {
		_ = l.root.Remove(tmp)
		return err
	}

	if err := l.root.Rename(tmp, c); err != nil {
		_ = l.root.Remove(tmp)
		return fmt.Errorf("storage: %w", err)
	}

	return nil
}

// Open implements Disk.
func (l *Local) Open(_ context.Context, p string) (io.ReadCloser, error) {
	c, err := Clean(p)
	if err != nil {
		return nil, err
	}

	f, err := l.root.Open(c)
	if err != nil {
		return nil, err
	}

	if st, err := f.Stat(); err == nil && st.IsDir() {
		_ = f.Close()
		return nil, &fs.PathError{Op: "open", Path: c, Err: fs.ErrNotExist}
	}

	return f, nil
}

// Exists implements Disk.
func (l *Local) Exists(_ context.Context, p string) (bool, error) {
	c, err := Clean(p)
	if err != nil {
		return false, err
	}

	st, err := l.root.Stat(c)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}

	return err == nil && !st.IsDir(), err
}

// Delete implements Disk (missing files are not an error).
func (l *Local) Delete(_ context.Context, p string) error {
	c, err := Clean(p)
	if err != nil {
		return err
	}

	if err := l.root.Remove(c); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}

	return nil
}

// List implements Disk.
func (l *Local) List(_ context.Context, dir string) ([]string, error) {
	start := "."

	if dir != "" && dir != "." {
		c, err := Clean(dir)
		if err != nil {
			return nil, err
		}

		start = c
	}

	var out []string

	err := fs.WalkDir(l.root.FS(), start, func(p string, d fs.DirEntry, err error) error {
		if errors.Is(err, fs.ErrNotExist) {
			return fs.SkipAll
		}

		if err != nil || d.IsDir() || isTemp(d.Name()) {
			return err
		}

		out = append(out, filepath.ToSlash(p))

		return nil
	})

	sort.Strings(out)

	return out, err
}

// URL implements Disk.
func (l *Local) URL(p string) string { return publicURL(l.base, p) }
