// Package upload receives files without buffering them: streaming
// multipart parsing with per-route limits, content sniffing that must agree
// with the file extension, random stored names, an optional virus-scan
// step (ClamAV) before files become visible, resumable uploads (tus 1.0)
// and presigned direct-to-storage uploads.
//
//	form, err := upload.Receive(c, disk, upload.Images(5<<20))
//	if err != nil {
//		return err
//	}
//	avatar := form.Files[0] // Path, Name, Type, Size, SHA256
//
// [Rules] say what a route accepts (a zero Rules rejects everything);
// [Receive] parses a request, [Save] stores a file from any reader,
// [Tus] serves resumable uploads and [Presign] / [Direct] hand out
// direct-to-storage uploads. Files land on a storage.Disk.
//
// See https://github.com/AlexGalhardo/vukem/blob/main/docs/subsystems.md#uploads.
package upload

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"path"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/AlexGalhardo/vukem/errs"
	"github.com/AlexGalhardo/vukem/storage"
	"github.com/AlexGalhardo/vukem/web"
)

// Defaults applied by Rules when a limit is left at zero.
const (
	DefaultMaxFiles      = 1
	DefaultMaxFields     = 32
	DefaultMaxFieldSize  = 64 << 10
	DefaultQuarantineDir = "quarantine"
	// sniffLen is what http.DetectContentType looks at.
	sniffLen = 512
	// formOverhead is the request budget for fields and part headers.
	formOverhead = 1 << 20
	maxNameLen   = 200
)

// Errors returned (as client-safe *errs.HTTPError values) by Receive and Save.
var (
	// ErrFileTooLarge reports a file larger than Rules.MaxFileSize (413).
	ErrFileTooLarge = errs.New(http.StatusRequestEntityTooLarge, "The file is too large.")
	// ErrFieldTooLarge reports a form field larger than Rules.MaxFieldSize (413).
	ErrFieldTooLarge = errs.New(http.StatusRequestEntityTooLarge, "A form field is too large.")
	// ErrTooManyFields reports more than Rules.MaxFields fields (413).
	ErrTooManyFields = errs.New(http.StatusRequestEntityTooLarge, "Too many form fields.")
	// ErrNotMultipart reports a request that is not multipart/form-data (415).
	ErrNotMultipart = errs.New(http.StatusUnsupportedMediaType, "The request must be multipart/form-data.")
)

// Rules restrict what Receive, Save, Verify and Presign accept. MaxFileSize
// and Types are required: a zero Rules rejects every upload (fail closed).
type Rules struct {
	// MaxFileSize is the largest file in bytes (required).
	MaxFileSize int64
	// MaxSize caps the whole request body; 0 = MaxFiles*MaxFileSize + 1 MiB.
	// It replaces the router-wide body limit for this request.
	MaxSize int64
	// MaxFiles is the number of files accepted (default 1).
	MaxFiles int
	// MaxPixels bounds width × height of JPEG, PNG, GIF and WebP files,
	// read from their header after storing (decompression bombs); files of
	// those types without a readable header are rejected. Images sets
	// DefaultMaxPixels; 0 skips the check.
	MaxPixels int64
	// Types are the allowed MIME types, matched against the type detected
	// from the content (http.DetectContentType); the client's
	// Content-Type is ignored (required).
	Types []string
	// Extensions optionally narrow the accepted file name extensions
	// (".jpg"). The extension must always match the detected type.
	Extensions []string
	// Dir is the directory files are stored under.
	Dir string
	// MaxFields and MaxFieldSize bound non-file fields (defaults 32, 64 KiB).
	MaxFields    int
	MaxFieldSize int64
	// Scanner, when set, scans every file in QuarantineDir before moving
	// it to Dir; infected files are deleted and rejected.
	Scanner Scanner
	// QuarantineDir holds files until they are scanned (default
	// "quarantine"). Quarantined names start with a dot, so
	// storage.Handler never serves them.
	QuarantineDir string
}

