package upload

import (
	"context"
	"encoding/binary"
	"errors"
	"image"
	"image/gif"
	"image/jpeg"
	"image/png"
	"io"

	"github.com/AlexGalhardo/vukem/errs"
	"github.com/AlexGalhardo/vukem/storage"
)

// DefaultMaxPixels is the width × height Images accepts (25 megapixels,
// about 100 MB once decoded to RGBA).
const DefaultMaxPixels = 25_000_000

var errImageHeader = errors.New("upload: unreadable image header")

// checkImage rejects JPEG, PNG, GIF and WebP files whose header declares
// more than maxPixels, or that have no readable header: a few kilobytes
// can declare a canvas that takes gigabytes to decode (decompression bomb).
// Only the header is read, never the pixels.
func checkImage(ctx context.Context, d storage.Disk, p, typ, field string, maxPixels int64) error {
	decode := map[string]func(io.Reader) (image.Config, error){
		"image/jpeg": jpeg.DecodeConfig,
		"image/png":  png.DecodeConfig,
		"image/gif":  gif.DecodeConfig,
		"image/webp": webpConfig,
	}[typ]
	if decode == nil || maxPixels <= 0 {
		return nil
	}

	rc, err := d.Open(ctx, p)
	if err != nil {
		return err
	}
	defer rc.Close()

	cfg, err := decode(io.LimitReader(rc, 1<<20))
	if err != nil || cfg.Width <= 0 || cfg.Height <= 0 {
		return errs.NewValidation().Add(field, "The image could not be read.")
	}

	if int64(cfg.Width)*int64(cfg.Height) > maxPixels {
		return errs.NewValidation().Add(field, "The image dimensions are too large.")
	}

	return nil
}

// webpConfig reads the canvas size of a WebP file (VP8, VP8L or VP8X);
// the standard library has no WebP decoder.
func webpConfig(r io.Reader) (image.Config, error) {
	var b [30]byte
	if _, err := io.ReadFull(r, b[:]); err != nil {
		return image.Config{}, errImageHeader
	}

	if string(b[0:4]) != "RIFF" || string(b[8:12]) != "WEBP" {
		return image.Config{}, errImageHeader
	}

	var w, h int

	switch string(b[12:16]) {
	case "VP8 ": // lossy: start code 9d 01 2a, then 14-bit sizes
		if b[23] != 0x9d || b[24] != 0x01 || b[25] != 0x2a {
			return image.Config{}, errImageHeader
		}

		w = int(binary.LittleEndian.Uint16(b[26:]) & 0x3fff)
		h = int(binary.LittleEndian.Uint16(b[28:]) & 0x3fff)
	case "VP8L": // lossless: signature 0x2f, then 14-bit sizes minus one
		if b[20] != 0x2f {
			return image.Config{}, errImageHeader
		}

		bits := binary.LittleEndian.Uint32(b[21:])
		w = int(bits&0x3fff) + 1
		h = int(bits>>14&0x3fff) + 1
	case "VP8X": // extended: 24-bit canvas sizes minus one
		w = (int(b[24]) | int(b[25])<<8 | int(b[26])<<16) + 1
		h = (int(b[27]) | int(b[28])<<8 | int(b[29])<<16) + 1
	default:
		return image.Config{}, errImageHeader
	}

	return image.Config{Width: w, Height: h}, nil
}
