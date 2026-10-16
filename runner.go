package lumek

import (
	"context"
	"errors"
	"net"
	"net/http"
	"time"
)

// Runner is a long-running component (HTTP server, queue worker, scheduler).
// Run blocks until ctx is done or the runner fails, and must return after
// releasing its resources.
type Runner interface {
	Run(ctx context.Context) error
}

// RunnerFunc adapts a function to a Runner.
type RunnerFunc func(ctx context.Context) error

// Run calls f.
func (f RunnerFunc) Run(ctx context.Context) error { return f(ctx) }

type namedRunner struct {
	name   string
	runner Runner
}

// Server timeouts applied by NewHTTPServer. Zero timeouts are never used
// (Slowloris protection, docs/security.md A02).
const (
	DefaultReadHeaderTimeout = 10 * time.Second
	DefaultReadTimeout       = 30 * time.Second
	DefaultWriteTimeout      = 60 * time.Second
	DefaultIdleTimeout       = 120 * time.Second
	DefaultMaxHeaderBytes    = 1 << 20
)

// HTTPServer runs an *http.Server as a Runner with graceful shutdown.
type HTTPServer struct {
	Server *http.Server
	// ShutdownTimeout bounds the graceful drain of in-flight requests.
	ShutdownTimeout time.Duration
	// Listener, when set, is used instead of listening on Server.Addr.
	Listener net.Listener
	// Ready, when set, receives the bound address once listening.
	Ready chan<- string
}

// NewHTTPServer returns an HTTPServer for handler on addr with secure
// timeouts.
func NewHTTPServer(addr string, handler http.Handler) *HTTPServer {
	return &HTTPServer{
		Server: &http.Server{
			Addr:              addr,
			Handler:           handler,
			ReadHeaderTimeout: DefaultReadHeaderTimeout,
			ReadTimeout:       DefaultReadTimeout,
			WriteTimeout:      DefaultWriteTimeout,
			IdleTimeout:       DefaultIdleTimeout,
			MaxHeaderBytes:    DefaultMaxHeaderBytes,
		},
		ShutdownTimeout: 15 * time.Second,
	}
}

// Run listens and serves until ctx is done, then drains in-flight requests.
func (s *HTTPServer) Run(ctx context.Context) error {
	ln := s.Listener
	if ln == nil {
		var err error

		var lc net.ListenConfig
		if ln, err = lc.Listen(ctx, "tcp", s.Server.Addr); err != nil {
			return err
		}
	}

	// In-flight handlers observe the base context; it is not cancelled on
	// shutdown so requests can finish, but carries the app's values.
	base := context.WithoutCancel(ctx)
	s.Server.BaseContext = func(net.Listener) context.Context { return base }

	if s.Ready != nil {
		s.Ready <- ln.Addr().String()
	}

	errc := make(chan error, 1)

	go func() { errc <- s.Server.Serve(ln) }()

	select {
	case err := <-errc:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}

		return err
	case <-ctx.Done():
	}

	shutCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), s.ShutdownTimeout)
	defer cancel()

	if err := s.Server.Shutdown(shutCtx); err != nil {
		return err
	}

	if err := <-errc; err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}

	return nil
}
