package main

import (
	"errors"
	"os"
	"testing"
	"time"
)

// TestDiffExportsTimesOut runs this test binary as an apidiff that never
// finishes: diffExports must give up with errDiffTimeout instead of hanging.
func TestDiffExportsTimesOut(t *testing.T) {
	if os.Getenv("APICHECK_HANG") == "1" {
		time.Sleep(time.Hour)
		os.Exit(0)
	}

	t.Setenv("APICHECK_HANG", "1")

	start := time.Now()

	_, err := diffExports(
		t.Context(),
		t.TempDir(),
		os.Args[0],
		[]string{"-test.run=^TestDiffExportsTimesOut$"},
		time.Second,
	)
	if !errors.Is(err, errDiffTimeout) {
		t.Fatalf("diffExports = %v, want errDiffTimeout", err)
	}

	if d := time.Since(start); d > time.Minute {
		t.Errorf("diffExports returned after %v, want about the 1s timeout", d)
	}
}
