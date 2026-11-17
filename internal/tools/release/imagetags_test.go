package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestFloatingImageTags(t *testing.T) {
	t.Parallel()

	published := []string{
		"v0.45.0",
		"v0.44.0",
		"ffmpeg-9.0.2-2",
		"contrib/engine/v0.2.0",
		"v0.47.0-rc.1",
	}

	tests := []struct {
		version string
		tags    []string
		want    bool
	}{
		{version: "v0.46.0", tags: published, want: true},
		{version: "v0.45.0", tags: append(published, "v0.46.0"), want: false},
		{version: "v0.44.0", tags: append(published, "v0.46.0"), want: false},
		{version: "v0.47.0-rc.1", tags: append(published, "v0.46.0"), want: false},
		{version: "v0.10.0", tags: []string{"v0.9.0", "ffmpeg-9.0.2-2"}, want: true},
		{version: "v0.9.1", tags: []string{"v0.10.0", "nightly"}, want: false},
		{version: "0.46.0", tags: []string{"v0.45.0"}, want: true},
	}

	for _, tt := range tests {
		t.Run(tt.version, func(t *testing.T) {
			t.Parallel()

			if got := floatingImageTags(tt.version, tt.tags); got != tt.want {
				t.Fatalf("floatingImageTags(%q) = %v, want %v", tt.version, got, tt.want)
			}
		})
	}
}

func TestImageTagLines(t *testing.T) {
	t.Parallel()

	published := []string{"v0.45.0", "v0.44.0", "ffmpeg-9.0.2-2"}

	got, err := imageTagLines("ghcr.io/alexgalhardo/vukem", "v0.46.0", published)
	if err != nil {
		t.Fatal(err)
	}

	want := []string{
		"ghcr.io/alexgalhardo/vukem:0.46.0",
		"ghcr.io/alexgalhardo/vukem:0.46",
		"ghcr.io/alexgalhardo/vukem:latest",
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Fatalf("imageTagLines mismatch (-want +got):\n%s", diff)
	}

	got, err = imageTagLines("ghcr.io/alexgalhardo/vukem", "v0.44.0", published)
	if err != nil {
		t.Fatal(err)
	}

	want = []string{"ghcr.io/alexgalhardo/vukem:0.44.0"}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Fatalf("older imageTagLines mismatch (-want +got):\n%s", diff)
	}
}

func TestPrintImageTagsReadsTheTagsFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "tags.txt")
	body := "v0.45.0\r\nffmpeg-9.0.2-2\r\nv0.44.0\r\n"

	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer

	err := printImageTags(&buf, "v0.45.0", []string{"example.com/app"}, path)
	if err != nil {
		t.Fatal(err)
	}

	want := "example.com/app:0.45.0\nexample.com/app:0.45\nexample.com/app:latest\n"
	if diff := cmp.Diff(want, buf.String()); diff != "" {
		t.Fatalf("printImageTags mismatch (-want +got):\n%s", diff)
	}
}