// Images allows JPEG, PNG, GIF and WebP files up to maxSize bytes and
// DefaultMaxPixels.
func Images(maxSize int64) Rules {
	return Rules{
		MaxFileSize: maxSize,
		MaxPixels:   DefaultMaxPixels,
		Types:       []string{"image/jpeg", "image/png", "image/gif", "image/webp"},
	}
}

// File is a stored upload.
type File struct {
	// Field is the form field name.
	Field string `json:"field"`
	// Name is the client's file name, sanitized. It is metadata only and
	// never part of Path.
	Name string `json:"name"`
	// Path is where the file was stored on the disk (random name).
	Path string `json:"path"`
	// Type is the MIME type detected from the content.
	Type string `json:"type"`
	// Size in bytes.
	Size int64 `json:"size"`
	// SHA256 is the hex digest of the content.
	SHA256 string `json:"sha256"`
}

// Form is a received multipart form.
type Form struct {
	Values url.Values
	Files  []File
}

// extensions maps the types http.DetectContentType reports to the file
// extensions that may carry them; the first one is used for stored names.
var extensions = map[string][]string{ //nolint:gochecknoglobals // read-only lookup table
	"image/jpeg":                    {".jpg", ".jpeg"},
	"image/png":                     {".png"},
	"image/gif":                     {".gif"},
	"image/webp":                    {".webp"},
	"image/bmp":                     {".bmp"},
	"image/x-icon":                  {".ico"},
	"application/pdf":               {".pdf"},
	"application/zip":               {".zip", ".docx", ".xlsx", ".pptx", ".odt", ".ods", ".odp"},
	"application/x-gzip":            {".gz", ".tgz"},
	"text/plain":                    {".txt", ".csv", ".md", ".json", ".log"},
	"text/html":                     {".html", ".htm"},
	"text/xml":                      {".xml"},
	"audio/mpeg":                    {".mp3"},
	"audio/wave":                    {".wav"},
	"application/ogg":               {".ogg", ".oga"},
	"video/mp4":                     {".mp4", ".m4v", ".m4a"},
	"video/webm":                    {".webm"},
	"video/avi":                     {".avi"},
	"font/woff":                     {".woff"},
	"font/woff2":                    {".woff2"},
	"font/ttf":                      {".ttf"},
	"application/vnd.ms-fontobject": {".eot"},
}

// Extensions returns the file extensions accepted for a detected MIME type.
func Extensions(mimeType string) []string { return slices.Clone(extensions[mediaType(mimeType)]) }

// errRules reports unusable rules: a configuration error, never shown to clients.
var errRules = errors.New("upload: Rules need MaxFileSize > 0 and at least one type")

func (r Rules) normalized() (Rules, error) {
	if r.MaxFileSize <= 0 || len(r.Types) == 0 {
		return r, errRules
	}

	if r.MaxFiles <= 0 {
		r.MaxFiles = DefaultMaxFiles
	}

	if r.MaxFields <= 0 {
		r.MaxFields = DefaultMaxFields
	}

	if r.MaxFieldSize <= 0 {
		r.MaxFieldSize = DefaultMaxFieldSize
	}

	if r.MaxSize <= 0 {
		r.MaxSize = int64(r.MaxFiles)*r.MaxFileSize + formOverhead
	}

	if r.QuarantineDir == "" {
		r.QuarantineDir = DefaultQuarantineDir
	}

	for _, dir := range []string{r.Dir, r.QuarantineDir} {
		if dir == "" {
			continue
		}

		if _, err := storage.Clean(dir); err != nil {
			return r, fmt.Errorf("upload: directory %q: %w", dir, err)
		}
	}

	types := make([]string, len(r.Types))
	for i, t := range r.Types {
		types[i] = mediaType(t)
	}

	r.Types = types

	exts := make([]string, len(r.Extensions))
	for i, e := range r.Extensions {
		exts[i] = "." + strings.TrimPrefix(strings.ToLower(e), ".")
	}

	r.Extensions = exts

	return r, nil
}

