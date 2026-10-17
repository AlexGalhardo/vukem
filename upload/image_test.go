package upload

import (
	"bytes"
	"encoding/binary"
	"errors"
	"image"
	"image/png"
	"strings"
	"testing"

	"github.com/AlexGalhardo/vukem/errs"
	"github.com/AlexGalhardo/vukem/storage"
)

func realPNG(t *testing.T, w, h int) []byte {
	t.Helper()

	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewGray(image.Rect(0, 0, w, h))); err != nil {
		t.Fatal(err)
	}

	return buf.Bytes()
}

// webpVP8X is the header of an extended WebP whose canvas is w x h.
func webpVP8X(w, h int) []byte {
	b := make([]byte, 30)
	copy(b, "RIFF")
	binary.LittleEndian.PutUint32(b[4:], 22)
	copy(b[8:], "WEBPVP8X")
	binary.LittleEndian.PutUint32(b[16:], 10)

	for i, v := range []int{w - 1, h - 1} {
		b[24+i*3], b[25+i*3], b[26+i*3] = byte(v), byte(v>>8), byte(v>>16)
	}

	return b
}

func TestSecurity_A05_UploadImagePixelLimit(t *testing.T) {
	t.Parallel()

	if Images(1<<20).MaxPixels != DefaultMaxPixels {
		t.Fatalf("Images().MaxPixels = %d, want DefaultMaxPixels", Images(1<<20).MaxPixels)
	}

	rules := Images(1 << 20)
	rules.MaxPixels = 100

	tests := []struct {
		name    string
		file    string
		content []byte
		wantErr string
	}{
		{"within limit", "ok.png", realPNG(t, 10, 10), ""},
		{"too many pixels", "big.png", realPNG(t, 11, 10), "The image dimensions are too large."},
		{"undecodable", "fake.png", []byte(pngMagic + strings.Repeat("x", 100)), "The image could not be read."},
		{"webp bomb header", "bomb.webp", webpVP8X(1<<14, 1<<14), "The image dimensions are too large."},
		{"small webp", "ok.webp", webpVP8X(10, 10), ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			disk := storage.NewMemory("")

			f, err := Save(t.Context(), disk, rules, "photo", tt.file, bytes.NewReader(tt.content))
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("Save() = %v, want nil", err)
				}

				if ok, _ := disk.Exists(t.Context(), f.Path); !ok {
					t.Error("accepted file not stored")
				}

				return
			}

			var verr *errs.ValidationError
			if !errors.As(err, &verr) || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Save() = %v, want validation error %q", err, tt.wantErr)
			}

			if files, _ := disk.List(t.Context(), ""); len(files) != 0 {
				t.Errorf("rejected image left on disk: %v", files)
			}
		})
	}
}
