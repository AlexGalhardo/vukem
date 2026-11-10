package graphql

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
)

func TestLexerErrorLocations(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		src  string
		want Location
	}{
		{"single line", "{ a ? }", Location{1, 5}},
		{"long line", "{ " + strings.Repeat("a ", 100) + "? }", Location{1, 203}},
		{"multiple lines", "{\n  a\n  b ?\n}", Location{3, 5}},
		{"multibyte before error", "{ a(b: \"é€😀\") ? }", Location{1, 15}},
		{"crlf", "{\r\n  a\r\n  ? }", Location{3, 3}},
		{"lone cr", "{\r  a ?", Location{2, 5}},
		{"bom", string(rune(0xFEFF)) + "{ ? }", Location{1, 4}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := parseQuery(tt.src, 10000)
			e, ok := errors.AsType[*Error](err)
			if !ok {
				t.Fatalf("parseQuery(%q) error = %v, want *Error", tt.src, err)
			}

			if diff := cmp.Diff([]Location{tt.want}, e.Locations); diff != "" {
				t.Errorf("locations mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func longLineDoc(spaces, tokens int) string {
	return strings.Repeat(" ", spaces) + strings.Repeat("a ", tokens)
}

func lexAll(src string) {
	l := newLexer(src, 0)
	for {
		tok, err := l.next()
		if err != nil || tok.kind == tokEOF {
			return
		}
	}
}

func BenchmarkLexLongLine(b *testing.B) {
	src := longLineDoc(200<<10, 5000)

	for b.Loop() {
		lexAll(src)
	}
}

func TestLexLongLineIsLinear(t *testing.T) {
	if testing.Short() {
		t.Skip("timing test")
	}

	measure := func(src string) time.Duration {
		best := time.Duration(1<<63 - 1)

		for range 3 {
			start := time.Now()
			lexAll(src)
			best = min(best, time.Since(start))
		}

		return best
	}

	one := measure(longLineDoc(1<<20, 25000))
	two := measure(longLineDoc(2<<20, 50000))

	if two > 4*one {
		t.Errorf("lexing 2x input took %v vs %v for 1x: want <= 4x", two, one)
	}
}
