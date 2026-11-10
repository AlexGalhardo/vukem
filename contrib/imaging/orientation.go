package imaging

import (
	"bytes"
	"encoding/binary"
	"image"
)

// exifOrientation reads the EXIF Orientation tag of a JPEG. It returns 1
// (upright) when the tag is absent, malformed or outside 1..8; every read is
// bounds-checked because the input is untrusted.
func exifOrientation(raw []byte) int {
	pos := 2 // after SOI

	for pos+4 <= len(raw) && raw[pos] == 0xff {
		marker := raw[pos+1]
		if marker == 0xda { // SOS: no metadata after the scan starts
			return 1
		}

		size := int(binary.BigEndian.Uint16(raw[pos+2:]))
		if size < 2 || pos+2+size > len(raw) {
			return 1
		}

		if marker == 0xe1 {
			if body := raw[pos+4 : pos+2+size]; bytes.HasPrefix(body, []byte("Exif\x00\x00")) {
				return tiffOrientation(body[6:])
			}
		}

		pos += 2 + size
	}

	return 1
}

func tiffOrientation(tiff []byte) int {
	if len(tiff) < 8 {
		return 1
	}

	var order binary.ByteOrder

	switch string(tiff[:2]) {
	case "II":
		order = binary.LittleEndian
	case "MM":
		order = binary.BigEndian
	default:
		return 1
	}

	ifd := int64(order.Uint32(tiff[4:]))
	if ifd+2 > int64(len(tiff)) {
		return 1
	}

	count := int64(order.Uint16(tiff[ifd:]))

	for i := range count {
		off := ifd + 2 + i*12
		if off+12 > int64(len(tiff)) {
			return 1
		}

		entry := tiff[off : off+12]
		if order.Uint16(entry) != 0x0112 {
			continue
		}

		// type SHORT, one value stored inline
		if order.Uint16(entry[2:]) != 3 || order.Uint32(entry[4:]) != 1 {
			return 1
		}

		if v := int(order.Uint16(entry[8:])); v >= 1 && v <= 8 {
			return v
		}

		return 1
	}

	return 1
}

// orient returns img transformed so that it displays upright for the given
// EXIF orientation (1..8).
func orient(img image.Image, o int) image.Image {
	if o < 2 || o > 8 {
		return img
	}

	b := img.Bounds()
	w, h := b.Dx(), b.Dy()

	dw, dh := w, h
	if o >= 5 {
		dw, dh = h, w
	}

	out := image.NewNRGBA(image.Rect(0, 0, dw, dh))

	for y := range dh {
		for x := range dw {
			var sx, sy int

			switch o {
			case 2:
				sx, sy = w-1-x, y
			case 3:
				sx, sy = w-1-x, h-1-y
			case 4:
				sx, sy = x, h-1-y
			case 5:
				sx, sy = y, x
			case 6:
				sx, sy = y, h-1-x
			case 7:
				sx, sy = w-1-y, h-1-x
			default: // 8
				sx, sy = w-1-y, x
			}

			out.Set(x, y, img.At(b.Min.X+sx, b.Min.Y+sy))
		}
	}

	return out
}

func jpegMagic(raw []byte) bool {
	return len(raw) >= 3 && raw[0] == 0xff && raw[1] == 0xd8 && raw[2] == 0xff
}
