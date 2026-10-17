package imaging

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"image"
	"image/color"
	"image/gif"
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

// webpVP8X is the header of an extended WebP declaring a w×h canvas.
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

// gifHeader is a GIF89a logical screen declaring w×h, without pixels.
func gifHeader(w, h int) []byte {
	b := []byte("GIF89a\x00\x00\x00\x00\x00\x00\x00;")
	binary.LittleEndian.PutUint16(b[6:], uint16(w))
	binary.LittleEndian.PutUint16(b[8:], uint16(h))

	return b
}

func TestSecurity_A05_ThumbnailRefusesBombHeaders(t *testing.T) {
	t.Parallel()

	disk := storage.NewMemory("")
	_ = storage.PutBytes(t.Context(), disk, "bomb.webp", webpVP8X(1<<14, 1<<14))
	_ = storage.PutBytes(t.Context(), disk, "bomb.gif", gifHeader(60000, 60000))

	for _, src := range []string{"bomb.webp", "bomb.gif"} {
		if _, err := Thumbnail(
			t.Context(),
			disk,
			src,
			"o",
			Options{MaxWidth: 10, MaxHeight: 10},
		); !errors.Is(
			err,
			ErrTooLarge,
		) {
			t.Errorf("Thumbnail(%s) = %v, want ErrTooLarge before decoding", src, err)
		}
	}
}

func TestThumbnailSniffsContentNotName(t *testing.T) {
	t.Parallel()

	var jpg bytes.Buffer
	if err := jpeg.Encode(&jpg, opaque(20, 20), nil); err != nil {
		t.Fatal(err)
	}

	disk := storage.NewMemory("")
	_ = storage.PutBytes(t.Context(), disk, "looks-like.png", jpg.Bytes())
	_ = storage.PutBytes(t.Context(), disk, "script.jpg", []byte("<script>alert(1)</script>"))

	if res, err := Thumbnail(
		t.Context(),
		disk,
		"looks-like.png",
		"a",
		Options{MaxWidth: 10, MaxHeight: 10},
	); err != nil ||
		res.Type != "image/jpeg" {
		t.Errorf("JPEG named .png = %+v, %v", res, err)
	}

	if _, err := Thumbnail(
		t.Context(),
		disk,
		"script.jpg",
		"b",
		Options{MaxWidth: 10, MaxHeight: 10},
	); !errors.Is(
		err,
		ErrUnsupported,
	) {
		t.Errorf("text named .jpg = %v, want ErrUnsupported", err)
	}
}

func TestThumbnailsValidatesNamesAndAppliesDefaultLimits(t *testing.T) {
	t.Parallel()

	disk := storage.NewMemory("")
	_ = storage.PutBytes(t.Context(), disk, "p.png", encodePNG(t, opaque(20, 20)))

	for _, name := range []string{"../x", "a/b", "", "a b"} {
		if _, err := Thumbnails(
			t.Context(),
			disk,
			"p.png",
			"o",
			map[string]Options{name: {MaxWidth: 5, MaxHeight: 5}},
		); !errors.Is(
			err,
			ErrInvalidOptions,
		) {
			t.Errorf("size name %q = %v, want ErrInvalidOptions", name, err)
		}
	}

	// A size without MaxPixels keeps the default; another size's larger
	// limit must not lift it, and the strictest explicit one wins.
	_, err := Thumbnails(t.Context(), disk, "p.png", "o", map[string]Options{
		"a": {MaxWidth: 5, MaxHeight: 5},
		"b": {MaxWidth: 5, MaxHeight: 5, MaxPixels: 100},
	})
	if !errors.Is(err, ErrTooLarge) {
		t.Errorf("Thumbnails with a 100 px size = %v, want ErrTooLarge", err)
	}
}

func TestThumbnailAnimatedGIFUsesFirstFrame(t *testing.T) {
	t.Parallel()

	pal := color.Palette{color.Black, color.White}
	anim := &gif.GIF{
		Image: []*image.Paletted{
			image.NewPaletted(image.Rect(0, 0, 40, 20), pal),
			image.NewPaletted(image.Rect(0, 0, 40, 20), pal),
		},
		Delay: []int{10, 10},
	}

	var buf bytes.Buffer
	if err := gif.EncodeAll(&buf, anim); err != nil {
		t.Fatal(err)
	}

	disk := storage.NewMemory("")
	_ = storage.PutBytes(t.Context(), disk, "a.gif", buf.Bytes())

	res, err := Thumbnail(t.Context(), disk, "a.gif", "g", Options{MaxWidth: 20, MaxHeight: 20})
	if err != nil || res.Width != 20 || res.Height != 10 {
		t.Fatalf("animated GIF = %+v, %v", res, err)
	}
}
