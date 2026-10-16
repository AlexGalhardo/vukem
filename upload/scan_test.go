package upload

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/AlexGalhardo/vukem/errs"
	"github.com/AlexGalhardo/vukem/storage"
)

const eicar = `X5O!P%@AP[4\PZX54(P^)7CC)7}$EICAR-STANDARD-ANTIVIRUS-TEST-FILE!$H+H*`

// fakeClamd speaks enough of clamd's INSTREAM protocol to flag EICAR.
func fakeClamd(t *testing.T) string {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = ln.Close() })

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}

			go serveClamd(conn)
		}
	}()

	return ln.Addr().String()
}

func serveClamd(conn net.Conn) {
	defer conn.Close()

	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	br := bufio.NewReader(conn)

	cmd, err := br.ReadString(0)
	if err != nil || cmd != "zINSTREAM\x00" {
		_, _ = io.WriteString(conn, "UNKNOWN COMMAND\x00")
		return
	}

	var data bytes.Buffer

	for {
		var n uint32
		if err := binary.Read(br, binary.BigEndian, &n); err != nil {
			return
		}

		if n == 0 {
			break
		}

		if _, err := io.CopyN(&data, br, int64(n)); err != nil {
			return
		}
	}

	if strings.Contains(data.String(), eicar) {
		_, _ = io.WriteString(conn, "stream: Eicar-Test-Signature FOUND\x00")
		return
	}

	_, _ = io.WriteString(conn, "stream: OK\x00")
}

func TestClamAV(t *testing.T) {
	t.Parallel()

	scanner := ClamAV(fakeClamd(t))
	scanner.ChunkSize = 16 // EICAR spans several chunks

	if err := scanner.Scan(t.Context(), strings.NewReader("clean file")); err != nil {
		t.Fatalf("Scan(clean) = %v", err)
	}

	err := scanner.Scan(t.Context(), strings.NewReader("prefix "+eicar))

	infected, ok := errors.AsType[*InfectedError](err)
	if !ok || !errors.Is(err, ErrInfected) || infected.Signature != "Eicar-Test-Signature" {
		t.Fatalf("Scan(eicar) = %v, want InfectedError(Eicar-Test-Signature)", err)
	}
}

func TestClamAVUnavailableFailsClosed(t *testing.T) {
	t.Parallel()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	addr := ln.Addr().String()
	_ = ln.Close()

	scanner := &ClamAVScanner{Addr: addr, Timeout: time.Second}
	if err := scanner.Scan(t.Context(), strings.NewReader("x")); err == nil || errors.Is(err, ErrInfected) {
		t.Fatalf("Scan(no clamd) = %v, want a connection error", err)
	}
}

type scanFunc func(ctx context.Context, r io.Reader) error

func (f scanFunc) Scan(ctx context.Context, r io.Reader) error { return f(ctx, r) }

func TestReceiveScansBeforeFilesBecomeVisible(t *testing.T) {
	t.Parallel()

	clean := part{field: "f", filename: "ok.txt", contentType: "text/plain", content: "hello"}
	infected := part{field: "f", filename: "bad.txt", contentType: "text/plain", content: eicar}

	tests := []struct {
		name       string
		scanner    Scanner
		part       part
		wantStatus int
	}{
		{name: "clean", scanner: ClamAV(fakeClamd(t)), part: clean},
		{name: "infected", scanner: ClamAV(fakeClamd(t)), part: infected, wantStatus: http.StatusUnprocessableEntity},
		{
			name:       "scanner down",
			scanner:    scanFunc(func(context.Context, io.Reader) error { return errors.New("connection refused") }),
			part:       clean,
			wantStatus: http.StatusServiceUnavailable,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			disk := storage.NewMemory("")
			seen := ""
			scanner := scanFunc(func(ctx context.Context, r io.Reader) error {
				files, _ := disk.List(ctx, "")
				seen = strings.Join(files, ",")

				return tt.scanner.Scan(ctx, r)
			})

			rules := Rules{MaxFileSize: 1 << 10, Types: []string{"text/plain"}, Dir: "docs", Scanner: scanner}

			form, err := receive(t, disk, rules, tt.part)
			if got := errs.Status(err); err != nil && got != tt.wantStatus || err == nil && tt.wantStatus != 0 {
				t.Fatalf("Receive() = %v (status %d), want %d", err, got, tt.wantStatus)
			}

			if !strings.HasPrefix(seen, "quarantine/.") {
				t.Errorf("file seen during the scan at %q, want quarantine/.<random>", seen)
			}

			files := listAll(t, disk)

			if tt.wantStatus != 0 {
				if len(files) != 0 {
					t.Errorf("rejected file left %v", files)
				}

				return
			}

			if len(files) != 1 || files[0] != form.Files[0].Path || !strings.HasPrefix(files[0], "docs/") {
				t.Errorf("disk = %v, want only %s", files, form.Files[0].Path)
			}
		})
	}
}
