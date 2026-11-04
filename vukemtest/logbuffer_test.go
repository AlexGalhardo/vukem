package vukemtest

import (
	"strings"
	"testing"
)

func TestLogBufferErrorsKeepsTheLastTenErrorLines(t *testing.T) {
	t.Parallel()

	var l logBuffer

	for i := range 15 {
		_, _ = l.Write([]byte("time=x level=INFO msg=ok\n"))
		_, _ = l.Write([]byte("time=x level=ERROR msg=boom" + string(rune('a'+i)) + "\n"))
	}

	lines := strings.Split(l.errors(), "\n")
	if len(lines) != 10 || !strings.HasSuffix(lines[9], "boomo") || !strings.HasSuffix(lines[0], "boomf") {
		t.Fatalf("errors() = %q, want the last ten ERROR lines", lines)
	}

	if (&logBuffer{}).errors() != "" {
		t.Fatal("an empty log reports errors")
	}
}

func TestLogBufferIsBounded(t *testing.T) {
	t.Parallel()

	var l logBuffer

	chunk := []byte(strings.Repeat("x", 1024) + "\n")
	for range 200 {
		_, _ = l.Write(chunk)
	}

	if n := l.buf.Len(); n > maxLogBytes {
		t.Fatalf("buffer holds %d bytes, want at most %d", n, maxLogBytes)
	}
}