// match checks the detected type and the client's extension against the
// rules and returns the type and the extension to store the file with.
func (r Rules) match(field, name string, head []byte) (string, string, error) {
	typ := mediaType(http.DetectContentType(head))
	ext := strings.ToLower(path.Ext(name))

	return r.matchType(field, typ, ext)
}

func (r Rules) matchType(field, typ, ext string) (string, string, error) {
	if !slices.Contains(r.Types, typ) {
		return "", "", errs.NewValidation().Add(field, "This file type is not allowed.")
	}

	known := extensions[typ]
	if len(known) > 0 && !slices.Contains(known, ext) {
		return "", "", errs.NewValidation().Add(field, "The file extension does not match its content.")
	}

	if len(r.Extensions) > 0 && !slices.Contains(r.Extensions, ext) {
		return "", "", errs.NewValidation().Add(field, "This file extension is not allowed.")
	}

	switch {
	case len(known) > 0:
		return typ, known[0], nil
	case len(r.Extensions) > 0:
		return typ, ext, nil
	default:
		return typ, ".bin", nil
	}
}

func (r Rules) storedPath(ext string) string {
	name := strings.ToLower(rand.Text()) + ext
	if r.Dir == "" {
		return name
	}

	return strings.TrimSuffix(r.Dir, "/") + "/" + name
}

func mediaType(t string) string {
	mt, _, err := mime.ParseMediaType(t)
	if err != nil {
		return strings.ToLower(strings.TrimSpace(t))
	}

	return mt
}

// Receive reads the multipart body of c part by part and streams each
// file to d: nothing is buffered beyond a small read buffer, whatever the
// file size. It enforces rules (sizes → 413, types, extensions and file
// count → 422) and removes every file it wrote when any part fails, so a
// rejected request leaves nothing behind. Files are stored under
// rules.Dir with random names; client file names are kept only as
// File.Name.
//
// Nothing may read the body before Receive: MethodOverride leaves
// multipart bodies unread, and Ctx.Input/FormFile must not be called.
func Receive(c *web.Ctx, d storage.Disk, rules Rules) (*Form, error) {
	r, err := rules.normalized()
	if err != nil {
		return nil, err
	}

	req := web.LimitBody(c.Writer(), c.Request(), r.MaxSize)
	c.SetRequest(req)

	if req.MultipartForm != nil {
		return nil, errors.New("upload: the multipart body was already parsed")
	}

	mr, err := req.MultipartReader()
	if err != nil {
		return nil, ErrNotMultipart
	}

	ctx := c.Context()
	form := &Form{Values: url.Values{}}
	done := false

	defer func() {
		if !done {
			for _, f := range form.Files {
				_ = d.Delete(context.WithoutCancel(ctx), f.Path)
			}
		}
	}()

	fields, parts := 0, 0

	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}

		if err != nil {
			return nil, clientError(err)
		}

		// Parts without a name are skipped but still parsed: count every
		// part, or tiny nameless parts would bypass MaxFields.
		parts++
		if parts > r.MaxFields+r.MaxFiles {
			return nil, ErrTooManyFields
		}

		name := part.FormName()

		switch {
		case name == "":
		case part.FileName() == "":
			fields++
			if fields > r.MaxFields {
				return nil, ErrTooManyFields
			}

			v, err := io.ReadAll(io.LimitReader(part, r.MaxFieldSize+1))
			if err != nil {
				return nil, clientError(err)
			}

			if int64(len(v)) > r.MaxFieldSize {
				return nil, ErrFieldTooLarge
			}

			form.Values.Add(name, string(v))
		case len(form.Files) >= r.MaxFiles:
			return nil, errs.NewValidation().
				Add(name, fmt.Sprintf("No more than %d files may be uploaded.", r.MaxFiles))
		default:
			f, err := save(ctx, d, r, name, part.FileName(), part)
			if err != nil {
				return nil, err
			}

			form.Files = append(form.Files, f)
		}

		_ = part.Close()
	}

	done = true

	return form, nil
}

