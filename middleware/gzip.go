package middleware

import (
	"bufio"
	"compress/gzip"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
)

// minGzipSize avoids compressing tiny bodies (headers dominate).
const minGzipSize = 1024

var gzipPool = sync.Pool{New: func() any {
	w, _ := gzip.NewWriterLevel(nil, gzip.DefaultCompression)
	return w
}}

// Gzip compresses text-like responses for clients that accept gzip. Bodies
// smaller than 1 KiB, already-encoded responses, ranges and event streams
// are passed through.
func Gzip() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Add("Vary", "Accept-Encoding")

			if !strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") || r.Method == http.MethodHead ||
				r.Header.Get("Range") != "" {
				next.ServeHTTP(w, r)
				return
			}

			gw := &gzipWriter{ResponseWriter: w}
			defer gw.finish()

			next.ServeHTTP(gw, r)
		})
	}
}

type gzipWriter struct {
	http.ResponseWriter

	status   int
	buf      []byte
	decided  bool
	hijacked bool
	gz       *gzip.Writer
}

func (g *gzipWriter) WriteHeader(code int) {
	if g.status == 0 {
		g.status = code
	}
}

func (g *gzipWriter) Write(b []byte) (int, error) {
	if g.status == 0 {
		g.status = http.StatusOK
	}

	if g.decided {
		if g.gz != nil {
			return g.gz.Write(b)
		}

		return g.ResponseWriter.Write(b)
	}

	g.buf = append(g.buf, b...)
	if len(g.buf) >= minGzipSize {
		if err := g.decide(); err != nil {
			return 0, err
		}
	}

	return len(b), nil
}

func (g *gzipWriter) compressible() bool {
	h := g.Header()
	if h.Get("Content-Encoding") != "" || g.status < 200 || g.status == http.StatusNoContent ||
		g.status == http.StatusNotModified || g.status == http.StatusPartialContent {
		return false
	}

	ct := h.Get("Content-Type")
	if ct == "" {
		ct = http.DetectContentType(g.buf)
	}

	if strings.HasPrefix(ct, "text/event-stream") {
		return false
	}

	return strings.HasPrefix(ct, "text/") || strings.Contains(ct, "json") || strings.Contains(ct, "javascript") ||
		strings.Contains(ct, "xml") || strings.Contains(ct, "svg")
}

func (g *gzipWriter) decide() error {
	g.decided = true

	if len(g.buf) >= minGzipSize && g.compressible() {
		h := g.Header()
		h.Set("Content-Encoding", "gzip")
		h.Del("Content-Length")

		g.gz, _ = gzipPool.Get().(*gzip.Writer)
		g.gz.Reset(g.ResponseWriter)
	}

	g.ResponseWriter.WriteHeader(g.status)

	buf := g.buf
	g.buf = nil

	if len(buf) == 0 {
		return nil
	}

	var err error
	if g.gz != nil {
		_, err = g.gz.Write(buf)
	} else {
		_, err = g.ResponseWriter.Write(buf)
	}

	return err
}

func (g *gzipWriter) finish() {
	if g.hijacked {
		// The connection belongs to the handler now: writing the status
		// or the gzip trailer would hit it.
		if g.gz != nil {
			gzipPool.Put(g.gz)
			g.gz = nil
		}

		return
	}

	if !g.decided && g.status != 0 {
		_ = g.decide()
	}

	if g.gz != nil {
		_ = g.gz.Close()
		gzipPool.Put(g.gz)
		g.gz = nil
	}
}

// Flush supports streaming handlers.
func (g *gzipWriter) Flush() {
	if !g.decided {
		_ = g.decide()
	}

	if g.gz != nil {
		_ = g.gz.Flush()
	}

	_ = http.NewResponseController(g.ResponseWriter).Flush()
}

// Hijack supports websocket upgrades and aborted streams through the
// middleware, also when the writer below is a wrapper with Unwrap.
func (g *gzipWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	conn, rw, err := http.NewResponseController(g.ResponseWriter).Hijack()
	if err != nil {
		return nil, nil, fmt.Errorf("middleware: hijack: %w", err)
	}

	g.hijacked = true

	return conn, rw, nil
}

// Unwrap supports http.ResponseController.
func (g *gzipWriter) Unwrap() http.ResponseWriter { return g.ResponseWriter }
