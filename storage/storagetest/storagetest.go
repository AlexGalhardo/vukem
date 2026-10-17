// Package storagetest is the conformance suite every storage.Disk passes
// (local and memory in core, S3 in drivers/s3). A new disk calls [Run]
// and [RunTraversal] from its tests.
//
// See https://github.com/AlexGalhardo/vukem/blob/main/docs/subsystems.md#storage.
package storagetest

import (
	"bytes"
	"errors"
	"io/fs"
	"slices"
	"testing"

	"github.com/AlexGalhardo/vukem/storage"
)

// Run checks d, which must start empty.
func Run(t *testing.T, d storage.Disk) {
	t.Helper()

	ctx := t.Context()

	if err := storage.PutBytes(ctx, d, "docs/a.txt", []byte("alpha")); err != nil {
		t.Fatal(err)
	}

	if err := storage.PutBytes(ctx, d, "docs/sub/b.txt", []byte("beta")); err != nil {
		t.Fatal(err)
	}

	if err := storage.PutBytes(ctx, d, "c.txt", []byte("gamma")); err != nil {
		t.Fatal(err)
	}

	if got, err := storage.Get(ctx, d, "docs/a.txt"); err != nil || string(got) != "alpha" {
		t.Fatalf("Get = %q %v", got, err)
	}

	// Overwrite replaces the whole content.
	if err := storage.PutBytes(ctx, d, "docs/a.txt", []byte("A")); err != nil {
		t.Fatal(err)
	}

	if got, _ := storage.Get(ctx, d, "docs/a.txt"); string(got) != "A" {
		t.Fatalf("overwrite = %q", got)
	}

	if ok, err := d.Exists(ctx, "docs/sub/b.txt"); !ok || err != nil {
		t.Fatalf("Exists = %v %v", ok, err)
	}

	if ok, _ := d.Exists(ctx, "docs"); ok {
		t.Fatal("a directory reported as a file")
	}

	all, err := d.List(ctx, "")
	if err != nil || !slices.Equal(all, []string{"c.txt", "docs/a.txt", "docs/sub/b.txt"}) {
		t.Fatalf("List = %v %v", all, err)
	}

	if docs, _ := d.List(ctx, "docs"); !slices.Equal(docs, []string{"docs/a.txt", "docs/sub/b.txt"}) {
		t.Fatalf("List(docs) = %v", docs)
	}

	if none, err := d.List(ctx, "missing"); err != nil || len(none) != 0 {
		t.Fatalf("List(missing) = %v %v", none, err)
	}

	if err := d.Delete(ctx, "c.txt"); err != nil {
		t.Fatal(err)
	}

	if err := d.Delete(ctx, "c.txt"); err != nil {
		t.Fatal("deleting a missing file failed")
	}

	if _, err := storage.Get(ctx, d, "c.txt"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("deleted file = %v", err)
	}

	if _, err := d.Open(ctx, "docs"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("Open(dir) = %v", err)
	}

	big := bytes.Repeat([]byte("x"), 1<<20)
	if err := storage.PutBytes(ctx, d, "big.bin", big); err != nil {
		t.Fatal(err)
	}

	if got, _ := storage.Get(ctx, d, "big.bin"); !bytes.Equal(got, big) {
		t.Fatal("large file corrupted")
	}
}

// RunTraversal checks that d rejects paths escaping the disk.
func RunTraversal(t *testing.T, d storage.Disk) {
	t.Helper()

	ctx := t.Context()

	for _, p := range []string{"../escape.txt", "/etc/passwd", "a/../../b", `..\x`, "C:/win.ini", "", ".", "a\x00b"} {
		if err := storage.PutBytes(ctx, d, p, []byte("x")); !errors.Is(err, storage.ErrInvalidPath) {
			t.Errorf("Put(%q) = %v, want ErrInvalidPath", p, err)
		}

		if _, err := d.Open(ctx, p); err == nil {
			t.Errorf("Open(%q) succeeded", p)
		}

		if _, err := d.Exists(ctx, p); err == nil {
			t.Errorf("Exists(%q) succeeded", p)
		}

		if err := d.Delete(ctx, p); err == nil {
			t.Errorf("Delete(%q) succeeded", p)
		}
	}

	if _, err := d.List(ctx, "../.."); err == nil {
		t.Error("List(../..) succeeded")
	}
}
