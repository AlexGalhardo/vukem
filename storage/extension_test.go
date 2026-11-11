package storage_test

import (
	"testing"

	"github.com/AlexGalhardo/vukem/mediatype"
)

// PutUploaded names stored files with mediatype.Extension, so the detected
// types it accepts must keep their extensions.
func TestStoredExtensionMatchesMediaType(t *testing.T) {
	t.Parallel()

	for ct, want := range map[string]string{
		"image/jpeg":                ".jpg",
		"image/png":                 ".png",
		"image/gif":                 ".gif",
		"image/webp":                ".webp",
		"application/pdf":           ".pdf",
		"text/plain; charset=utf-8": ".txt",
	} {
		if got := mediatype.Extension(ct); got != want {
			t.Errorf("mediatype.Extension(%q) = %q, want %q", ct, got, want)
		}
	}
}
