package z

import (
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
)

// CodeInvalidFile reports a file whose type or extension is not allowed.
const CodeInvalidFile = "invalid_file"

// FileSchema validates uploaded files (*multipart.FileHeader, as put in
// web.Ctx.Input by multipart forms). The type is detected from the first
// 512 bytes of the content, never taken from the client.
//
//	"avatar": z.File().MaxSize(2 << 20).Types("image/png", "image/jpeg")
type FileSchema struct {
	o       opts[*multipart.FileHeader]
	maxSize int64
	types   []string
	exts    []string
}

// File returns a schema for an uploaded file.
func File() FileSchema { return FileSchema{} }

// GoType implements AnySchema.
func (FileSchema) GoType() reflect.Type { return reflect.TypeFor[*multipart.FileHeader]() }

func (s FileSchema) optional() bool { return s.o.optional || s.o.hasDef }

func (s FileSchema) parseAny(c *pctx, path string, in any, present bool) (any, bool, bool) {
	v, set, ok := parse(&s.o, c, path, in, present, true,
		func(c *pctx, path string, in any) (*multipart.FileHeader, bool) {
			fh, isFile := in.(*multipart.FileHeader)
			if !isFile || fh == nil {
				s.o.issue(c, path, CodeInvalidType, CodeInvalidType,
					map[string]any{"expected": "a file", "received": typeName(in)})

				return nil, false
			}

			return fh, s.check(c, path, fh)
		})

	return v, set, ok
}

func (s FileSchema) check(c *pctx, p string, fh *multipart.FileHeader) bool {
	if s.maxSize > 0 && fh.Size > s.maxSize {
		s.o.issue(c, p, CodeTooBig, "too_big.file", map[string]any{"max": s.maxSize, "max_kb": s.maxSize >> 10})
		return false
	}

	if len(s.exts) > 0 && !slices.Contains(s.exts, strings.ToLower(filepath.Ext(fh.Filename))) {
		s.o.issue(c, p, CodeInvalidFile, "file.extension", map[string]any{"extensions": strings.Join(s.exts, ", ")})
		return false
	}

	if len(s.types) > 0 && !slices.Contains(s.types, sniff(fh)) {
		s.o.issue(c, p, CodeInvalidFile, "file.type", map[string]any{"types": strings.Join(s.types, ", ")})
		return false
	}

	return true
}

// sniff detects the media type of fh's content ("" when unreadable).
func sniff(fh *multipart.FileHeader) string {
	f, err := fh.Open()
	if err != nil {
		return ""
	}
	defer f.Close()

	head := make([]byte, 512)
	n, _ := io.ReadFull(f, head)

	mt, _, err := mime.ParseMediaType(http.DetectContentType(head[:n]))
	if err != nil {
		return ""
	}

	return mt
}

// MaxSize rejects files larger than n bytes.
func (s FileSchema) MaxSize(n int64) FileSchema { s.o = s.o.clone(); s.maxSize = n; return s }

// Types allows only these MIME types, detected from the content
// ("image/png", "application/pdf", "text/plain").
func (s FileSchema) Types(mimeTypes ...string) FileSchema {
	s.o = s.o.clone()
	s.types = nil

	for _, t := range mimeTypes {
		if mt, _, err := mime.ParseMediaType(t); err == nil {
			s.types = append(s.types, mt)
		}
	}

	return s
}

// Extensions allows only these file name extensions (".png" or "png").
func (s FileSchema) Extensions(exts ...string) FileSchema {
	s.o = s.o.clone()
	s.exts = make([]string, len(exts))

	for i, e := range exts {
		s.exts[i] = "." + strings.TrimPrefix(strings.ToLower(e), ".")
	}

	return s
}

// Optional allows the file to be absent.
func (s FileSchema) Optional() FileSchema { s.o = s.o.clone(); s.o.optional = true; return s }

// Refine adds a custom check.
func (s FileSchema) Refine(fn func(*multipart.FileHeader) error) FileSchema {
	s.o = s.o.clone()
	s.o.refines = append(s.o.refines, fn)

	return s
}

// Message overrides the message for an issue code or key ("file.type").
func (s FileSchema) Message(code, message string) FileSchema {
	s.o = s.o.clone()
	if s.o.messages == nil {
		s.o.messages = map[string]string{}
	}

	s.o.messages[code] = message

	return s
}

// Describe sets a description used by schema exports.
func (s FileSchema) Describe(text string) FileSchema { s.o = s.o.clone(); s.o.desc = text; return s }

// Parse implements Schema.
func (s FileSchema) Parse(in any) (*multipart.FileHeader, error) {
	return parseRoot[*multipart.FileHeader](s, in)
}

// Check implements Schema. Files are not JSON values, so it runs the
// rules on fh directly.
func (s FileSchema) Check(fh *multipart.FileHeader) error {
	c := &pctx{}
	s.parseAny(c, "", fh, fh != nil)

	if len(c.issues) > 0 {
		return c.issues
	}

	return nil
}

func (s FileSchema) jsonSchema() map[string]any {
	m := s.o.decorate(map[string]any{"format": "binary"}, "string")
	if len(s.types) == 1 {
		m["contentMediaType"] = s.types[0]
	}

	return m
}
