package storagetest_test

import (
	"testing"

	"github.com/AlexGalhardo/vukem/storage"
	"github.com/AlexGalhardo/vukem/storage/storagetest"
)

// A disk implementation runs the suite, and the path traversal checks,
// from its tests.
func ExampleRun() {
	_ = func(t *testing.T) {
		t.Helper()

		disk := storage.NewMemory("/files")
		storagetest.Run(t, disk)
		storagetest.RunTraversal(t, disk)
	}
}