// Save validates and stores one file read from src under rules (the same
// checks as Receive): use it for files from other sources, such as a
// finished tus upload. field names the input in validation errors.
func Save(ctx context.Context, d storage.Disk, rules Rules, field, filename string, src io.Reader) (File, error) {
	r, err := rules.normalized()
	if err != nil {
		return File{}, err
	}

	return save(ctx, d, r, field, filename, src)
}

func save(ctx context.Context, d storage.Disk, r Rules, field, filename string, src io.Reader) (File, error) {
	name := SanitizeName(filename)

	head := make([]byte, sniffLen)

	n, err := io.ReadFull(src, head)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return File{}, clientError(err)
	}

	head = head[:n]

	typ, ext, err := r.match(field, strings.ReplaceAll(filename, "\\", "/"), head)
	if err != nil {
		return File{}, err
	}

	final := r.storedPath(ext)

	target := final
	if r.Scanner != nil {
		target = strings.TrimSuffix(r.QuarantineDir, "/") + "/." + strings.ToLower(rand.Text())
	}

	sum := sha256.New()
	body := &capReader{r: io.MultiReader(bytes.NewReader(head), src), left: r.MaxFileSize}

	if err := d.Put(ctx, target, io.TeeReader(body, sum)); err != nil {
		_ = d.Delete(context.WithoutCancel(ctx), target)

		switch {
		case body.tooLarge:
			return File{}, ErrFileTooLarge
		case body.err != nil:
			return File{}, clientError(body.err)
		default:
			return File{}, fmt.Errorf("upload: store %s: %w", target, err)
		}
	}

	if err := checkImage(ctx, d, target, typ, field, r.MaxPixels); err != nil {
		_ = d.Delete(context.WithoutCancel(ctx), target)
		return File{}, err
	}

	if r.Scanner != nil {
		if err := scanAndMove(ctx, d, r.Scanner, field, target, final); err != nil {
			return File{}, err
		}
	}

	return File{
		Field:  field,
		Name:   name,
		Path:   final,
		Type:   typ,
		Size:   r.MaxFileSize - body.left,
		SHA256: hex.EncodeToString(sum.Sum(nil)),
	}, nil
}

// clientError maps a failure to read the request body to a client-safe error.
func clientError(err error) error {
	if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
		return errs.ErrRequestTooLarge
	}

	return errs.Wrap(http.StatusBadRequest, fmt.Errorf("upload: read body: %w", err))
}

// capReader fails once more than left bytes were read, so a file can never
// be stored beyond the limit, and remembers read errors so they are told
// apart from storage failures.
type capReader struct {
	r        io.Reader
	left     int64
	tooLarge bool
	err      error
}

func (c *capReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.left -= int64(n)

	if c.left < 0 {
		c.tooLarge = true
		return n, ErrFileTooLarge
	}

	if err != nil && !errors.Is(err, io.EOF) {
		c.err = err
	}

	return n, err
}

// SanitizeName returns a display-safe version of a client file name: the
// last path element (either separator), without control and format
// (bidi, zero-width) characters, quotes or leading dots, at most 200
// characters; "file" when nothing is left. It is meant for metadata (Content-Disposition, listings), never
// for storage paths.
func SanitizeName(name string) string {
	name = path.Base(strings.ReplaceAll(name, "\\", "/"))

	name = strings.Map(func(r rune) rune {
		// Format characters (bidi overrides, zero-width) can disguise the
		// extension: a right-to-left override before "fdp.exe" displays the
		// name as "exe.pdf".
		if r < 0x20 || r == 0x7f || r == '"' || r == '/' || r == utf8.RuneError || unicode.Is(unicode.Cf, r) {
			return -1
		}

		return r
	}, name)

	name = strings.TrimLeft(strings.TrimSpace(name), ".")

	if utf8.RuneCountInString(name) > maxNameLen {
		ext := path.Ext(name)
		if utf8.RuneCountInString(ext) > 16 {
			ext = ""
		}

		runes := []rune(strings.TrimSuffix(name, ext))
		name = string(runes[:maxNameLen-utf8.RuneCountInString(ext)]) + ext
	}

	if name == "" {
		return "file"
	}

	return name
}
