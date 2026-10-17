package i18n

import (
	"slices"
	"strings"
	"testing"
)

// safeTag reports whether s only holds characters a canonical tag may use,
// so it can never break a header, cookie or file path.
func safeTag(s string) bool {
	return s != "" && !strings.ContainsFunc(s, func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '-'
	})
}

func FuzzCanonical(f *testing.F) {
	for _, s := range []string{"pt_br", "zh-hant-tw", "en", "es-419", "../etc", "en\r\nX: y", "fil-Latn-419", "a-b-c-d", "en--US"} {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, tag string) {
		canon, ok := Canonical(tag)
		if !ok {
			return
		}

		if !safeTag(canon) {
			t.Fatalf("Canonical(%q) = %q, has unsafe characters", tag, canon)
		}

		if again, ok := Canonical(canon); !ok || again != canon {
			t.Fatalf("Canonical(Canonical(%q)) = %q, %v, want %q", tag, again, ok, canon)
		}
	})
}

func FuzzParseAcceptLanguage(f *testing.F) {
	for _, s := range []string{
		"pt-BR,pt;q=0.9,en;q=0.8", "*", "en;q=0", "en;q=1.5", "de;q=abc, fr", "zh-Hant-TW;q=0.5,zh;q=0.6",
		strings.Repeat("en,", 40), "en;q=0.5;q=0.6", "\xff",
	} {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, header string) {
		tags := parseAcceptLanguage(header)
		if len(tags) > maxAcceptLanguages {
			t.Fatalf("parseAcceptLanguage(%q) returned %d tags, want at most %d", header, len(tags), maxAcceptLanguages)
		}

		for i, tag := range tags {
			if canon, ok := Canonical(tag); !ok || canon != tag {
				t.Fatalf("parseAcceptLanguage(%q) returned non-canonical %q", header, tag)
			}

			if slices.Contains(tags[:i], tag) {
				t.Fatalf("parseAcceptLanguage(%q) returned %q twice", header, tag)
			}
		}
	})
}
