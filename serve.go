package vukem

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net"
	"net/http"
	"slices"
	"sync"
	"time"

	"github.com/AlexGalhardo/vukem/sse"
)

// ErrWarmupRun is returned by Boot (and so Run) on a KindWarmup app: it is
// built only to read its routes and may lack APP_KEY, so it never opens
// connections or serves. Never expose a warmup app's Router or Handler.
var ErrWarmupRun = errors.New("vukem: a warmup app cannot boot or run")

// Run boots the app, starts every runner and blocks until ctx is cancelled
// or a runner fails; then it shuts everything down gracefully within
// SHUTDOWN_TIMEOUT. A cancelled ctx is a clean exit (nil error). Runners
// that have not returned SHUTDOWN_TIMEOUT (plus three seconds) after the
// cancel are logged and left behind: shutdown hooks and providers run
// anyway, so one runner ignoring ctx cannot hold the process forever.
func (a *App) Run(ctx context.Context) error {
	if err := a.Boot(ctx); err != nil {
		return err
	}

	if a.Kind == KindWeb && !a.noHTTP {
		r, err := a.httpRunner()
		if err != nil {
			return err
		}

		a.AddRunner("http", r)
	}

	a.mu.Lock()
	runners := slices.Clone(a.runners)
	a.mu.Unlock()

	runCtx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)

	var (
		wg       sync.WaitGroup
		errMu    sync.Mutex
		firstErr error
		running  = map[string]int{}
	)

	for _, r := range runners {
		running[r.name]++
	}

	for _, r := range runners {
		wg.Go(func() {
			err := r.runner.Run(runCtx)

			errMu.Lock()
			defer errMu.Unlock()

			running[r.name]--
			if running[r.name] == 0 {
				delete(running, r.name)
			}

			if err != nil && !errors.Is(err, context.Canceled) {
				if firstErr == nil {
					firstErr = fmt.Errorf("vukem: runner %s: %w", r.name, err)
				}

				cancel(err)
			}
		})
	}

	<-runCtx.Done()
	a.Log.InfoContext(ctx, "shutting down", "cause", context.Cause(runCtx))

	// Runners observe runCtx and stop on their own; wait for them first.
	cancel(nil)

	if !waitTimeout(&wg, a.Config.ShutdownTimeout+runnerGrace) {
		errMu.Lock()
		stuck := slices.Sorted(maps.Keys(running))
		errMu.Unlock()

		a.Log.ErrorContext(ctx, "runners still running after SHUTDOWN_TIMEOUT: shutting down without them",
			"runners", stuck, "shutdown_timeout", a.Config.ShutdownTimeout)
	}

	shutCtx, stop := context.WithTimeout(context.WithoutCancel(ctx), a.Config.ShutdownTimeout)
	defer stop()

	errMu.Lock()
	runErr := firstErr
	errMu.Unlock()

	return errors.Join(runErr, a.Shutdown(shutCtx))
}

// runnerGrace lets runners that stop at SHUTDOWN_TIMEOUT themselves (the
// HTTP drain, the scheduler, the queue worker) return before Run stops
// waiting for them.
const runnerGrace = 3 * time.Second

// waitTimeout waits for wg up to d and reports whether it finished.
func waitTimeout(wg *sync.WaitGroup, d time.Duration) bool {
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()

	t := time.NewTimer(d)
	defer t.Stop()

	select {
	case <-done:
		return true
	case <-t.C:
		return false
	}
}

// Shutdown runs shutdown hooks and providers' Shutdown in reverse order.
// It is idempotent.
func (a *App) Shutdown(ctx context.Context) error {
	a.mu.Lock()
	if a.shutdown {
		a.mu.Unlock()
		return nil
	}

	a.shutdown = true
	hooks := slices.Clone(a.hooks)
	a.mu.Unlock()

	var errs []error

	for _, fn := range slices.Backward(hooks) {
		if err := fn(ctx); err != nil {
			errs = append(errs, err)
		}
	}

	for _, p := range slices.Backward(a.providers) {
		s, ok := p.(Shutdowner)
		if !ok || !runsIn(p, a.Kind) || a.Kind == KindWarmup {
			continue
		}

		if err := s.Shutdown(ctx, a); err != nil {
			errs = append(errs, fmt.Errorf("vukem: shutdown %T: %w", p, err))
		}
	}

	return errors.Join(errs...)
}

// Server timeouts applied by NewHTTPServer. Zero timeouts are never used
// (Slowloris protection, docs/security.md A02).
const (
	DefaultReadHeaderTimeout = 10 * time.Second
	DefaultReadTimeout       = 30 * time.Second
	DefaultWriteTimeout      = 60 * time.Second
	DefaultIdleTimeout       = 120 * time.Second
	// DefaultMaxHeaderBytes is 64 KiB, not Go's 1 MiB
	// (http.DefaultMaxHeaderBytes, which is only the ceiling
	// HTTP_MAX_HEADER_BYTES accepts): it bounds the
	// memory each connection can make the server buffer, and is still far
	// above what browsers send and common proxies forward (nginx 8 KiB per
	// line, Node.js 16 KiB in total). HTTP_MAX_HEADER_BYTES overrides it.
	DefaultMaxHeaderBytes = 64 << 10
)

// minMaxHeaderBytes is the smallest HTTP_MAX_HEADER_BYTES accepted: below
// it ordinary browser requests with a few cookies fail.
const minMaxHeaderBytes = 4 << 10

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
	// Server-sent event streams end when shutdown starts, so Shutdown does
	// not wait on connections that never finish by themselves.
	stopping := make(chan struct{})
	s.Server.RegisterOnShutdown(sync.OnceFunc(func() { close(stopping) }))

	base := sse.WithShutdown(context.WithoutCancel(ctx), stopping)
	s.Server.BaseContext = func(net.Listener) context.Context { return base }

	if s.Ready != nil {
		s.Ready <- ln.Addr().String()
	}

	errc := make(chan error, 1)

	go func() {
		if s.Server.TLSConfig != nil {
			// Certificates come from TLSConfig; ServeTLS adds ALPN h2 per Protocols.
			errc <- s.Server.ServeTLS(ln, "", "")
		} else {
			errc <- s.Server.Serve(ln)
		}
	}()

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
