// Package qrcode encodes a short payload as a QR code.
//
// PNG and SVG are drawn from rsc.io/qr ([Code.Black]): the standard
// library has no QR encoder, and this module does not implement
// Reed-Solomon itself or pull in a second QR library. The same content
// and [Options] produce the same bytes. PNG bytes are stable for one Go
// toolchain (image/png); SVG bytes are stable across toolchains.
//
// The module does not import vukem. Mount [Handler] on any [http.ServeMux]
// or pass the bytes to your own response.
package qrcode

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"strconv"
	"strings"

	"rsc.io/qr"
)

const (
	// MaxContent is the largest payload, in bytes (not runes).
	MaxContent = 2048
	// DefaultScale is the module size in pixels when [Options.Scale] is 0.
	DefaultScale = 8
	// MaxScale is the largest accepted module size in pixels.
	MaxScale = 32
	// DefaultQuiet is the quiet zone in modules when [Options.Quiet] is nil.
	DefaultQuiet = 4
	// MaxQuiet is the largest accepted quiet zone in modules.
	MaxQuiet = 16
)

var (
	// ErrEmpty reports an empty payload.
	ErrEmpty = errors.New("qrcode: empty content")
	// ErrTooLarge reports a payload longer than [MaxContent] bytes.
	ErrTooLarge = errors.New("qrcode: content exceeds 2048 bytes")
	// ErrInvalidOptions reports a level, scale or quiet zone outside its range.
	ErrInvalidOptions = errors.New("qrcode: invalid options")
)

// Level is a QR error-correction level. Higher levels survive more damage
// and store less data. Values match rsc.io/qr (L, M, Q, H). The zero Level
// is Low.
type Level int

const (
	// Low recovers about 7% of damaged codewords (rsc.io/qr level L).
	Low Level = iota
	// Medium recovers about 15% of damaged codewords (rsc.io/qr level M).
	Medium
	// Quartile recovers about 25% of damaged codewords (rsc.io/qr level Q).
	Quartile
	// High recovers about 30% of damaged codewords (rsc.io/qr level H).
	High
)

// Options controls encoding and drawing.
type Options struct {
	// Level selects error correction. The zero value is Low.
	Level Level
	// Scale is the module size in pixels. Zero means [DefaultScale].
	// Any other value outside 1..[MaxScale] is rejected.
	Scale int
	// Quiet is the quiet zone in modules. Nil means [DefaultQuiet].
	// A non-nil value outside 0..[MaxQuiet] is rejected. Zero is a real
	// width, so the default cannot be the integer zero.
	Quiet *int
}

// PNG encodes content as a PNG image.
func PNG(content string, opt Options) ([]byte, error) {
	img, err := imageOf(content, opt)
	if err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	if err := (&png.Encoder{CompressionLevel: png.DefaultCompression}).Encode(&buf, img); err != nil {
		return nil, fmt.Errorf("qrcode: png: %w", err)
	}

	return buf.Bytes(), nil
}

// SVG encodes content as an SVG document: one white rectangle and one
// black path of filled modules. The path is built from [qr.Code.Black].
func SVG(content string, opt Options) ([]byte, error) {
	code, scale, quiet, err := layout(content, opt)
	if err != nil {
		return nil, err
	}

	side := (code.Size + 2*quiet) * scale

	var b strings.Builder
	b.Grow(128 + code.Size*code.Size*8)
	fmt.Fprintf(&b, "<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n")
	fmt.Fprintf(&b, "<svg xmlns=\"http://www.w3.org/2000/svg\" width=\"%d\" height=\"%d\" ", side, side)
	fmt.Fprintf(&b, "viewBox=\"0 0 %d %d\" shape-rendering=\"crispEdges\">\n", side, side)
	fmt.Fprintf(&b, "<rect width=\"%d\" height=\"%d\" fill=\"#ffffff\"/>\n", side, side)
	b.WriteString("<path fill=\"#000000\" d=\"")

	for y := range code.Size {
		for x := range code.Size {
			if !code.Black(x, y) {
				continue
			}

			b.WriteByte('M')
			b.WriteString(strconv.Itoa((x + quiet) * scale))
			b.WriteByte(' ')
			b.WriteString(strconv.Itoa((y + quiet) * scale))
			b.WriteByte('h')
			b.WriteString(strconv.Itoa(scale))
			b.WriteByte('v')
			b.WriteString(strconv.Itoa(scale))
			b.WriteByte('h')
			b.WriteByte('-')
			b.WriteString(strconv.Itoa(scale))
			b.WriteByte('z')
		}
	}

	b.WriteString("\"/>\n</svg>\n")

	return []byte(b.String()), nil
}

func imageOf(content string, opt Options) (*image.Paletted, error) {
	code, scale, quiet, err := layout(content, opt)
	if err != nil {
		return nil, err
	}

	side := (code.Size + 2*quiet) * scale
	img := image.NewPaletted(image.Rect(0, 0, side, side), color.Palette{color.White, color.Black})

	for y := range code.Size {
		for x := range code.Size {
			if !code.Black(x, y) {
				continue
			}

			x0 := (x + quiet) * scale
			y0 := (y + quiet) * scale

			for dy := range scale {
				row := img.Pix[(y0+dy)*img.Stride+x0 : (y0+dy)*img.Stride+x0+scale]
				for i := range row {
					row[i] = 1
				}
			}
		}
	}

	return img, nil
}

func layout(content string, opt Options) (*qr.Code, int, int, error) {
	if content == "" {
		return nil, 0, 0, ErrEmpty
	}

	if len(content) > MaxContent {
		return nil, 0, 0, ErrTooLarge
	}

	scale := opt.Scale
	if scale == 0 {
		scale = DefaultScale
	}

	if scale < 1 || scale > MaxScale {
		return nil, 0, 0, fmt.Errorf("%w: scale %d", ErrInvalidOptions, scale)
	}

	quiet := DefaultQuiet
	if opt.Quiet != nil {
		quiet = *opt.Quiet
	}

	if quiet < 0 || quiet > MaxQuiet {
		return nil, 0, 0, fmt.Errorf("%w: quiet zone %d", ErrInvalidOptions, quiet)
	}

	level, err := opt.Level.qr()
	if err != nil {
		return nil, 0, 0, err
	}

	code, err := qr.Encode(content, level)
	if err != nil {
		return nil, 0, 0, fmt.Errorf("qrcode: encode: %w", err)
	}

	return code, scale, quiet, nil
}

func (l Level) qr() (qr.Level, error) {
	switch l {
	case Low:
		return qr.L, nil
	case Medium:
		return qr.M, nil
	case Quartile:
		return qr.Q, nil
	case High:
		return qr.H, nil
	default:
		return 0, fmt.Errorf("%w: level %d", ErrInvalidOptions, int(l))
	}
}
