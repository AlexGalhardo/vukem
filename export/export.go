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
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"iter"
	"net/http"
	"strings"
	"time"

	"github.com/AlexGalhardo/vukem/internal/disposition"
)

const (
	// DefaultFlushEvery is the number of rows between flushes.
	DefaultFlushEvery = 500
	// DefaultWriteTimeout is how long each flush window may take before
	// the write deadline cuts a stalled client.
	DefaultWriteTimeout = time.Minute

	// statusClientClosed is nginx's 499: the client went away, so the
	// error handler logs it below error level.
	statusClientClosed = 499

	bom = "\ufeff"
)

// ErrIncomplete wraps a failure after the response started: the client got
// part of the export and the connection was dropped.
var ErrIncomplete = errors.New("export: response incomplete")

// clientGoneError is returned when the request context was cancelled (the
// client disconnected): nothing failed on the server side.
type clientGoneError struct{ err error }

func (e clientGoneError) Error() string { return "export: client went away: " + e.err.Error() }

func (e clientGoneError) Unwrap() error { return e.err }

// HTTPStatus implements errs.StatusCoder.
func (clientGoneError) HTTPStatus() int { return statusClientClosed }

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

// WriteTimeout keeps the write deadline d ahead of every write to the
// client, so a long export outlives the server's WriteTimeout while a
// client that stops reading for d is still cut. The deadline is cleared
// when the export ends. Zero or negative keeps the server's deadline.
func WriteTimeout(d time.Duration) Option { return func(o *options) { o.writeTimeout = d } }

// WithBOM starts a CSV export with a UTF-8 byte order mark, which Excel
// needs to read non-ASCII text correctly. JSON and NDJSON ignore it.
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
	renewed time.Time
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

	s := &stream{w: w, r: r, rc: http.NewResponseController(w), o: o, ctype: ctype, filename: filename}
	s.bw = bufio.NewWriter(deadlineWriter{s})

	return s
}

// deadlineWriter renews the write deadline before bufio hands a full
// buffer to the client, not only at flushes: one flush window can span
// many buffers.
type deadlineWriter struct{ s *stream }

func (d deadlineWriter) Write(b []byte) (int, error) {
	if err := d.s.renew(false); err != nil {
		return 0, err
	}

	return d.s.w.Write(b)
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

	if err := s.renew(true); err != nil {
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

	if err := s.renew(false); err != nil {
		return err
	}

	if err := s.rc.Flush(); err != nil && !errors.Is(err, http.ErrNotSupported) {
		return err
	}

	return nil
}

// renew moves the write deadline writeTimeout ahead. Unless forced, it
// runs at most every writeTimeout/4 (capped at 1s), so small writes do not
// each pay for a deadline update.
func (s *stream) renew(force bool) error {
	if s.o.writeTimeout <= 0 {
		return nil
	}

	now := time.Now()
	if !force && now.Sub(s.renewed) < min(time.Second, s.o.writeTimeout/4) {
		return nil
	}

	s.renewed = now

	return s.setDeadline(now.Add(s.o.writeTimeout))
}

func (s *stream) setDeadline(t time.Time) error {
	err := s.rc.SetWriteDeadline(t)
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

	if err == nil && s.started && s.o.writeTimeout > 0 {
		// Otherwise the deadline stays on the keep-alive connection of a
		// server whose WriteTimeout is 0 (it is then never reset).
		err = s.setDeadline(time.Time{})
	}

	gone := errors.Is(err, context.Canceled) && s.r.Context().Err() != nil

	switch {
	case err == nil:
		return nil
	case !s.started && gone:
		return clientGoneError{err}
	case !s.started:
		return err
	}

	s.abort()

	if gone {
		return clientGoneError{err}
	}

	return fmt.Errorf("%w: %w", ErrIncomplete, err)
}

// abort drops the connection so the client cannot mistake the bytes it got
// for a whole file: a CSV or NDJSON file cut between rows still parses.
// HTTP/1.x connections are hijacked and closed, so the caller still gets
// the error. Where that is impossible (HTTP/2, writers without Hijack) it
// panics with http.ErrAbortHandler, net/http's documented way to abort a
// response: the server recovers it silently and resets the stream, and
// middleware.Recover re-raises it.
func (s *stream) abort() {
	conn, _, err := s.rc.Hijack()
	if err != nil {
		panic(http.ErrAbortHandler)
	}

	_ = conn.Close()
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

// EncodeCSV writes header and record(row) for each row to w, with the same
// cell rules as CSV (SafeCell unless InsecureRawCells, WithBOM), for files
// built outside a request: a ZIP entry, a storage upload, a queued job.
// FlushEvery and WriteTimeout do not apply; w gets everything by the time
// EncodeCSV returns. A failing row stops the encoding and returns its error,
// with the rows before it already written to w. JSON needs no helper here:
// use encoding/json.NewEncoder(w).
func EncodeCSV[T any](
	w io.Writer,
	header []string,
	rows iter.Seq2[T, error],
	record func(T) []string,
	opts ...Option,
) error {
	var o options
	for _, opt := range opts {
		opt(&o)
	}

	bw := bufio.NewWriter(w)
	cw := &CSVWriter{s: &stream{bw: bw, o: o}, cw: csv.NewWriter(bw)}

	err := func() error {
		if o.bom {
			if _, err := bw.WriteString(bom); err != nil {
				return err
			}
		}

		if len(header) > 0 {
			if err := cw.write(header); err != nil {
				return err
			}
		}

		for v, err := range rows {
			if err != nil {
				return err
			}

			if err := cw.write(record(v)); err != nil {
				return err
			}
		}

		return nil
	}()

	cw.cw.Flush()

	if err == nil {
		err = cw.cw.Error()
	}

	if ferr := bw.Flush(); err == nil {
		err = ferr
	}

	return err
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
// a leading apostrophe so spreadsheets show it as text. Some spreadsheets
// skip leading spaces, line breaks and quotes, so a cell whose first other
// character is =, +, - or @ is guarded too; the trade-off is that text such
// as " - note" or "'@handle" also gains the apostrophe. Plain decimal
// numbers ("-42", "+3.14") cannot run anything and stay as they are.
func SafeCell(s string) string {
	if s == "" || isNumber(s) {
		return s
	}

	switch s[0] {
	case '=', '+', '-', '@', '\t', '\r':
		return "'" + s
	}

	if t := strings.TrimLeft(s, " \t\r\n\"'"); t != "" && !isNumber(t) {
		switch t[0] {
		case '=', '+', '-', '@':
			return "'" + s
		}
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
// (either separator) and loses control characters (C0, C1, U+2028/2029),
// format characters (bidi overrides, zero-width), leading dots, trailing
// dots and spaces and the characters Windows forbids; Windows device names
// (CON, NUL, COM1...) get a "_" prefix; it is cut to 200 characters keeping
// the extension, and is "download" when nothing is left. Non-ASCII names
// get an ASCII filename plus an RFC 5987 filename*.
func ContentDisposition(filename string) string { return disposition.Attachment(filename) }
