package upload

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/AlexGalhardo/vukem/errs"
	"github.com/AlexGalhardo/vukem/storage"
)

// Scanner inspects a file before it becomes visible. Scan returns an
// error wrapping ErrInfected for malware; any other error means the scan
// could not run, and the upload is rejected (fail closed).
type Scanner interface {
	Scan(ctx context.Context, r io.Reader) error
}

// ErrInfected is wrapped by Scanner errors for infected files.
var ErrInfected = errors.New("upload: file is infected")

// InfectedError names the signature a scanner found.
type InfectedError struct {
	Signature string
}

// Error implements error.
func (e *InfectedError) Error() string { return "upload: file is infected: " + e.Signature }

// Unwrap makes errors.Is(err, ErrInfected) true.
func (e *InfectedError) Unwrap() error { return ErrInfected }

// errScanUnavailable is the client-facing error when a scan cannot run.
var errScanUnavailable = errs.New(http.StatusServiceUnavailable, "The file could not be checked. Try again later.")

// scanAndMove scans the quarantined file and moves it to final, deleting
// it when it is infected or the scan fails.
func scanAndMove(ctx context.Context, d storage.Disk, s Scanner, field, quarantined, final string) error {
	cleanup := context.WithoutCancel(ctx)

	rc, err := d.Open(ctx, quarantined)
	if err != nil {
		_ = d.Delete(cleanup, quarantined)
		return fmt.Errorf("upload: open quarantined file: %w", err)
	}

	scanErr := s.Scan(ctx, rc)
	_ = rc.Close()

	if scanErr != nil {
		_ = d.Delete(cleanup, quarantined)

		if errors.Is(scanErr, ErrInfected) {
			return errs.NewValidation().Add(field, "The file was rejected by the virus scanner.")
		}

		// The cause is logged by the error handler, never shown.
		unavailable := errScanUnavailable.WithHeader("Retry-After", "60")
		unavailable.Err = fmt.Errorf("upload: scan: %w", scanErr)

		return unavailable
	}

	rc, err = d.Open(ctx, quarantined)
	if err != nil {
		_ = d.Delete(cleanup, quarantined)
		return fmt.Errorf("upload: open quarantined file: %w", err)
	}

	err = d.Put(ctx, final, rc)
	_ = rc.Close()
	_ = d.Delete(cleanup, quarantined)

	if err != nil {
		_ = d.Delete(cleanup, final)
		return fmt.Errorf("upload: move scanned file: %w", err)
	}

	return nil
}

// ClamAVScanner scans files with clamd's INSTREAM command over TCP.
type ClamAVScanner struct {
	// Addr is clamd's host:port.
	Addr string
	// Timeout bounds dialing and each read or write (default 30s).
	Timeout time.Duration
	// ChunkSize is the INSTREAM chunk size (default 64 KiB). Keep it
	// below clamd's StreamMaxLength.
	ChunkSize int
}

// ClamAV returns a Scanner talking to clamd at addr (for example
// "127.0.0.1:3310").
func ClamAV(addr string) *ClamAVScanner { return &ClamAVScanner{Addr: addr} }

// maxReply bounds clamd's answer.
const maxReply = 4 << 10

// Scan implements Scanner.
func (s *ClamAVScanner) Scan(ctx context.Context, r io.Reader) error {
	timeout := s.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}

	chunk := s.ChunkSize
	if chunk <= 0 {
		chunk = 64 << 10
	}

	dialer := net.Dialer{Timeout: timeout}

	conn, err := dialer.DialContext(ctx, "tcp", s.Addr)
	if err != nil {
		return fmt.Errorf("upload: clamav: %w", err)
	}
	defer conn.Close()

	stop := context.AfterFunc(ctx, func() { _ = conn.SetDeadline(time.Now()) })
	defer stop()

	deadline := func() { _ = conn.SetDeadline(time.Now().Add(timeout)) }
	deadline()

	if _, err := io.WriteString(conn, "zINSTREAM\x00"); err != nil {
		return fmt.Errorf("upload: clamav: %w", err)
	}

	buf := make([]byte, 4+chunk)

	for {
		n, rerr := io.ReadFull(r, buf[4:])
		if n > 0 {
			deadline()
			binary.BigEndian.PutUint32(buf[:4], uint32(n)) //nolint:gosec // n <= chunk, a small positive int

			if _, err := conn.Write(buf[:4+n]); err != nil {
				return fmt.Errorf("upload: clamav: %w", err)
			}
		}

		if errors.Is(rerr, io.EOF) || errors.Is(rerr, io.ErrUnexpectedEOF) {
			break
		}

		if rerr != nil {
			return fmt.Errorf("upload: clamav: read file: %w", rerr)
		}
	}

	deadline()

	if _, err := conn.Write([]byte{0, 0, 0, 0}); err != nil {
		return fmt.Errorf("upload: clamav: %w", err)
	}

	reply, err := bufio.NewReader(io.LimitReader(conn, maxReply)).ReadString(0)
	if err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("upload: clamav: read reply: %w", err)
	}

	return parseClamReply(reply)
}

// parseClamReply reads "stream: OK", "stream: <signature> FOUND" or an
// error reply.
func parseClamReply(reply string) error {
	reply = strings.TrimSpace(strings.TrimRight(reply, "\x00"))
	_, result, _ := strings.Cut(reply, ": ")

	switch {
	case result == "OK":
		return nil
	case strings.HasSuffix(result, " FOUND"):
		return &InfectedError{Signature: strings.TrimSuffix(result, " FOUND")}
	default:
		return fmt.Errorf("upload: clamav: unexpected reply %q", reply)
	}
}
