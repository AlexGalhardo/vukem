package archive_test

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/AlexGalhardo/vukem/archive"
)

// policy is a small, valid policy; each test tightens the bound it is about.
func policy() archive.Policy {
	return archive.Policy{MaxEntries: 10, MaxTotalSize: 1 << 20, MaxEntrySize: 1 << 16}
}

// zipOf builds a ZIP from name to content, as a well-behaved client would.
func zipOf(t *testing.T, files map[string]string, order ...string) ([]byte, int64) {
	t.Helper()

	var buf bytes.Buffer

	zw := zip.NewWriter(&buf)

	for _, n := range order {
		w, err := zw.Create(n)
		if err != nil {
			t.Fatal(err)
		}

		if _, err := io.WriteString(w, files[n]); err != nil {
			t.Fatal(err)
		}
	}

	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}

	return buf.Bytes(), int64(buf.Len())
}

func TestZipReadsEntriesAsSafeKeys(t *testing.T) {
	t.Parallel()

	raw, size := zipOf(t, map[string]string{
		"frames/001.png": "one",
		"frames/002.png": "two",
	}, "frames/001.png", "frames/002.png")

	entries, err := archive.Zip(bytes.NewReader(raw), size, policy())
	if err != nil {
		t.Fatal(err)
	}

	if diff := cmp.Diff([]string{"001.png", "002.png"}, keys(entries)); diff != "" {
		t.Errorf("keys diff (-want +got):\n%s", diff)
	}

	for i, e := range entries {
		if e.Size != 3 {
			t.Errorf("entry %d size = %d, want 3", i, e.Size)
		}

		rc, err := e.Open()
		if err != nil {
			t.Fatal(err)
		}

		b, err := io.ReadAll(rc)
		if err != nil {
			t.Fatal(err)
		}

		if err := rc.Close(); err != nil {
			t.Fatal(err)
		}

		if len(b) != 3 {
			t.Errorf("entry %d read %q", i, b)
		}
	}
}

// Defence 5, the one that corrupts instead of failing: two entries whose
// names differ only by directory collapse to one key, so an app would
// write both frames under one name and describe one in its metadata,
// putting every later frame off by one and delivering a wrong result as a
// correct one.
func TestZipRefusesCollidingKeys(t *testing.T) {
	t.Parallel()

	raw, size := zipOf(t, map[string]string{
		"walk/frame.png": "walking",
		"jump/frame.png": "jumping",
	}, "walk/frame.png", "jump/frame.png")

	_, err := archive.Zip(bytes.NewReader(raw), size, policy())
	if !errors.Is(err, archive.ErrDuplicateKey) {
		t.Fatalf("Zip() = %v, want ErrDuplicateKey", err)
	}

	// The key is named, so the operator can see which one clashed. It has
	// already passed the safe-key check, so it carries no control
	// characters into a log or a response.
	if !strings.Contains(err.Error(), "frame.png") {
		t.Errorf("error does not name the key: %v", err)
	}
}

// Defence 1.
func TestZipRefusesTooManyEntries(t *testing.T) {
	t.Parallel()

	files, order := map[string]string{}, []string(nil)

	for i := range 5 {
		n := string(rune('a'+i)) + ".txt"
		files[n], order = "x", append(order, n)
	}

	raw, size := zipOf(t, files, order...)

	p := policy()
	p.MaxEntries = 4

	if _, err := archive.Zip(bytes.NewReader(raw), size, p); !errors.Is(err, archive.ErrTooManyEntries) {
		t.Fatalf("Zip() = %v, want ErrTooManyEntries", err)
	}
}

// Defence 2: a bomb is refused on its claim, read from the central
// directory, rather than by inflating it. Proving it needs a hand-forged
// ZIP, because archive/zip's writer computes the size from what was
// actually written and will not lie.
func TestZipRefusesABombOnItsDeclaredSize(t *testing.T) {
	t.Parallel()

	raw, _ := zipOf(t, map[string]string{"bomb.bin": "small"}, "bomb.bin")
	forged := forgeDeclaredSize(t, raw, 8<<20)

	_, err := archive.Zip(bytes.NewReader(forged), int64(len(forged)), policy())
	if !errors.Is(err, archive.ErrTooLarge) {
		t.Fatalf("Zip() = %v, want ErrTooLarge from the declared size alone", err)
	}

	// Nothing was inflated to find that out: the claim is 8 MiB and the
	// payload is five bytes, so a reader that had to decompress to notice
	// would have succeeded.
	if !strings.Contains(err.Error(), "declares") {
		t.Errorf("error does not say the size was declared: %v", err)
	}
}

