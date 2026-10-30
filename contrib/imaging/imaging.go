// Package imaging makes thumbnails of uploaded images: it decodes JPEG,
// PNG, GIF and WebP from a storage.Disk, checks the declared dimensions
// before decoding (decompression bombs), scales the image down to fit a
// box without upscaling, and stores it as JPEG, or PNG when the source has
// transparency. Re-encoding drops all metadata (EXIF, GPS). Animated GIFs
// become a still of their first frame. WebP is decoded only: there is no
// encoder in the standard library or golang.org/x/image. Output is
// deterministic for one Go toolchain (same bytes and Options give the same
// file), not across Go releases. Peak memory per call is roughly the
// source file plus its decoded pixels (up to ~8 bytes per pixel for 16-bit
// PNG): bound concurrency, for example by making thumbnails in a queue job.
//
//	res, err := imaging.Thumbnail(ctx, disk, file.Path, "thumbs/"+id, imaging.Options{MaxWidth: 400, MaxHeight: 400})
//
// It is a separate module so the core does not depend on golang.org/x/image.
package imaging

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"regexp"
	"slices"

	"golang.org/x/image/draw"
	"golang.org/x/image/webp"

	"github.com/AlexGalhardo/vukem/storage"
	"github.com/AlexGalhardo/vukem/upload"
)

// Defaults applied when an Options field is zero, and the largest output
// side accepted.
const (
	DefaultQuality  = 82
	DefaultMaxBytes = 25 << 20
	MaxSide         = 1 << 14
)

var (
	// ErrTooLarge reports a source above Options.MaxBytes or MaxPixels.
	ErrTooLarge = errors.New("imaging: image too large")
	// ErrUnsupported reports a source that is not a readable JPEG, PNG,
	// GIF or WebP image.
	ErrUnsupported = errors.New("imaging: unsupported or corrupt image")
	// ErrInvalidOptions reports a missing or out-of-range Options field.
	ErrInvalidOptions = errors.New("imaging: invalid options")
)

// Options describe one thumbnail.
type Options struct {
	// MaxWidth and MaxHeight bound the output (required, > 0). The image
	// keeps its aspect ratio and is never upscaled.
	MaxWidth, MaxHeight int
	// Quality is the JPEG quality, 1-100 (default 82).
	Quality int
	// MaxPixels bounds the source's declared width × height (default
	// upload.DefaultMaxPixels).
	MaxPixels int64
	// MaxBytes bounds the source file size (default 25 MiB).
	MaxBytes int64
}

// Result is a stored thumbnail.
type Result struct {
	Path          string `json:"path"`
	Type          string `json:"type"`
	Width, Height int
}

// Thumbnail reads src from d, scales it to fit opts and stores it at dst
// plus ".jpg" or ".png", replacing an existing file there. On failure the
// output is removed (best effort).
func Thumbnail(ctx context.Context, d storage.Disk, src, dst string, opts Options) (Result, error) {
	img, err := load(ctx, d, src, opts)
	if err != nil {
		return Result{}, err
	}

	return store(ctx, d, img, dst, opts)
}

var sizeName = regexp.MustCompile(`^[A-Za-z0-9_-]{1,32}$`)

// Thumbnails stores one thumbnail per named size from a single decode,
// at dst + "-" + name + extension.
func Thumbnails(
	ctx context.Context,
	d storage.Disk,
	src, dst string,
	sizes map[string]Options,
) (map[string]Result, error) {
	if len(sizes) == 0 {
		return nil, fmt.Errorf("%w: no sizes", ErrInvalidOptions)
	}

	// The strictest limits (defaults resolved) apply to the shared decode.
	lim := Options{MaxWidth: 1, MaxHeight: 1}

	names := make([]string, 0, len(sizes))
	for name, o := range sizes {
		if !sizeName.MatchString(name) {
			return nil, fmt.Errorf("%w: size name %q (letters, digits, - and _)", ErrInvalidOptions, name)
		}

		if err := validate(o); err != nil {
			return nil, err
		}

		lim.MaxPixels = minPositive(lim.MaxPixels, cmpOr(o.MaxPixels, upload.DefaultMaxPixels))
		lim.MaxBytes = minPositive(lim.MaxBytes, cmpOr(o.MaxBytes, DefaultMaxBytes))
		names = append(names, name)
	}

	slices.Sort(names)

	img, err := load(ctx, d, src, lim)
	if err != nil {
		return nil, err
	}

	out := make(map[string]Result, len(sizes))

	for _, name := range names {
		o := sizes[name]

		r, err := store(ctx, d, img, dst+"-"+name, o)
		if err != nil {
			for _, done := range out {
				_ = d.Delete(context.WithoutCancel(ctx), done.Path)
			}

			return nil, fmt.Errorf("imaging: size %q: %w", name, err)
		}

		out[name] = r
	}

	return out, nil
}

