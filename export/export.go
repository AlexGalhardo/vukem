// Package export streams CSV, JSON and NDJSON downloads row by row, so a
// large export never sits in memory:
//
//	return export.CSV(c.Writer(), c.Request(), "users.csv", []string{"id", "email"},
//		orm.QueryOf[User](db).Each(c.Context()),
//		func(u User) []string { return []string{strconv.FormatInt(u.ID, 10), u.Email} })
//
// Responses are attachments with a sanitized file name (RFC 6266, with an
// RFC 5987 filename* for non-ASCII names), Cache-Control: no-store and
// nosniff. CSV cells that a spreadsheet would run as a formula are written
// as text. Nothing is sent before the first row, so a query that fails up
// front still reaches the error handler; a failure after that aborts the
// connection so the client sees a failed download, not a truncated file.
//
// See https://github.com/AlexGalhardo/vukem/blob/main/docs/subsystems.md#exports.
package export

import (
	"bufio"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"net/http"
	"path"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	// DefaultFlushEvery is the number of rows between flushes.
	DefaultFlushEvery = 500
	// DefaultWriteTimeout is how long each flush window may take before
	// the write deadline cuts a stalled client.
	DefaultWriteTimeout = time.Minute

	maxNameLen = 200
	fallback   = "download"
	bom        = "\ufeff"
)

// ErrIncomplete wraps a failure after the response started: the client got
// part of the export and the connection was aborted where possible.
var ErrIncomplete = errors.New("export: response incomplete")

// Option configures an export.
type Option func(*options)

type options struct {
	flushEvery   int
	writeTimeout time.Duration
	bom          bool
	rawCells     bool
}

// FlushEvery flushes the response after every n rows (values below 1 flush
// after every row). The end of the export is always flushed.
func FlushEvery(n int) Option { return func(o *options) { o.flushEvery = max(n, 1) } }

// WriteTimeout sets the write deadline to d from the start and from every
// flush, so a long export outlives the server's WriteTimeout while a
// stalled client is still cut. Zero or negative keeps the server's deadline.
func WriteTimeout(d time.Duration) Option { return func(o *options) { o.writeTimeout = d } }

// WithBOM starts a CSV export with a UTF-8 byte order mark, which Excel
// needs to read non-ASCII text correctly.
func WithBOM() Option { return func(o *options) { o.bom = true } }

// InsecureRawCells writes CSV cells as given, without the formula-injection
// guard. Use it only for files never opened in a spreadsheet.
func InsecureRawCells() Option { return func(o *options) { o.rawCells = true } }

// stream holds the response state shared by every format.
type stream struct {
	w        http.ResponseWriter
	r        *http.Request
	rc       *http.ResponseController
	bw       *bufio.Writer
	o        options
	ctype    string
	filename string

	started bool
	dirty   bool
	rows    int
	// before runs inside start, before the first body byte (CSV header,
	// JSON "[").
	before func() error
	// buffered flushes writers layered over bw (csv.Writer).
	buffered func() error
}

func newStream(w http.ResponseWriter, r *http.Request, ctype, filename, ext string, opts []Option) *stream {
	o := options{flushEvery: DefaultFlushEvery, writeTimeout: DefaultWriteTimeout}
	for _, opt := range opts {
		opt(&o)
	}

	if filename == "" {
		filename = "export." + ext
	}

	return &stream{
		w: w, r: r, rc: http.NewResponseController(w), bw: bufio.NewWriter(w),
		o: o, ctype: ctype, filename: filename,
	}
}

// start sends the headers once, right before the first body byte.
func (s *stream) start() error {
	if s.started {
		return nil
	}

	if err := s.r.Context().Err(); err != nil {
		return err
	}

	s.started = true

	if err := s.extendDeadline(); err != nil {
		return err
	}

	h := s.w.Header()
	h.Set("Content-Type", s.ctype)
	h.Set("Content-Disposition", ContentDisposition(s.filename))
	// Exports usually hold personal data: never keep them in a cache.
	h.Set("Cache-Control", "no-store")
	h.Set("X-Content-Type-Options", "nosniff")
	s.w.WriteHeader(http.StatusOK)

	if s.before != nil {
		s.dirty = true

		return s.before()
	}

	return nil
}

// row starts the response if needed; it runs before each row is written.
func (s *stream) row() error {
	if err := s.r.Context().Err(); err != nil {
		return err
	}

	return s.start()
}

