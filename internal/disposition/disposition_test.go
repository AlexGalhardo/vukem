package disposition

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestSanitize(t *testing.T) {
	t.Parallel()

	tests := []struct{ name, in, want string }{
		{"plain", "report.csv", "report.csv"},
		{"unix path", "a/b/c.txt", "c.txt"},
		{"windows path", `C:\x\y.txt`, "y.txt"},
		{"traversal", "../../etc/passwd", "passwd"},
		{"control chars", "a\x00b\r\nc\x7f.txt", "abc.txt"},
		{"c1 control", "a\u0085b.txt", "ab.txt"},
		{"line separators", "a\u2028b\u2029c.txt", "abc.txt"},
		{"bidi and zero width", "a\u202eb\u200bc.txt", "abc.txt"},
		{"invalid utf8", "a\xffb.txt", "ab.txt"},
		{"windows forbidden", `a<b>c:d"e|f?g*h.txt`, "a_b_c_d_e_f_g_h.txt"},
		{"leading dots", "..hidden", "hidden"},
		{"trailing dot and space", "name. . ", "name"},
		{"empty", "", fallback},
		{"only dots", "...", fallback},
		{"only controls", "\x00\x01", fallback},
		{"device with ext", "NUL.csv", "_NUL.csv"},
		{"device lower", "con", "_con"},
		{"device com1", "com1.txt", "_com1.txt"},
		{"device lpt9", "LPT9", "_LPT9"},
		{"com0 is fine", "COM0.txt", "COM0.txt"},
		{"com10 is fine", "COM10", "COM10"},
		{"console is fine", "console.txt", "console.txt"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := Sanitize(tt.in); got != tt.want {
				t.Errorf("Sanitize(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestSanitizeOr(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct{ in, empty, want string }{
		{"", "file", "file"},
		{"...", "x", "x"},
		{"a.txt", "file", "a.txt"},
	} {
		if got := SanitizeOr(tt.in, tt.empty); got != tt.want {
			t.Errorf("SanitizeOr(%q, %q) = %q, want %q", tt.in, tt.empty, got, tt.want)
		}
	}
}

func TestSanitize_longName(t *testing.T) {
	t.Parallel()

	got := Sanitize(strings.Repeat("a", 300) + ".pdf")
	if n := utf8.RuneCountInString(got); n != maxNameLen || !strings.HasSuffix(got, ".pdf") {
		t.Errorf("long name = %d runes, %q; want %d keeping .pdf", n, got[len(got)-8:], maxNameLen)
	}

	got = Sanitize(strings.Repeat("a", 300) + "." + strings.Repeat("e", 20))
	if n := utf8.RuneCountInString(got); n != maxNameLen {
		t.Errorf("long extension = %d runes, want %d", n, maxNameLen)
	}
}

func TestAttachment(t *testing.T) {
	t.Parallel()

	tests := []struct{ name, in, want string }{
		{"ascii", "report.csv", `attachment; filename="report.csv"`},
		{"quote", `a"b.txt`, `attachment; filename="a_b.txt"`},
		{"percent", "50%.txt", `attachment; filename="50_.txt"; filename*=UTF-8''50%25.txt`},
		{"non ascii", "relatório.pdf", `attachment; filename="relat_rio.pdf"; filename*=UTF-8''relat%C3%B3rio.pdf`},
		{"empty", "", `attachment; filename="download"`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := Attachment(tt.in); got != tt.want {
				t.Errorf("Attachment(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func FuzzSanitize(f *testing.F) {
	for _, s := range []string{"", "a.txt", "../x", `C:\a\b`, "NUL.csv", "a\u202eb", "\xff"} {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, in string) {
		got := Sanitize(in)
		if got == "" || utf8.RuneCountInString(got) > maxNameLen+1 || strings.ContainsAny(got, "/<>:\"|?*\x00\\") {
			t.Fatalf("Sanitize(%q) = %q", in, got)
		}
	})
}
