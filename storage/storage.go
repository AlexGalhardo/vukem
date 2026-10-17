// Package storage reads and writes files on disks: the local filesystem
// (through os.Root, so paths can never escape the disk) and memory (tests);
// S3-compatible storage lives in the drivers/s3 module.
//
//	disk, err := storage.NewLocal("storage/app/public", "/storage")
//	path, err := storage.PutUploaded(ctx, disk, fileHeader, "avatars", storage.Images(2<<20))
//	url := disk.URL(path)
//
// Every disk implements [Disk]; [Clean] validates paths, [Handler] serves
// a disk over HTTP and [TemporaryURL] signs links to private files.
// Streaming, sniffed uploads live in package upload.
//
// See https://github.com/AlexGalhardo/vukem/blob/main/docs/subsystems.md#storage.
package storage

import (
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
	"path"
	"sort"
	"strings"
	"sync"
	"time"
)

// Disk stores files under slash-separated relative paths.
type Disk interface {
	Put(ctx context.Context, p string, r io.Reader) error
	Open(ctx context.Context, p string) (io.ReadCloser, error)
	Exists(ctx context.Context, p string) (bool, error)
	Delete(ctx context.Context, p string) error
	// List returns the file paths under dir (recursively), sorted.
	List(ctx context.Context, dir string) ([]string, error)
	// URL is the public URL of p ("" for private disks).
	URL(p string) string
}

// ErrNotFound is returned (wrapped) for missing files.
var ErrNotFound = fs.ErrNotExist

// ErrInvalidPath rejects absolute paths, "..", backslashes and empty names.
var ErrInvalidPath = errors.New("storage: invalid path")

// Clean validates a relative path and returns it normalized.
func Clean(p string) (string, error) {
	if p == "" || strings.ContainsAny(p, "\\\x00:") || strings.HasPrefix(p, "/") {
		return "", ErrInvalidPath
	}

	c := path.Clean(p)
	if c == "." || c == ".." || strings.HasPrefix(c, "../") {
		return "", ErrInvalidPath
	}

	for part := range strings.SplitSeq(c, "/") {
		if part == ".." {
			return "", ErrInvalidPath
		}
	}

	return c, nil
}

// Get reads a whole file.
func Get(ctx context.Context, d Disk, p string) ([]byte, error) {
	f, err := d.Open(ctx, p)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	return io.ReadAll(f)
}

// PutBytes writes data to p.
func PutBytes(ctx context.Context, d Disk, p string, data []byte) error {
	return d.Put(ctx, p, bytes.NewReader(data))
}

// Memory is an in-memory disk for tests.
type Memory struct {
	base string

	mu    sync.RWMutex
	files map[string][]byte
}

// NewMemory returns an empty memory disk; base prefixes URLs ("" = private).
func NewMemory(base string) *Memory { return &Memory{base: base, files: map[string][]byte{}} }

// Put implements Disk.
func (m *Memory) Put(_ context.Context, p string, r io.Reader) error {
	c, err := Clean(p)
	if err != nil {
		return err
	}

	b, err := io.ReadAll(r)
	if err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	m.files[c] = b

	return nil
}

// Open implements Disk.
func (m *Memory) Open(_ context.Context, p string) (io.ReadCloser, error) {
	c, err := Clean(p)
	if err != nil {
		return nil, err
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	b, ok := m.files[c]
	if !ok {
		return nil, &fs.PathError{Op: "open", Path: c, Err: fs.ErrNotExist}
	}

	return io.NopCloser(bytes.NewReader(bytes.Clone(b))), nil
}

// Exists implements Disk.
func (m *Memory) Exists(_ context.Context, p string) (bool, error) {
	c, err := Clean(p)
	if err != nil {
		return false, err
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	_, ok := m.files[c]

	return ok, nil
}

// Delete implements Disk (missing files are not an error).
func (m *Memory) Delete(_ context.Context, p string) error {
	c, err := Clean(p)
	if err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	delete(m.files, c)

	return nil
}

// List implements Disk.
func (m *Memory) List(_ context.Context, dir string) ([]string, error) {
	prefix := ""

	if dir != "" && dir != "." {
		c, err := Clean(dir)
		if err != nil {
			return nil, err
		}

		prefix = c + "/"
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	var out []string

	for p := range m.files {
		if strings.HasPrefix(p, prefix) {
			out = append(out, p)
		}
	}

	sort.Strings(out)

	return out, nil
}

// URL implements Disk.
func (m *Memory) URL(p string) string { return publicURL(m.base, p) }

func publicURL(base, p string) string {
	if base == "" {
		return ""
	}

	return strings.TrimSuffix(base, "/") + "/" + strings.TrimPrefix(p, "/")
}

// modTimeNow is used by disks that cannot report a modification time.
var modTimeNow = time.Now //nolint:gochecknoglobals // overridable clock for http.ServeContent only
