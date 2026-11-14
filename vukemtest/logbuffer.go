package vukemtest

import (
	"bytes"
	"strings"
	"sync"
)

// maxLogBytes bounds what the test app keeps of its log: the tail is what explains the last failure.
const maxLogBytes = 64 << 10

// logBuffer is the app's log output in a test. It keeps the last maxLogBytes so a failed assertion on a 5xx
// response can show the logged error instead of only the generic body.
type logBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (l *logBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.buf.Write(p)

	if extra := l.buf.Len() - maxLogBytes; extra > 0 {
		l.buf.Next(extra)
	}

	return len(p), nil
}

func (l *logBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()

	return l.buf.String()
}

// errors returns the last ERROR lines (at most ten), or "" when there are none.
func (l *logBuffer) errors() string {
	l.mu.Lock()
	text := l.buf.String()
	l.mu.Unlock()

	var out []string

	for line := range strings.SplitSeq(text, "\n") {
		if strings.Contains(line, "level=ERROR") || strings.Contains(line, `"level":"ERROR"`) {
			out = append(out, line)
		}
	}

	if len(out) > 10 {
		out = out[len(out)-10:]
	}

	return strings.Join(out, "\n")
}