// The total is capped as well as each entry: many entries just under the
// per-entry cap are a bomb too.
func TestZipRefusesADeclaredTotalOverTheCap(t *testing.T) {
	t.Parallel()

	files, order := map[string]string{}, []string(nil)
	for i := range 4 {
		n := string(rune('a'+i)) + ".bin"
		files[n], order = strings.Repeat("x", 300), append(order, n)
	}

	raw, size := zipOf(t, files, order...)

	p := policy()
	p.MaxTotalSize = 1000 // 4 x 300 declared

	if _, err := archive.Zip(bytes.NewReader(raw), size, p); !errors.Is(err, archive.ErrTooLarge) {
		t.Fatalf("Zip() = %v, want ErrTooLarge for the declared total", err)
	}
}

// Defence 3 and 4: the name never becomes a path, and a long one is
// refused. It does not reach a path on a correct design, but it does reach
// the metadata and from there a consumer's asset loader.
func TestZipRefusesUnsafeAndOverlongNames(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		entry string
		want  error
	}{
		"traversal":         {"../../etc/passwd", archive.ErrUnsafeName},
		"absolute":          {"/etc/passwd", archive.ErrUnsafeName},
		"windows separator": {"..\\..\\windows\\system32", archive.ErrUnsafeName},
		"bare dots":         {"frames/..", archive.ErrUnsafeName},
		// A right-to-left override disguises the extension, and a control
		// character travels into whatever renders the metadata.
		"rtl override":      {"frames/evil\u202epng.exe", archive.ErrUnsafeName},
		"control character": {"frames/a\x01b.png", archive.ErrUnsafeName},
		"too long":          {"frames/" + strings.Repeat("a", 300) + ".png", archive.ErrNameTooLong},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			raw, size := zipOf(t, map[string]string{tt.entry: "x"}, tt.entry)

			_, err := archive.Zip(bytes.NewReader(raw), size, policy())
			if !errors.Is(err, tt.want) {
				t.Fatalf("Zip(%q) = %v, want %v", tt.entry, err, tt.want)
			}

			// The raw name is attacker-controlled text and never reaches
			// the error, which a naive handler puts in a response body.
			if strings.Contains(err.Error(), tt.entry) {
				t.Errorf("error echoes the entry name: %v", err)
			}
		})
	}
}

// Defence in depth for defence 2: an entry yields no more bytes than it
// declared. archive/zip rejects the simple inconsistency (a central
// directory claiming 10 bytes over a 5 MiB payload fails with "zip: not a
// valid zip file"), but the guarantee an app needs is that the declared
// total bounds what it can ever read, so Open holds the stream to it.
func TestEntryOpenIsBoundedByTheDeclaredSize(t *testing.T) {
	t.Parallel()

	raw, size := zipOf(t, map[string]string{"a.txt": strings.Repeat("x", 500)}, "a.txt")

	entries, err := archive.Zip(bytes.NewReader(raw), size, policy())
	if err != nil {
		t.Fatal(err)
	}

	rc, err := entries[0].Open()
	if err != nil {
		t.Fatal(err)
	}

	defer func() { _ = rc.Close() }()

	n, err := io.Copy(io.Discard, rc)
	if err != nil || n != 500 {
		t.Fatalf("read %d bytes, %v, want 500", n, err)
	}
}

// Directory entries are not entries: they carry no content and would
// produce an empty key.
func TestZipSkipsDirectories(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	zw := zip.NewWriter(&buf)
	if _, err := zw.Create("frames/"); err != nil {
		t.Fatal(err)
	}

	w, err := zw.Create("frames/a.png")
	if err != nil {
		t.Fatal(err)
	}

	if _, err := io.WriteString(w, "x"); err != nil {
		t.Fatal(err)
	}

	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}

	entries, err := archive.Zip(bytes.NewReader(buf.Bytes()), int64(buf.Len()), policy())
	if err != nil {
		t.Fatal(err)
	}

	if diff := cmp.Diff([]string{"a.png"}, keys(entries)); diff != "" {
		t.Errorf("keys diff (-want +got):\n%s", diff)
	}
}

