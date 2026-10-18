// Package archive reads a user-supplied archive under a policy, and hands
// back entries whose names are already safe keys.
//
// Every application that accepts an archive needs the same defences, and
// getting any one of them wrong is a vulnerability rather than a bug, so
// they are here rather than in each app:
//
//  1. an entry count cap, before anything is read;
//  2. a declared-uncompressed-size cap, per entry and in total, read from
//     the **central directory** — a bomb is refused on its claim rather
//     than by inflating it to find out;
//  3. the entry name never becomes a path. [Zip] takes no path of any
//     kind and returns a key, so a name cannot reach a file operation;
//  4. a name-length cap. The name does not reach a path on a correct
//     design, but it does reach the metadata and from there a consumer's
//     asset loader, so it is bounded and checked for text that disguises
//     itself (control characters, a right-to-left override);
//  5. collision detection on the key. This is the one that **corrupts
//     instead of failing**: "walk/frame.png" and "jump/frame.png" collapse
//     to one key once the directory is dropped, so an app writes both
//     frames under one name while its metadata describes one, putting
//     every later frame off by one and delivering a wrong result as a
//     correct one.
//
// What stays with the application is the per-entry content rule: what a
// frame may contain, how big an image may be, which types are acceptable
// as input. This package has no opinion on any of that.
//
// The entries are metadata only until [Entry.Open] is called, and that
// reader is held to the size the entry declared, so the declared total
// bounds what an archive can ever yield.
package archive

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"math"
	"path"
	"strings"
	"unicode"
	"unicode/utf8"
)

// DefaultMaxNameLength bounds an entry key when [Policy.MaxNameLength] is
// zero. It is the longest name most filesystems and asset loaders accept.
const DefaultMaxNameLength = 255

// Errors a caller distinguishes: ErrPolicy is the application's own
// mistake and belongs in a 500, the rest are the client's and belong in a
// 422 with a message of the handler's choosing. None of them carries the
// entry's raw name, which is attacker-controlled text that a naive handler
// would put in a response body.
var (
	// ErrPolicy reports an unusable Policy: a configuration error.
	ErrPolicy = errors.New("archive: policy needs MaxEntries, MaxTotalSize and MaxEntrySize above zero")
	// ErrTooManyEntries reports more entries than the policy allows.
	ErrTooManyEntries = errors.New("archive: too many entries")
	// ErrTooLarge reports a declared size over the policy, per entry or in
	// total. It is decided from the central directory, so an archive that
	// claims to be a bomb is refused without being inflated.
	ErrTooLarge = errors.New("archive: declared size over the limit")
	// ErrNameTooLong reports an entry key longer than the policy allows.
	ErrNameTooLong = errors.New("archive: entry name too long")
	// ErrUnsafeName reports a key that is not usable as one: empty, a path
	// of any kind, or text that disguises itself.
	ErrUnsafeName = errors.New("archive: unsafe entry name")
	// ErrDuplicateKey reports two entries resolving to one key. Refused
	// rather than overwritten: silently keeping the last one corrupts the
	// result instead of failing it.
	ErrDuplicateKey = errors.New("archive: duplicate entry key")
)

// Policy bounds what [Zip] accepts. The three caps are required: there is
// no safe default for "how much of this archive may this application
// hold", and guessing one is how a bomb gets through.
type Policy struct {
	// MaxEntries is the most entries an archive may hold (required).
	MaxEntries int
	// MaxTotalSize is the largest sum of declared uncompressed sizes
	// (required). Many entries just under MaxEntrySize are a bomb too.
	MaxTotalSize int64
	// MaxEntrySize is the largest declared uncompressed size of one entry
	// (required).
	MaxEntrySize int64
	// MaxNameLength bounds the key in bytes (DefaultMaxNameLength when 0).
	MaxNameLength int
	// Key derives an entry's key from its name; the default is the name's
	// last element, which is what drops the directory. Whatever it
	// returns is validated like any other key, so an application cannot
	// opt out of the safe-key guarantee by supplying its own.
	Key func(name string) string
}

// Entry is one archive member whose key is already safe to use as an
// identifier: non-empty, no separators, no "." or "..", within the length
// cap, valid UTF-8 without control characters, and unique in the archive.
type Entry struct {
	// Key identifies the entry. It is derived, never the raw name.
	Key string
	// Size is the uncompressed size the archive declares, already checked
	// against the policy.
	Size int64

	file *zip.File
}

// Open reads the entry's content, bounded by the size it declared: a
// stream that tries to yield more is cut off with an error. archive/zip
// already rejects the simple inconsistency of a central directory that
// disagrees with the local header, so this is defence in depth, and it is
// what lets the declared total bound what the archive can ever yield.
//
// Close it before opening the next entry's reader.
func (e Entry) Open() (io.ReadCloser, error) {
	rc, err := e.file.Open()
	if err != nil {
		// The driver error can name the archive's internals; the caller
		// gets the fact, not the detail.
		return nil, fmt.Errorf("%w: entry %q cannot be read", ErrTooLarge, e.Key)
	}

	return &boundedReader{rc: rc, left: e.Size, key: e.Key}, nil
}

// boundedReader stops at the declared size rather than trusting the
// stream to end there.
type boundedReader struct {
	rc   io.ReadCloser
	left int64
	key  string
}