func validate(o Options) error {
	if o.MaxWidth <= 0 || o.MaxHeight <= 0 || o.MaxWidth > MaxSide || o.MaxHeight > MaxSide ||
		o.Quality < 0 || o.Quality > 100 || o.MaxPixels < 0 || o.MaxBytes < 0 {
		return fmt.Errorf("%w: MaxWidth and MaxHeight must be 1-%d, Quality 0-100", ErrInvalidOptions, MaxSide)
	}

	return nil
}

func minPositive(a, b int64) int64 {
	switch {
	case a <= 0:
		return b
	case b <= 0:
		return a
	default:
		return min(a, b)
	}
}

func load(ctx context.Context, d storage.Disk, src string, o Options) (image.Image, error) {
	if err := validate(o); err != nil {
		return nil, err
	}

	maxBytes := cmpOr(o.MaxBytes, DefaultMaxBytes)
	maxPixels := cmpOr(o.MaxPixels, upload.DefaultMaxPixels)

	rc, err := d.Open(ctx, src)
	if err != nil {
		return nil, fmt.Errorf("imaging: open %s: %w", src, err)
	}
	defer rc.Close()

	raw, err := io.ReadAll(io.LimitReader(rc, maxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("imaging: read %s: %w", src, err)
	}

	if int64(len(raw)) > maxBytes {
		return nil, ErrTooLarge
	}

	decodeConfig, decode := codecs(raw)
	if decode == nil {
		return nil, ErrUnsupported
	}

	// The header first: a small file can declare a canvas that takes
	// gigabytes to decode.
	cfg, err := decodeConfig(bytes.NewReader(raw))
	if err != nil || cfg.Width <= 0 || cfg.Height <= 0 {
		return nil, ErrUnsupported
	}

	if int64(cfg.Width)*int64(cfg.Height) > maxPixels {
		return nil, ErrTooLarge
	}

	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// Animated GIFs decode their first frame only.
	img, err := decode(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrUnsupported, err)
	}

	return img, nil
}

func cmpOr(v, def int64) int64 {
	if v > 0 {
		return v
	}

	return def
}

// codecs picks the decoder from the content, never the file name.
func codecs(raw []byte) (func(io.Reader) (image.Config, error), func(io.Reader) (image.Image, error)) {
	switch {
	case bytes.HasPrefix(raw, []byte("\xff\xd8\xff")):
		return jpeg.DecodeConfig, jpeg.Decode
	case bytes.HasPrefix(raw, []byte("\x89PNG\r\n\x1a\n")):
		return png.DecodeConfig, png.Decode
	case bytes.HasPrefix(raw, []byte("GIF87a")), bytes.HasPrefix(raw, []byte("GIF89a")):
		return gif.DecodeConfig, gif.Decode
	case len(raw) >= 12 && string(raw[0:4]) == "RIFF" && string(raw[8:12]) == "WEBP":
		return webp.DecodeConfig, webp.Decode
	default:
		return nil, nil
	}
}

func store(ctx context.Context, d storage.Disk, img image.Image, dst string, o Options) (Result, error) {
	if err := validate(o); err != nil {
		return Result{}, err
	}

	if err := ctx.Err(); err != nil {
		return Result{}, err
	}

	b := img.Bounds()
	w, h := fit(b.Dx(), b.Dy(), o.MaxWidth, o.MaxHeight)

	transparent := !isOpaque(img)

	var out draw.Image
	if transparent {
		out = image.NewNRGBA(image.Rect(0, 0, w, h))
	} else {
		out = image.NewRGBA(image.Rect(0, 0, w, h))
	}

	draw.CatmullRom.Scale(out, out.Bounds(), img, b, draw.Src, nil)

	var (
		buf bytes.Buffer
		res = Result{Width: w, Height: h}
		err error
	)

	if transparent {
		res.Path, res.Type = dst+".png", "image/png"
		err = png.Encode(&buf, out)
	} else {
		res.Path, res.Type = dst+".jpg", "image/jpeg"
		err = jpeg.Encode(&buf, out, &jpeg.Options{Quality: int(cmpOr(int64(o.Quality), DefaultQuality))})
	}

	if err != nil {
		return Result{}, fmt.Errorf("imaging: encode: %w", err)
	}

	if err := storage.PutBytes(ctx, d, res.Path, buf.Bytes()); err != nil {
		_ = d.Delete(context.WithoutCancel(ctx), res.Path) // a partial write
		return Result{}, fmt.Errorf("imaging: store %s: %w", res.Path, err)
	}

	return res, nil
}

// fit scales w×h down to fit maxW×maxH, keeping the aspect ratio; it never
// upscales and never returns a zero side.
func fit(w, h, maxW, maxH int) (int, int) {
	if w <= maxW && h <= maxH {
		return w, h
	}

	if int64(w)*int64(maxH) > int64(h)*int64(maxW) { // width is the binding side
		return maxW, max(1, int(int64(h)*int64(maxW)/int64(w)))
	}

	return max(1, int(int64(w)*int64(maxH)/int64(h))), maxH
}

func isOpaque(img image.Image) bool {
	if o, ok := img.(interface{ Opaque() bool }); ok {
		return o.Opaque()
	}

	return false
}
