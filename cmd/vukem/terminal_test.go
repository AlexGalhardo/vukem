package main

import (
	"os"
	"testing"
)

// The null device is a character device, but nobody types at it: a script run
// with stdin from /dev/null must not be asked questions.
func TestNullDeviceIsNotATerminal(t *testing.T) {
	t.Parallel()

	f, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}

	defer f.Close()

	if isTerminal(f) {
		t.Errorf("isTerminal(%s) = true, want false", os.DevNull)
	}
}

func TestPipeIsNotATerminal(t *testing.T) {
	t.Parallel()

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}

	defer r.Close()
	defer w.Close()

	if isTerminal(r) {
		t.Error("isTerminal(pipe) = true, want false")
	}
}
