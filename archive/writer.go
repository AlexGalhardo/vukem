package archive

import (
	"archive/zip"
	"fmt"
	"io"
)

// Writer writes a zip that [Zip] can read. Entry names are flat keys checked
// by the same rules as the reader, and the declared sizes are held to the
// same [Policy] before anything is compressed. Close it before reading the
// bytes back.
type Writer struct {
	zw      *zip.Writer
	p       Policy
	entries int
	total   int64
	seen    map[string]struct{}
	closed  bool
}

// NewWriter starts a zip on w. p must set MaxEntries, MaxTotalSize and
// MaxEntrySize, the same caps [Zip] requires. [Policy.Key] is not used:
// Add's name is the key.
func NewWriter(w io.Writer, p Policy) (*Writer, error) {
	if p.MaxEntries <= 0 || p.MaxTotalSize <= 0 || p.MaxEntrySize <= 0 {
		return nil, ErrPolicy
	}

	if p.MaxNameLength <= 0 {
		p.MaxNameLength = DefaultMaxNameLength
	}

	return &Writer{
		zw:   zip.NewWriter(w),
		p:    p,
		seen: make(map[string]struct{}),
	}, nil
}

// Add writes one regular file under name. name must be a flat safe key (no
// directory, no escape). The length of data counts against MaxEntrySize and
// MaxTotalSize. A refused name or size writes nothing.
func (w *Writer) Add(name string, data []byte) error {
	if w.closed {
		return fmt.Errorf("%w: writer is closed", ErrPolicy)
	}

	if w.entries == w.p.MaxEntries {
		return fmt.Errorf("%w: more than %d", ErrTooManyEntries, w.p.MaxEntries)
	}

	// Same order as Zip: an escape is not a usable key, then the key rules.
	if err := rejectEscape(name, w.entries); err != nil {
		return err
	}

	key, err := safeKey(name, w.entries, w.p.MaxNameLength)
	if err != nil {
		return err
	}

	size := int64(len(data))
	if size > w.p.MaxEntrySize || w.total > w.p.MaxTotalSize-size {
		return fmt.Errorf("%w: entry %d declares %d bytes, limit %d (total limit %d)",
			ErrTooLarge, w.entries, size, w.p.MaxEntrySize, w.p.MaxTotalSize)
	}

	if _, dup := w.seen[key]; dup {
		return fmt.Errorf("%w: %q appears twice", ErrDuplicateKey, key)
	}

	out, err := w.zw.Create(key)
	if err != nil {
		return fmt.Errorf("archive: write %q: %w", key, err)
	}

	if _, err := out.Write(data); err != nil {
		return fmt.Errorf("archive: write %q: %w", key, err)
	}

	w.seen[key] = struct{}{}
	w.entries++
	w.total += size

	return nil
}

// Close finishes the zip. Add fails after Close.
func (w *Writer) Close() error {
	if w.closed {
		return nil
	}

	w.closed = true

	if err := w.zw.Close(); err != nil {
		return fmt.Errorf("archive: close: %w", err)
	}

	return nil
}
