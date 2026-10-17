package imaging

import (
	"bytes"
	"encoding/base64"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"strings"
	"testing"

	"github.com/AlexGalhardo/vukem/storage"
)

func encodePNG(t *testing.T, img image.Image) []byte {
	t.Helper()

	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}

	return buf.Bytes()
}

func opaque(w, h int) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.Set(x, y, color.RGBA{R: uint8(x), G: uint8(y), B: 100, A: 255})
		}
	}

	return img
}

// tinyWebP is a 1x1 lossless WebP with a transparent pixel.
const tinyWebP = "UklGRhoAAABXRUJQVlA4TA0AAAAvAAAAEAcQERGIiP4HAA=="

func TestThumbnail(t *testing.T) {
	t.Parallel()

	transparent := image.NewNRGBA(image.Rect(0, 0, 40, 40)) // all zero alpha

	webp, err := base64.StdEncoding.DecodeString(tinyWebP)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name          string
		src           []byte
		maxW, maxH    int
		wantW, wantH  int
		wantType, ext string
	}{
		{"landscape fits width", encodePNG(t, opaque(400, 200)), 100, 100, 100, 50, "image/jpeg", ".jpg"},
		{"portrait fits height", encodePNG(t, opaque(200, 400)), 100, 100, 50, 100, "image/jpeg", ".jpg"},
		{"never upscales", encodePNG(t, opaque(30, 20)), 100, 100, 30, 20, "image/jpeg", ".jpg"},
		{"transparency stays png", encodePNG(t, transparent), 20, 20, 20, 20, "image/png", ".png"},
		{"webp source (with alpha)", webp, 10, 10, 1, 1, "image/png", ".png"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			disk := storage.NewMemory("")
			if err := storage.PutBytes(t.Context(), disk, "in/src", tt.src); err != nil {
				t.Fatal(err)
			}

			res, err := Thumbnail(
				t.Context(),
				disk,
				"in/src",
				"thumbs/a",
				Options{MaxWidth: tt.maxW, MaxHeight: tt.maxH},
			)
			if err != nil {
				t.Fatalf("Thumbnail() = %v", err)
			}

			if res.Width != tt.wantW || res.Height != tt.wantH || res.Type != tt.wantType ||
				res.Path != "thumbs/a"+tt.ext {
				t.Fatalf("Thumbnail() = %+v, want %dx%d %s thumbs/a%s", res, tt.wantW, tt.wantH, tt.wantType, tt.ext)
			}

			rc, err := disk.Open(t.Context(), res.Path)
			if err != nil {
				t.Fatal(err)
			}
			defer rc.Close()

			cfg, format, err := image.DecodeConfig(rc)
			if err != nil || cfg.Width != tt.wantW || cfg.Height != tt.wantH || "image/"+format != tt.wantType {
				t.Fatalf("stored = %+v %s %v", cfg, format, err)
			}
		})
	}
}

func TestSecurity_A05_ThumbnailRefusesBombsAndJunk(t *testing.T) {
	t.Parallel()

	disk := storage.NewMemory("")
	_ = storage.PutBytes(t.Context(), disk, "big", encodePNG(t, opaque(20, 20)))
	_ = storage.PutBytes(t.Context(), disk, "junk", []byte("\x89PNG\r\n\x1a\n"+strings.Repeat("x", 64)))
	_ = storage.PutBytes(t.Context(), disk, "huge", make([]byte, 2048))

	if _, err := Thumbnail(
		t.Context(),
		disk,
		"big",
		"o1",
		Options{MaxWidth: 10, MaxHeight: 10, MaxPixels: 100},
	); !errors.Is(
		err,
		ErrTooLarge,
	) {
		t.Errorf("400 px with MaxPixels 100 = %v, want ErrTooLarge", err)
	}

	if _, err := Thumbnail(
		t.Context(),
		disk,
		"junk",
		"o2",
		Options{MaxWidth: 10, MaxHeight: 10},
	); !errors.Is(
		err,
		ErrUnsupported,
	) {
		t.Errorf("corrupt PNG = %v, want ErrUnsupported", err)
	}

	if _, err := Thumbnail(
		t.Context(),
		disk,
		"huge",
		"o3",
		Options{MaxWidth: 10, MaxHeight: 10, MaxBytes: 1024},
	); !errors.Is(
		err,
		ErrTooLarge,
	) {
		t.Errorf("2 KiB with MaxBytes 1 KiB = %v, want ErrTooLarge", err)
	}

	if files, _ := disk.List(t.Context(), ""); len(files) != 3 {
		t.Errorf("files after failures = %v, want only the 3 sources", files)
	}
}

func TestThumbnailsWritesEverySize(t *testing.T) {
	t.Parallel()

	disk := storage.NewMemory("")
	_ = storage.PutBytes(t.Context(), disk, "p.png", encodePNG(t, opaque(1000, 500)))

	got, err := Thumbnails(t.Context(), disk, "p.png", "products/1", map[string]Options{
		"sm": {MaxWidth: 320, MaxHeight: 320},
		"lg": {MaxWidth: 800, MaxHeight: 800, Quality: 90},
	})
	if err != nil {
		t.Fatal(err)
	}

	if got["sm"].Path != "products/1-sm.jpg" || got["sm"].Width != 320 || got["lg"].Width != 800 ||
		got["lg"].Path != "products/1-lg.jpg" {
		t.Fatalf("Thumbnails() = %+v", got)
	}

	rc, _ := disk.Open(t.Context(), got["lg"].Path)
	defer rc.Close()

	if _, err := jpeg.Decode(rc); err != nil {
		t.Fatalf("lg is not a JPEG: %v", err)
	}
}

func TestOptionsValidation(t *testing.T) {
	t.Parallel()

	disk := storage.NewMemory("")
	_ = storage.PutBytes(t.Context(), disk, "p", encodePNG(t, opaque(10, 10)))

	for _, o := range []Options{{}, {MaxWidth: -1, MaxHeight: 10}, {MaxWidth: 10, MaxHeight: 10, Quality: 101}} {
		if _, err := Thumbnail(t.Context(), disk, "p", "o", o); err == nil {
			t.Errorf("Thumbnail(%+v) = nil error, want invalid options", o)
		}
	}
}