// wrote counts a written row and flushes at the cadence.
func (s *stream) wrote() error {
	s.rows++
	s.dirty = true

	if s.rows%s.o.flushEvery == 0 {
		return s.flush()
	}

	return nil
}

func (s *stream) flush() error {
	if s.buffered != nil {
		if err := s.buffered(); err != nil {
			return err
		}
	}

	if err := s.bw.Flush(); err != nil {
		return err
	}

	s.dirty = false

	if err := s.rc.Flush(); err != nil && !errors.Is(err, http.ErrNotSupported) {
		return err
	}

	return s.extendDeadline()
}

func (s *stream) extendDeadline() error {
	if s.o.writeTimeout <= 0 {
		return nil
	}

	err := s.rc.SetWriteDeadline(time.Now().Add(s.o.writeTimeout))
	if err != nil && !errors.Is(err, http.ErrNotSupported) {
		return err
	}

	return nil
}

// finish completes the export after fn ran and returns its outcome.
func (s *stream) finish(err error, after func() error) error {
	if err == nil {
		err = s.start()
	}

	if err == nil && after != nil {
		s.dirty = true
		err = after()
	}

	if err == nil && s.dirty {
		err = s.flush()
	}

	if err == nil || !s.started {
		return err
	}

	s.abort()

	return fmt.Errorf("%w: %w", ErrIncomplete, err)
}

// abort drops the connection so the client cannot mistake the bytes it got
// for a whole file: a CSV or NDJSON file cut between rows still parses.
//
// shortcut: HTTP/2 has no hijack, so there the stream ends cleanly and only
// ErrIncomplete reports the failure; panic(http.ErrAbortHandler) in the
// handler resets it.
func (s *stream) abort() {
	conn, _, err := s.rc.Hijack()
	if err == nil {
		_ = conn.Close()
	}
}

// each pulls rows and writes them with fn.
func each[T any](s *stream, rows iter.Seq2[T, error], fn func(T) error) error {
	for v, err := range rows {
		if err != nil {
			return err
		}

		if err := s.row(); err != nil {
			return err
		}

		if err := fn(v); err != nil {
			return err
		}

		if err := s.wrote(); err != nil {
			return err
		}
	}

	return nil
}

// CSVWriter writes CSV records to an export started by WriteCSV.
type CSVWriter struct {
	s  *stream
	cw *csv.Writer
}

// Write writes one record (an empty record writes an empty line). Each
// cell goes through SafeCell unless InsecureRawCells is set.
func (w *CSVWriter) Write(record []string) error {
	if err := w.s.row(); err != nil {
		return err
	}

	if err := w.write(record); err != nil {
		return err
	}

	return w.s.wrote()
}

func (w *CSVWriter) write(record []string) error {
	if !w.s.o.rawCells {
		safe := make([]string, len(record))
		for i, c := range record {
			safe[i] = SafeCell(c)
		}

		record = safe
	}

	if len(record) == 0 {
		// csv.Writer writes nothing for an empty record; a blank line
		// separates sections.
		w.cw.Flush()

		if err := w.cw.Error(); err != nil {
			return err
		}

		_, err := w.s.bw.WriteString("\n")

		return err
	}

	return w.cw.Write(record)
}

func newCSV(w http.ResponseWriter, r *http.Request, filename string, opts []Option) (*stream, *CSVWriter) {
	s := newStream(w, r, "text/csv; charset=utf-8", filename, "csv", opts)
	cw := &CSVWriter{s: s, cw: csv.NewWriter(s.bw)}

	s.buffered = func() error {
		cw.cw.Flush()

		return cw.cw.Error()
	}

	if s.o.bom {
		s.before = func() error {
			_, err := s.bw.WriteString(bom)

			return err
		}
	}

	return s, cw
}

// WriteCSV streams a CSV attachment whose records fn writes, for exports
// that are not one row per item (several sections, summary rows).
func WriteCSV(
	w http.ResponseWriter,
	r *http.Request,
	filename string,
	fn func(*CSVWriter) error,
	opts ...Option,
) error {
	s, cw := newCSV(w, r, filename, opts)

	return s.finish(fn(cw), nil)
}

// CSV streams rows as a CSV attachment: header first, then record(row) for
// each row. Pass orm Query.Each as rows to read from the database lazily.
func CSV[T any](
	w http.ResponseWriter, r *http.Request, filename string, header []string,
	rows iter.Seq2[T, error], record func(T) []string, opts ...Option,
) error {
	s, cw := newCSV(w, r, filename, opts)

	if before := s.before; len(header) > 0 {
		s.before = func() error {
			if before != nil {
				if err := before(); err != nil {
					return err
				}
			}

			return cw.write(header)
		}
	}

	return s.finish(each(s, rows, func(v T) error { return cw.write(record(v)) }), nil)
}

