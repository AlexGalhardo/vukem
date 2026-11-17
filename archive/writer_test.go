package archive_test

import (
	"bytes"
	"errors"
	"io"
	"testing"

	"github.com/AlexGalhardo/vukem/archive"
)

func TestWriterRoundTrip(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	w, err := archive.NewWriter(&buf, policy())
	if err != nil {
		t.Fatal(err)
	}

	if err := w.Add("manifest.json", []byte("hello")); err != nil {
		t.Fatal(err)
	}

	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	entries, err := archive.Zip(bytes.NewReader(buf.Bytes()), int64(buf.Len()), policy())
	if err != nil {
		t.Fatal(err)
	}

	if len(entries) != 1 || entries[0].Key != "manifest.json" || entries[0].Size != 5 {
		t.Fatalf("Zip() = %+v, want one manifest.json of 5 bytes", entries)
	}

	rc, err := entries[0].Open()
	if err != nil {
		t.Fatal(err)
	}

	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatal(err)
	}

	if err := rc.Close(); err != nil {
		t.Fatal(err)
	}

	if string(got) != "hello" {
		t.Fatalf("entry = %q, want hello", got)
	}
}

func TestWriterRejectsUnsafeName(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	w, err := archive.NewWriter(&buf, policy())
	if err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{"../secret", `dir\file`, "frames/001.png", "C:drive"} {
		err := w.Add(name, []byte("x"))
		if !errors.Is(err, archive.ErrUnsafeName) {
			t.Errorf("Add(%q) = %v, want ErrUnsafeName", name, err)
		}
	}
}

func TestWriterRejectsSizeOverPolicy(t *testing.T) {
	t.Parallel()

	p := policy()
	p.MaxEntrySize = 4
	p.MaxTotalSize = 6

	var buf bytes.Buffer

	w, err := archive.NewWriter(&buf, p)
	if err != nil {
		t.Fatal(err)
	}

	if err := w.Add("a.txt", []byte("12345")); !errors.Is(err, archive.ErrTooLarge) {
		t.Fatalf("Add(over entry) = %v, want ErrTooLarge", err)
	}

	if err := w.Add("a.txt", []byte("1234")); err != nil {
		t.Fatal(err)
	}

	if err := w.Add("b.txt", []byte("123")); !errors.Is(err, archive.ErrTooLarge) {
		t.Fatalf("Add(over total) = %v, want ErrTooLarge", err)
	}
}
