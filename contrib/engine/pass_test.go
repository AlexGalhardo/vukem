package engine

import (
	"bytes"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestApplyDirectStdioPassesFilesThrough(t *testing.T) {
	t.Parallel()

	in, err := os.CreateTemp(t.TempDir(), "in")
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = in.Close() })

	out, err := os.CreateTemp(t.TempDir(), "out")
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = out.Close() })

	cmd := exec.Command("tool")
	applyDirectStdio(cmd, out, in, nil)

	if cmd.Stdin != in || cmd.Stdout != out {
		t.Fatalf("stdio = %v %v, want the *os.File values with no wrapper", cmd.Stdin, cmd.Stdout)
	}

	var buf bytes.Buffer

	copied := strings.NewReader("x")
	applyDirectStdio(cmd, &buf, nil, copied)

	if cmd.Stdin != copied || cmd.Stdout != &buf {
		t.Fatal("a non-file stream was replaced")
	}
}