// JSON streams rows as a JSON array attachment, one element per row.
func JSON[T any](
	w http.ResponseWriter,
	r *http.Request,
	filename string,
	rows iter.Seq2[T, error],
	opts ...Option,
) error {
	s := newStream(w, r, "application/json; charset=utf-8", filename, "json", opts)
	s.before = func() error { return s.bw.WriteByte('[') }

	first := true
	err := each(s, rows, func(v T) error {
		b, err := json.Marshal(v)
		if err != nil {
			return fmt.Errorf("export: encode row: %w", err)
		}

		if !first {
			if err := s.bw.WriteByte(','); err != nil {
				return err
			}
		}

		first = false
		_, err = s.bw.Write(b)

		return err
	})

	return s.finish(err, func() error {
		_, err := s.bw.WriteString("]\n")

		return err
	})
}

// NDJSON streams rows as newline-delimited JSON (application/x-ndjson),
// one object per line.
func NDJSON[T any](
	w http.ResponseWriter,
	r *http.Request,
	filename string,
	rows iter.Seq2[T, error],
	opts ...Option,
) error {
	s := newStream(w, r, "application/x-ndjson", filename, "ndjson", opts)

	err := each(s, rows, func(v T) error {
		b, err := json.Marshal(v)
		if err != nil {
			return fmt.Errorf("export: encode row: %w", err)
		}

		if _, err := s.bw.Write(b); err != nil {
			return err
		}

		return s.bw.WriteByte('\n')
	})

	return s.finish(err, nil)
}

// SafeCell guards a CSV cell against formula injection (OWASP CSV
// injection): a cell starting with =, +, -, @, tab or carriage return gets
// a leading apostrophe so spreadsheets show it as text. Plain decimal
// numbers ("-42", "+3.14") cannot run anything and stay as they are.
func SafeCell(s string) string {
	if s == "" || isNumber(s) {
		return s
	}

	switch s[0] {
	case '=', '+', '-', '@', '\t', '\r':
		return "'" + s
	}

	return s
}

// isNumber reports whether s is [+-]digits[.digits].
func isNumber(s string) bool {
	if s[0] == '+' || s[0] == '-' {
		s = s[1:]
	}

	intPart, frac, hasDot := strings.Cut(s, ".")

	return allDigits(intPart) && (!hasDot || allDigits(frac))
}

func allDigits(s string) bool {
	if s == "" {
		return false
	}

	for i := range len(s) {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}

	return true
}

// ContentDisposition returns an attachment Content-Disposition for
// filename (RFC 6266). The name is reduced to its last path element
// (either separator), loses control and format characters (bidi
// overrides, zero-width), leading dots and characters Windows forbids, and
// is cut to 200 characters keeping the extension; "download" when nothing
// is left. Non-ASCII names get an ASCII filename plus an RFC 5987
// filename*.
func ContentDisposition(filename string) string {
	name := sanitize(filename)

	ascii := strings.Map(func(r rune) rune {
		// Some browsers percent-decode filename, so % goes too.
		if r >= utf8.RuneSelf || r == '%' {
			return '_'
		}

		return r
	}, name)

	v := `attachment; filename="` + ascii + `"`
	if ascii != name {
		v += "; filename*=UTF-8''" + encode5987(name)
	}

	return v
}

func sanitize(name string) string {
	name = path.Base(strings.ReplaceAll(name, `\`, "/"))

	name = strings.Map(func(r rune) rune {
		switch {
		case r < 0x20, r == 0x7f, r == utf8.RuneError, unicode.Is(unicode.Cf, r):
			return -1
		case strings.ContainsRune(`<>:"/|?*`, r):
			return '_'
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
		return fallback
	}

	return name
}

// encode5987 percent-encodes every byte outside attr-char (RFC 5987 §3.2.1).
func encode5987(s string) string {
	const hex = "0123456789ABCDEF"

	var b strings.Builder

	for i := range len(s) {
		c := s[i]
		if c < utf8.RuneSelf && (c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' ||
			strings.IndexByte("!#$&+-.^_`|~", c) >= 0) {
			b.WriteByte(c)

			continue
		}

		b.WriteByte('%')
		b.WriteByte(hex[c>>4])
		b.WriteByte(hex[c&0x0f])
	}

	return b.String()
}
