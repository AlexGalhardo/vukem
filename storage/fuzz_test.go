package storage_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/AlexGalhardo/vukem/storage"
)

// FuzzClean checks that an accepted storage path is relative, clean, has no
// ".." segment and none of the characters Windows treats specially, and is
// a local path on every OS.
func FuzzClean(f *testing.F) {
	for _, s := range []string{
		"a/b.txt", "../x", "a/../../x", "/abs", `a\b`, "C:/x", "a:stream", "a\x00b", ".", "a/./b//c", "...", "CON",
	} {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, p string) {
		c, err := storage.Clean(p)
		if err != nil {
			return
		}

		if c == "" || strings.HasPrefix(c, "/") || strings.ContainsAny(c, "\\\x00:") ||
			strings.Contains("/"+c+"/", "/../") || !filepath.IsLocal(filepath.FromSlash(c)) && !reserved(c) {
			t.Fatalf("Clean(%q) = %q, escapes the disk root", p, c)
		}

		if again, err := storage.Clean(c); err != nil || again != c {
			t.Fatalf("Clean(Clean(%q)) = %q, %v, want %q", p, again, err, c)
		}
	})
}

// reserved reports Windows device names ("NUL", "com1.txt"), which
// filepath.IsLocal rejects on Windows only; os.Root refuses to open them.
func reserved(p string) bool {
	for seg := range strings.SplitSeq(p, "/") {
		base, _, _ := strings.Cut(strings.ToUpper(strings.TrimRight(seg, " .")), ".")
		switch {
		case base == "CON", base == "PRN", base == "AUX", base == "NUL", base == "CONIN$", base == "CONOUT$":
			return true
		case len(base) == 4 && (strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT")):
			return true
		}
	}

	return false
}
