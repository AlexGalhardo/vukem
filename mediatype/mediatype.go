// Package mediatype names the types an application **produces**: the
// canonical file extension and a human label for a file it generates and
// hands back as a download.
//
// It is the other direction from [upload.Extensions], which answers "what
// extension may a file of this detected type have" as part of validating
// input. A service that converts, renders or exports needs "what do I call
// the thing I just made": the extension to put on the download, and a
// label to show in a list of output formats.
//
// # What this package is not
//
// It is **not** a set of types an application accepts as input, and it is
// built so it cannot be used as one. There is no exported list, no
// iterator and no Known predicate — only lookups that answer "" for a type
// they do not have. A framework-wide "known types" list would be the wrong
// shape for an input policy anyway: that policy is per application and
// often the only thing keeping a vulnerable decoder unreachable. An app may
// depend on "no tool here accepts XML input" or "EXR is not a recognised
// type", and a shared list that quietly grew a type would take that
// argument away without anyone editing the app.
//
// Validate input with [upload.Rules], or with a detector of your own, and
// keep the accepted set in the application.
package mediatype

import (
	"mime"
	"strings"
)

// kind is one produced type.
type kind struct {
	ext   string
	label string
}

// kinds maps a media type to the extension and label an application uses
// when it hands the file back. The extension is the canonical one, so a
// type with several spellings (.jpg/.jpeg, .tif/.tiff) resolves to one.
//
// Only types a web application generates are here. Adding one is a
// deliberate act: see the package comment for why this is not an input
// allowlist.
var kinds = map[string]kind{ //nolint:gochecknoglobals // read-only lookup table
	// Images
	"image/png":     {".png", "PNG image"},
	"image/jpeg":    {".jpg", "JPEG image"},
	"image/gif":     {".gif", "GIF image"},
	"image/webp":    {".webp", "WebP image"},
	"image/avif":    {".avif", "AVIF image"},
	"image/tiff":    {".tiff", "TIFF image"},
	"image/bmp":     {".bmp", "BMP image"},
	"image/x-icon":  {".ico", "Icon"},
	"image/svg+xml": {".svg", "SVG image"},
	// Documents
	"application/pdf":  {".pdf", "PDF document"},
	"text/plain":       {".txt", "Plain text"},
	"text/csv":         {".csv", "CSV spreadsheet"},
	"text/markdown":    {".md", "Markdown"},
	"text/html":        {".html", "HTML document"},
	"application/json": {".json", "JSON"},
	// Audio and video
	"audio/mpeg": {".mp3", "MP3 audio"},
	"audio/wav":  {".wav", "WAV audio"},
	"audio/ogg":  {".ogg", "Ogg audio"},
	"audio/flac": {".flac", "FLAC audio"},
	"video/mp4":  {".mp4", "MP4 video"},
	"video/webm": {".webm", "WebM video"},
	// Archives
	"application/zip":  {".zip", "ZIP archive"},
	"application/gzip": {".gz", "gzip archive"},
	// Fonts
	"font/woff2": {".woff2", "WOFF2 font"},
	"font/ttf":   {".ttf", "TrueType font"},
}

// byExt is the reverse lookup, built from kinds so the two cannot disagree.
var byExt = func() map[string]string { //nolint:gochecknoglobals // derived from kinds, read-only
	out := make(map[string]string, len(kinds))
	for t, k := range kinds {
		out[k.ext] = t
	}

	return out
}()

// Extension is the canonical file extension for a produced type,
// including the leading dot ("" for a type this package does not name).
// Parameters are ignored, so "text/csv; charset=utf-8" answers ".csv".
//
// Use it to name a download, not to decide whether a type is acceptable.
func Extension(t string) string { return kinds[normalize(t)].ext }

// Label is a short human name for a produced type, for a list of output
// formats or a download's description ("" for a type this package does not
// name). It is English and not translated: pass it through your own
// catalogue when the application is localised.
func Label(t string) string { return kinds[normalize(t)].label }

// ByExtension is the media type for a canonical extension, with or without
// the leading dot ("" when there is none). It answers only the canonical
// spelling this package uses, so ".jpeg" is not ".jpg": an application
// reading an extension from a file name should not be trusting it in the
// first place, and this is for naming what the application itself wrote.
func ByExtension(ext string) string {
	if ext == "" {
		return ""
	}

	if !strings.HasPrefix(ext, ".") {
		ext = "." + ext
	}

	return byExt[strings.ToLower(ext)]
}

// normalize drops the parameters and lower-cases a media type.
func normalize(t string) string {
	parsed, _, err := mime.ParseMediaType(t)
	if err != nil {
		// Not a parsable media type; try it as a bare one so a caller
		// passing "image/png" with odd spacing still resolves.
		return strings.ToLower(strings.TrimSpace(t))
	}

	return parsed
}