// An unusable policy is a configuration error, never a client's fault.
func TestZipRefusesAnUnusablePolicy(t *testing.T) {
	t.Parallel()

	raw, size := zipOf(t, map[string]string{"a.txt": "x"}, "a.txt")

	for name, p := range map[string]archive.Policy{
		"no entry cap":  {MaxTotalSize: 10, MaxEntrySize: 10},
		"no total cap":  {MaxEntries: 1, MaxEntrySize: 10},
		"no entry size": {MaxEntries: 1, MaxTotalSize: 10},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if _, err := archive.Zip(bytes.NewReader(raw), size, p); !errors.Is(err, archive.ErrPolicy) {
				t.Fatalf("Zip() = %v, want ErrPolicy", err)
			}
		})
	}
}

// A custom Key is the app's, and its result is validated the same way: an
// app cannot opt out of the safe-key guarantee by supplying its own.
func TestZipValidatesACustomKey(t *testing.T) {
	t.Parallel()

	raw, size := zipOf(t, map[string]string{"frames/a.png": "x"}, "frames/a.png")

	p := policy()
	p.Key = func(string) string { return "../escape" }

	if _, err := archive.Zip(bytes.NewReader(raw), size, p); !errors.Is(err, archive.ErrUnsafeName) {
		t.Fatalf("Zip() with a Key returning a path = %v, want ErrUnsafeName", err)
	}
}

func keys(entries []archive.Entry) []string {
	out := make([]string, len(entries))
	for i, e := range entries {
		out[i] = e.Key
	}

	return out
}

// forgeDeclaredSize rewrites the uncompressed size in the central
// directory, which is what a bomb lies about. archive/zip's writer cannot
// do it: it computes the size from what was written.
func forgeDeclaredSize(t *testing.T, raw []byte, size uint32) []byte {
	t.Helper()

	at := bytes.Index(raw, []byte{0x50, 0x4b, 0x01, 0x02})
	if at < 0 {
		t.Fatal("no central directory header in the test archive")
	}

	out := bytes.Clone(raw)
	binary.LittleEndian.PutUint32(out[at+24:], size)

	return out
}

// docs/security.md A01: an archive entry name never reaches a path. Zip's
// signature takes no path of any kind, which is what makes that true by
// construction rather than by review — pinned here so a later path
// parameter cannot quietly make entry names attacker-controlled input to
// a file operation again.
func TestSecurity_A01_ArchiveTakesNoPath(t *testing.T) {
	t.Parallel()

	fn := reflect.TypeOf(archive.Zip)

	want := []reflect.Type{
		reflect.TypeFor[io.ReaderAt](),
		reflect.TypeFor[int64](),
		reflect.TypeFor[archive.Policy](),
	}

	got := make([]reflect.Type, fn.NumIn())
	for i := range fn.NumIn() {
		got[i] = fn.In(i)
	}

	if diff := cmp.Diff(want, got, cmp.Comparer(func(a, b reflect.Type) bool { return a == b })); diff != "" {
		t.Errorf("Zip's parameters changed (-want +got):\n%s\na path parameter here would make an "+
			"entry name an argument to a file operation", diff)
	}

	// Policy carries no path either: its only func field derives a key
	// from a name, and whatever it returns is validated.
	for f := range reflect.TypeFor[archive.Policy]().Fields() {
		if f.Type.Kind() == reflect.String {
			t.Errorf("Policy.%s is a string; a path on the policy is the same hazard", f.Name)
		}
	}
}

// docs/security.md A05: a declared size bounds what an entry can yield,
// so an archive cannot inflate past its own claim.
func TestSecurity_A05_ArchiveEntriesCannotExceedTheirDeclaredSize(t *testing.T) {
	t.Parallel()

	raw, size := zipOf(t, map[string]string{"a.txt": strings.Repeat("x", 400)}, "a.txt")

	entries, err := archive.Zip(bytes.NewReader(raw), size, policy())
	if err != nil {
		t.Fatal(err)
	}

	// Shrink the declared size behind the reader's back: the bound is the
	// entry's own claim, whatever the stream holds.
	entries[0].Size = 10

	rc, err := entries[0].Open()
	if err != nil {
		t.Fatal(err)
	}

	defer func() { _ = rc.Close() }()

	n, err := io.Copy(io.Discard, rc)
	if !errors.Is(err, archive.ErrTooLarge) {
		t.Fatalf("read %d bytes, %v, want ErrTooLarge once the claim ran out", n, err)
	}

	if n > 10 {
		t.Errorf("read %d bytes past a declared 10", n)
	}
}
