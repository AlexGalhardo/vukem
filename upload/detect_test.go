package upload_test

import (
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/AlexGalhardo/vukem/upload"
)

// A108: Rules.Types is matched against the type detected from content with
// http.DetectContentType, whose vocabulary is narrow. A rule naming a type
// the detector can never produce did not fail — it quietly rejected every
// file of that type, which is the worst of both outcomes: the application
// believes it accepts AVIF and accepts nothing.
func TestRulesRefuseAnUndetectableType(t *testing.T) {
	t.Parallel()

	// Measured on Go 1.27.2: the detector answers application/octet-stream
	// for every one of these.
	for _, typ := range []string{"image/avif", "image/heic", "image/heif", "image/jxl", "image/tiff"} {
		t.Run(typ, func(t *testing.T) {
			t.Parallel()

			rules := upload.Rules{MaxFileSize: 1 << 20, Types: []string{typ}}

			_, err := upload.Save(t.Context(), nil, rules, "file", "x.avif", strings.NewReader("x"))
			if !errors.Is(err, upload.ErrUndetectableType) {
				t.Fatalf("Save() with Types %q = %v, want ErrUndetectableType", typ, err)
			}

			// The message names what can be used instead of guessing.
			if !strings.Contains(err.Error(), "image/jpeg") {
				t.Errorf("error does not list the detectable types: %v", err)
			}
		})
	}
}

// Every type the package claims to detect really is detectable: the list
// is measured against the detector, not asserted. A sample per type, so
// the claim cannot drift from what http.DetectContentType does.
func TestDetectableTypesAreActuallyDetectable(t *testing.T) {
	t.Parallel()

	samples := map[string][]byte{
		"image/jpeg":      {0xff, 0xd8, 0xff, 0xe0, 0, 0, 0, 0},
		"image/png":       {0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a},
		"image/gif":       []byte("GIF89a\x00\x00"),
		"image/webp":      append([]byte("RIFF\x00\x00\x00\x00WEBPVP8 "), make([]byte, 12)...),
		"image/bmp":       {'B', 'M', 0, 0, 0, 0, 0, 0},
		"image/x-icon":    {0x00, 0x00, 0x01, 0x00, 1, 0, 0, 0},
		"application/pdf": []byte("%PDF-1.7"),
		"audio/wave":      append([]byte("RIFF\x00\x00\x00\x00WAVE"), make([]byte, 12)...),
		// The detector's "I cannot tell", and the documented escape hatch
		// for a service with its own sniffer.
		"application/octet-stream": {0x00, 0x01, 0x02, 0x03, 0xff, 0xfe, 0x7f, 0x80},
	}

	detectable := upload.DetectableTypes()

	for typ, head := range samples {
		if !slices.Contains(detectable, typ) {
			t.Errorf("DetectableTypes() omits %q, which the detector does resolve", typ)
			continue
		}

		if got, _, _ := strings.Cut(http.DetectContentType(head), ";"); got != typ {
			t.Errorf("http.DetectContentType(sample for %q) = %q", typ, got)
		}
	}

	// And the escape hatch is there, since without it a service handling
	// AVIF has no way to use this package at all.
	if !slices.Contains(detectable, "application/octet-stream") {
		t.Error("DetectableTypes() has no application/octet-stream escape hatch")
	}
}
