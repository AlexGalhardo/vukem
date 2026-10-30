// Package http3 serves a vukem app over HTTP/3 (QUIC, UDP) next to its
// HTTPS server, on the same port by default:
//
//	if err := http3.Serve(app, http3.Options{}); err != nil {
//		return err
//	}
//
// It shares the app's handler and certificate (TLS_CERT_FILE and
// TLS_KEY_FILE, or Options.TLS), advertises itself with an Alt-Svc header
// on the HTTP/1.1 and HTTP/2 responses, and stops gracefully with the app.
// The QUIC stack is github.com/quic-go/quic-go: the standard library has no
// HTTP/3 server and golang.org/x/net/quic is not yet covered by the Go
// security policy (checked 2026-10-29, x/net v0.62.0).
//
// See https://github.com/AlexGalhardo/vukem/blob/main/docs/http.md#http3.
package http3

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/quic-go/quic-go"
	qhttp3 "github.com/quic-go/quic-go/http3"

	"github.com/AlexGalhardo/vukem"
)

// ErrNoTLS reports an HTTP/3 server without a certificate: QUIC always
// runs over TLS 1.3.
var ErrNoTLS = errors.New("http3: needs a certificate: set TLS_CERT_FILE and TLS_KEY_FILE, or Options.TLS")

// Options configure Serve. Zero values take the defaults.
type Options struct {
	// Addr is the UDP address (default the app's HOST:PORT, the HTTPS port).
	Addr string
	// TLS is the certificate source (default: TLS_CERT_FILE/TLS_KEY_FILE,
	// reloaded when they change). QUIC negotiates TLS 1.3 only.
	TLS *tls.Config
	// MaxStreams caps concurrent request streams per connection (default 100).
	MaxStreams int64
	// IdleTimeout closes idle connections (default vukem.DefaultIdleTimeout).
	IdleTimeout time.Duration
	// MaxHeaderBytes caps request headers (default HTTP_MAX_HEADER_BYTES,
	// else vukem.DefaultMaxHeaderBytes).
	MaxHeaderBytes int
	// ShutdownTimeout bounds the graceful drain (default SHUTDOWN_TIMEOUT).
	ShutdownTimeout time.Duration
	// Ready, when set, receives the bound UDP address.
	Ready chan<- string
}

// Serve adds an "http3" runner to app and the Alt-Svc header to its other
// responses. Call it before the app serves (in bootstrap, after routes).
func Serve(app *vukem.App, opts Options) error {
	cfg, err := resolve(app, opts)
	if err != nil {
		return err
	}

	srv := &qhttp3.Server{
		TLSConfig:      qhttp3.ConfigureTLSConfig(cfg.TLS),
		QUICConfig:     &quic.Config{MaxIncomingStreams: cfg.MaxStreams, MaxIdleTimeout: cfg.IdleTimeout},
		MaxHeaderBytes: cfg.MaxHeaderBytes,
		IdleTimeout:    cfg.IdleTimeout,
		Logger:         app.Log,
	}

	app.Router.UseServer(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.ProtoMajor < 3 {
				// Fails only before the listener is bound; then nothing is advertised.
				_ = srv.SetQUICHeaders(w.Header())
			}

			next.ServeHTTP(w, r)
		})
	})

	app.AddRunner("http3", vukem.RunnerFunc(func(ctx context.Context) error {
		h, err := app.Handler()
		if err != nil {
			return err
		}

		srv.Handler = h

		app.Log.InfoContext(ctx, "http3 server starting", "addr", cfg.Addr)

		return run(ctx, srv, cfg)
	}))

	return nil
}

func resolve(app *vukem.App, opts Options) (Options, error) {
	if opts.Addr == "" {
		opts.Addr = app.Addr()
	}

	if opts.MaxStreams <= 0 {
		opts.MaxStreams = 100
	}

	if opts.IdleTimeout <= 0 {
		opts.IdleTimeout = vukem.DefaultIdleTimeout
	}

	if opts.ShutdownTimeout <= 0 {
		opts.ShutdownTimeout = app.Config.ShutdownTimeout
	}

	if opts.MaxHeaderBytes <= 0 {
		opts.MaxHeaderBytes = vukem.DefaultMaxHeaderBytes

		// Read from the source so this module builds against older cores.
		if v := app.Source.Get("HTTP_MAX_HEADER_BYTES"); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil || n <= 0 {
				return opts, fmt.Errorf("http3: HTTP_MAX_HEADER_BYTES %q is not a positive number", v)
			}

			opts.MaxHeaderBytes = n
		}
	}

	if opts.TLS != nil {
		opts.TLS = opts.TLS.Clone()
		return opts, nil
	}

	certFile, keyFile := app.Source.Get("TLS_CERT_FILE"), app.Source.Get("TLS_KEY_FILE")
	if certFile == "" || keyFile == "" {
		return opts, ErrNoTLS
	}

	r := &reloader{certFile: certFile, keyFile: keyFile}
	if err := r.load(); err != nil {
		return opts, err
	}

	opts.TLS = &tls.Config{MinVersion: tls.VersionTLS13, GetCertificate: r.get}

	return opts, nil
}

func run(ctx context.Context, srv *qhttp3.Server, cfg Options) error {
	var lc net.ListenConfig

	conn, err := lc.ListenPacket(ctx, "udp", cfg.Addr)
	if err != nil {
		return fmt.Errorf("http3: listen %s: %w", cfg.Addr, err)
	}
	defer conn.Close()

	if cfg.Ready != nil {
		cfg.Ready <- conn.LocalAddr().String()
	}

	errc := make(chan error, 1)

	go func() { errc <- srv.Serve(conn) }()

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}

	shutCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cfg.ShutdownTimeout)
	defer cancel()

	err = srv.Shutdown(shutCtx)
	if serveErr := <-errc; err == nil && !errors.Is(serveErr, http.ErrServerClosed) {
		err = serveErr
	}

	return err
}

// reloader serves the TLS_CERT_FILE/TLS_KEY_FILE pair and reloads it when a
// modification time moves (checked at most once a second), like the core
// HTTPS server; a pair that fails to load keeps the previous certificate.
type reloader struct {
	certFile, keyFile string

	mu      sync.Mutex
	cert    *tls.Certificate
	mod     time.Time
	checked time.Time
}

func (r *reloader) get(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if time.Since(r.checked) >= time.Second {
		r.checked = time.Now()
		_ = r.load() // keep serving the previous certificate on failure
	}

	return r.cert, nil
}

func (r *reloader) load() error {
	var mod time.Time

	for _, f := range []string{r.certFile, r.keyFile} {
		fi, err := os.Stat(f)
		if err != nil {
			return fmt.Errorf("http3: %w", err)
		}

		if fi.ModTime().After(mod) {
			mod = fi.ModTime()
		}
	}

	if r.cert != nil && mod.Equal(r.mod) {
		return nil
	}

	cert, err := tls.LoadX509KeyPair(r.certFile, r.keyFile)
	if err != nil {
		return fmt.Errorf("http3: %w", err)
	}

	r.cert, r.mod = &cert, mod

	return nil
}