func (b *boundedReader) Read(p []byte) (int, error) {
	if b.left <= 0 {
		// One extra byte is read to tell "exactly the declared size" from
		// "more than it declared".
		var probe [1]byte
		if n, _ := b.rc.Read(probe[:]); n > 0 {
			return 0, fmt.Errorf("%w: entry %q yields more than it declares", ErrTooLarge, b.key)
		}

		return 0, io.EOF
	}

	if int64(len(p)) > b.left {
		p = p[:b.left]
	}

	n, err := b.rc.Read(p)
	b.left -= int64(n)

	return n, err
}

func (b *boundedReader) Close() error { return b.rc.Close() }

// Zip reads the central directory of a ZIP archive and returns its
// entries under p, without decompressing anything. It takes no path: a
// reader, a size and a policy in, safe keys out, which is what keeps an
// entry name from ever being an argument to a file operation.
//
// The archive is read in full before any entry is returned, so a policy
// violation anywhere refuses the whole archive. An application that wants
// a partial result should split the archive instead.
func Zip(r io.ReaderAt, size int64, p Policy) ([]Entry, error) {
	if p.MaxEntries <= 0 || p.MaxTotalSize <= 0 || p.MaxEntrySize <= 0 {
		return nil, ErrPolicy
	}

	if p.MaxNameLength <= 0 {
		p.MaxNameLength = DefaultMaxNameLength
	}

	if p.Key == nil {
		p.Key = path.Base
	}

	zr, err := zip.NewReader(r, size)
	if err != nil {
		return nil, fmt.Errorf("archive: not a readable zip: %w", err)
	}

	entries := make([]Entry, 0, min(len(zr.File), p.MaxEntries))
	seen := make(map[string]struct{}, len(entries))

	var total uint64

	for i, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}

		if len(entries) == p.MaxEntries {
			return nil, fmt.Errorf("%w: more than %d", ErrTooManyEntries, p.MaxEntries)
		}

		// The declared size, from the central directory. Checked before
		// anything is inflated, which is the whole point: an archive that
		// claims 8 MiB over a five-byte payload is refused on the claim.
		// The declared size is an attacker-controlled uint64, so the
		// arithmetic stays unsigned: a value past MaxInt64 would wrap
		// negative and slip under the total cap.
		declared := f.UncompressedSize64
		if declared > math.MaxInt64 || declared > uint64(p.MaxEntrySize) {
			return nil, fmt.Errorf("%w: entry %d declares %d bytes, limit %d",
				ErrTooLarge, i, declared, p.MaxEntrySize)
		}

		total += declared
		if total > uint64(p.MaxTotalSize) {
			return nil, fmt.Errorf("%w: entries declare more than %d bytes in total",
				ErrTooLarge, p.MaxTotalSize)
		}

		// Checked on the raw name, before Key sees it. Dropping an
		// ordinary directory is intended ("frames/walk/001.png" is a
		// legitimate entry), but a name that tries to escape is not, and
		// the default Key would quietly turn "../../etc/passwd" into the
		// perfectly valid key "passwd" — safe to use, and an archive the
		// application should never have accepted, with nothing in the log
		// to say so.
		if err := rejectEscape(f.Name, i); err != nil {
			return nil, err
		}

		key, err := safeKey(p.Key(f.Name), i, p.MaxNameLength)
		if err != nil {
			return nil, err
		}

		if _, dup := seen[key]; dup {
			return nil, fmt.Errorf("%w: %q appears twice (entry names differing only by directory)",
				ErrDuplicateKey, key)
		}

		seen[key] = struct{}{}
		entries = append(entries, Entry{Key: key, Size: int64(declared), file: f})
	}

	return entries, nil
}

// rejectEscape refuses a raw entry name that reaches outside its own
// archive: an absolute path, a drive letter, or a ".." segment. The error
// names the index and not the name.
func rejectEscape(name string, index int) error {
	slashed := strings.ReplaceAll(name, "\\", "/")

	switch {
	case strings.HasPrefix(slashed, "/"):
		return fmt.Errorf("%w: entry %d is an absolute path", ErrUnsafeName, index)
	case len(name) > 1 && name[1] == ':':
		return fmt.Errorf("%w: entry %d names a drive", ErrUnsafeName, index)
	}

	for seg := range strings.SplitSeq(slashed, "/") {
		if seg == ".." {
			return fmt.Errorf("%w: entry %d walks out of the archive", ErrUnsafeName, index)
		}
	}

	return nil
}

// safeKey validates a derived key. The error names the entry's index and
// never the name itself: the name is attacker-controlled text, and an
// error is the thing most likely to be rendered somewhere.
func safeKey(key string, index, maxLen int) (string, error) {
	if len(key) > maxLen {
		return "", fmt.Errorf("%w: entry %d is %d bytes, limit %d", ErrNameTooLong, index, len(key), maxLen)
	}

	switch {
	case key == "", key == ".", key == "..":
		return "", fmt.Errorf("%w: entry %d has no usable name", ErrUnsafeName, index)
	case strings.ContainsAny(key, `/\`):
		return "", fmt.Errorf("%w: entry %d resolves to a path", ErrUnsafeName, index)
	case !utf8.ValidString(key):
		return "", fmt.Errorf("%w: entry %d is not valid UTF-8", ErrUnsafeName, index)
	case strings.ContainsFunc(key, disguising):
		// A control character or a bidirectional override travels into
		// whatever renders the metadata, where it can hide an extension
		// ("evil<RLO>png.exe" reads as "evilexe.gnp").
		return "", fmt.Errorf("%w: entry %d holds a control or direction-changing character",
			ErrUnsafeName, index)
	}

	return key, nil
}

// disguising reports a rune that changes how the text around it reads.
func disguising(r rune) bool {
	return unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || r == 0xFEFF
}
