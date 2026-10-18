//go:build unix

package vukem

import (
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// Opening a FIFO blocks until a writer appears: readConfigFile must
// refuse it without opening it, or @file:<fifo> hangs the boot.
func TestSecurity_A02_FileRefRefusesFIFOWithoutBlocking(t *testing.T) {
	t.Parallel()

	fifo := filepath.Join(t.TempDir(), "secret")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Skipf("mkfifo: %v", err)
	}

	done := make(chan error, 1)

	go func() {
		_, err := readConfigFile(fifo)
		done <- err
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("readConfigFile(fifo) = nil error, want a not-a-regular-file error")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("readConfigFile(fifo) blocked, want it refused before opening")
	}
}
