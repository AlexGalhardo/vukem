// Package testutil holds helpers shared by the core packages' tests.
package testutil

import (
	"os"
	"strings"
	"testing"
	"time"
)

// removeTimeout bounds the cleanup retries; observed delays are a few
// milliseconds.
const removeTimeout = 5 * time.Second

// TempDir is t.TempDir for directories that hold SQLite files: the
// directory is removed after the test's later cleanups (database Close
// included) and the removal is retried for a few seconds.
//
// shortcut: on Windows, Defender and the search indexer briefly open
// files that were just written; deleting such a file only marks it
// delete-pending, so removing the directory fails with "directory is not
// empty" for a few milliseconds. It happens with plain os.WriteFile files
// too (no handle of ours is open), and testing.T.TempDir retries only
// access-denied errors. Drop this helper when testing retries
// ERROR_DIR_NOT_EMPTY.
func TempDir(tb testing.TB) string {
	tb.Helper()

	//nolint:usetesting // t.TempDir cannot retry its removal; see the shortcut above.
	dir, err := os.MkdirTemp("", pattern(tb.Name()))
	if err != nil {
		tb.Fatalf("TempDir: %v", err)
	}

	tb.Cleanup(func() {
		deadline := time.Now().Add(removeTimeout)

		for {
			err := os.RemoveAll(dir)
			if err == nil {
				return
			}

			if time.Now().After(deadline) {
				tb.Errorf("TempDir RemoveAll cleanup: %v", err)
				return
			}

			time.Sleep(10 * time.Millisecond)
		}
	})

	return dir
}

// pattern makes a MkdirTemp pattern from a test name, which can contain
// path separators and other characters invalid in file names.
func pattern(name string) string {
	name = strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			return r
		default:
			return '_'
		}
	}, name)

	return name[:min(len(name), 64)] + "-*"
}
