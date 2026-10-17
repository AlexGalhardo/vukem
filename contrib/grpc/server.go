package grpc

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/AlexGalhardo/vukem"
)

// ErrCleartextInProduction is returned by Server and Serve for a server
// without TLS in production when HTTP_H2C is not enabled.
var ErrCleartextInProduction = errors.New(
	"grpc: cleartext gRPC server in production: set ServerOptions.TLS, or HTTP_H2C=true behind a proxy that speaks h2c",
)

// ServerOptions configures Server and Serve.
type ServerOptions struct {
	// TLS serves HTTP/2 and HTTP/1.1 over TLS; NextProtos defaults to h2
	// and http/1.1. Nil serves HTTP/2 cleartext (h2c, prior knowledge) and
	// HTTP/1.1.
	TLS *tls.Config
	// ShutdownTimeout bounds the graceful drain of in-flight RPCs. Zero
	// means 15s for Server and SHUTDOWN_TIMEOUT for Serve.
	ShutdownTimeout time.Duration
	// Ready, when set, receives the bound address once listening.
	Ready chan<- string
}

// Server returns a runner serving h, usually a router holding only the RPC
// services (Mount, Health, Reflection), on its own address, with the
// secure timeouts of vukem.NewHTTPServer and a graceful drain when its
// context ends. cfg is the app config (app.Config): in production a
// server without TLS is refused unless HTTP_H2C=true, and a nil cfg
// counts as production. Streams longer than the write timeout (60s) need
// a server of their own built from vukem.NewHTTPServer.
func Server(cfg *vukem.Config, addr string, h http.Handler, opts ServerOptions) (vukem.Runner, error) {
	if opts.TLS == nil && (cfg == nil || cfg.IsProduction() && !cfg.H2C) {
		return nil, ErrCleartextInProduction
	}

	return vukem.RunnerFunc(func(ctx context.Context) error {
		srv := vukem.NewHTTPServer(addr, h)
		if opts.ShutdownTimeout > 0 {
			srv.ShutdownTimeout = opts.ShutdownTimeout
		}

		srv.Ready = opts.Ready
		srv.Server.Protocols = new(http.Protocols)
		srv.Server.Protocols.SetHTTP1(true)

		if opts.TLS == nil {
			srv.Server.Protocols.SetUnencryptedHTTP2(true)
			return srv.Run(ctx)
		}

		tlsCfg := opts.TLS.Clone()
		if len(tlsCfg.NextProtos) == 0 {
			tlsCfg.NextProtos = []string{"h2", "http/1.1"}
		}

		srv.Server.Protocols.SetHTTP2(true)
		srv.Server.TLSConfig = tlsCfg

		var lc net.ListenConfig

		ln, err := lc.Listen(ctx, "tcp", addr)
		if err != nil {
			return fmt.Errorf("grpc: listen %s: %w", addr, err)
		}

		srv.Listener = tls.NewListener(ln, tlsCfg)

		return srv.Run(ctx)
	}), nil
}

// Serve adds a "grpc" runner to app serving h on addr (see Server), so
// RPCs get their own port next to the HTTP server and stop with the app.
// Call it from a provider's Register or Boot. In production a server
// without TLS is refused unless HTTP_H2C=true (ErrCleartextInProduction).
//
// The port does not inherit the HTTP router's middleware: give h its own
// stack (vukem.APIMiddleware, grpc.Required, rate limiting) and a message
// cap (HandlerOptions sets one), since the server bounds header sizes and
// timeouts, not bodies.
func Serve(app *vukem.App, addr string, h http.Handler, opts ServerOptions) error {
	if opts.ShutdownTimeout <= 0 {
		opts.ShutdownTimeout = app.Config.ShutdownTimeout
	}

	run, err := Server(&app.Config, addr, h, opts)
	if err != nil {
		return err
	}

	app.AddRunner("grpc", vukem.RunnerFunc(func(ctx context.Context) error {
		app.Log.InfoContext(ctx, "grpc server starting", "addr", addr, "tls", opts.TLS != nil)
		return run.Run(ctx)
	}))

	return nil
}
