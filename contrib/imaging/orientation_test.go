package imaging

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/jpeg"
	"testing"

	"github.com/AlexGalhardo/vukem/storage"
)

// exifJPEG encodes a 32x16 JPEG whose top-left 8x8 block is red and the
// rest white, with an EXIF APP1 segment carrying the orientation.
func exifJPEG(tb testing.TB, orientation uint16) []byte {
	tb.Helper()

	img := image.NewRGBA(image.Rect(0, 0, 32, 16))
	for y := range 16 {
		for x := range 32 {
			c := color.RGBA{R: 255, G: 255, B: 255, A: 255}
			if x < 8 && y < 8 {
				c = color.RGBA{R: 255, A: 255}
			}

			img.Set(x, y, c)
		}
	}

	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 100}); err != nil {
		tb.Fatal(err)
	}

	tiff := []byte("MM\x00\x2a\x00\x00\x00\x08\x00\x01")
	tiff = binary.BigEndian.AppendUint16(tiff, 0x0112)
	tiff = binary.BigEndian.AppendUint16(tiff, 3)
	tiff = binary.BigEndian.AppendUint32(tiff, 1)
	tiff = binary.BigEndian.AppendUint16(tiff, orientation)
	tiff = append(tiff, 0, 0, 0, 0, 0, 0)

	body := append([]byte("Exif\x00\x00"), tiff...)
	seg := []byte{0xff, 0xe1}
	seg = binary.BigEndian.AppendUint16(seg, uint16(len(body)+2))
	seg = append(seg, body...)

	src := buf.Bytes()
	out := append([]byte{}, src[:2]...)
	out = append(out, seg...)

	return append(out, src[2:]...)
}

func TestThumbnailAppliesEXIFOrientation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		orientation  uint16
		wantW, wantH int
		redX, redY   int // a point inside the red block of the result
	}{
		{"1 upright", 1, 32, 16, 4, 4},
		{"3 rotated 180", 3, 32, 16, 27, 11},
		{"6 rotate 90 cw", 6, 16, 32, 11, 4},
		{"8 rotate 90 ccw", 8, 16, 32, 4, 27},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			disk := storage.NewMemory("")
			if err := storage.PutBytes(t.Context(), disk, "in", exifJPEG(t, tt.orientation)); err != nil {
				t.Fatal(err)
			}

			res, err := Thumbnail(t.Context(), disk, "in", "out", Options{MaxWidth: 100, MaxHeight: 100})
			if err != nil {
				t.Fatal(err)
			}

			if res.Width != tt.wantW || res.Height != tt.wantH {
				t.Fatalf("size = %dx%d, want %dx%d", res.Width, res.Height, tt.wantW, tt.wantH)
			}

			rc, err := disk.Open(t.Context(), res.Path)
			if err != nil {
				t.Fatal(err)
			}
			defer rc.Close()

			img, err := jpeg.Decode(rc)
			if err != nil {
				t.Fatal(err)
			}

			if r, g, _, _ := img.At(tt.redX, tt.redY).RGBA(); r>>8 < 200 || g>>8 > 80 {
				t.Fatalf("pixel (%d,%d) is not red: r=%d g=%d", tt.redX, tt.redY, r>>8, g>>8)
			}
		})
	}
}

func TestExifOrientationMalformed(t *testing.T) {
	t.Parallel()

	good := exifJPEG(t, 6)
	for n := range 60 {
		if got := exifOrientation(good[:n]); got < 1 || got > 8 {
			t.Fatalf("truncated at %d: %d", n, got)
		}
	}

	if got := exifOrientation(exifJPEG(t, 9)); got != 1 {
		t.Fatalf("out-of-range tag = %d, want 1", got)
	}
}

func FuzzOrientation(f *testing.F) {
	for _, o := range []uint16{1, 6, 8, 9} {
		f.Add(exifJPEG(f, o)[:80])
	}

	f.Add([]byte("\xff\xd8\xff\xe1\x00\x08Exif\x00\x00"))

	f.Fuzz(func(t *testing.T, raw []byte) {
		if got := exifOrientation(raw); got < 1 || got > 8 {
			t.Fatalf("exifOrientation = %d", got)
		}
	})
}
