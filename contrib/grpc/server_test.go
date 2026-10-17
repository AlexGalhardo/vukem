package grpc_test

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"connectrpc.com/connect"

	"github.com/AlexGalhardo/vukem"
	"github.com/AlexGalhardo/vukem/config"
	"github.com/AlexGalhardo/vukem/contrib/grpc"
	greetv1 "github.com/AlexGalhardo/vukem/contrib/grpc/testdata/gen/greet/v1"
	"github.com/AlexGalhardo/vukem/contrib/grpc/testdata/gen/greet/v1/greetv1connect"
	"github.com/AlexGalhardo/vukem/router"
)

func rpcRouter() *router.Router {
	r := router.New(nil)
	path, h := greetv1connect.NewGreetServiceHandler(greeter{}, grpc.HandlerOptions(grpc.Options{
		Log: slog.New(slog.DiscardHandler),
	})...)
	grpc.Mount(r, path, h).Use(grpc.Required(bearerGuard{}))

	return r
}

// startServer runs runner until the test ends and returns the bound
// address and the runner's result.
func startServer(t *testing.T, runner vukem.Runner, ready <-chan string) (string, func() error) {
	t.Helper()

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)

	go func() { done <- runner.Run(ctx) }()

	var addr string
	select {
	case addr = <-ready:
	case err := <-done:
		cancel()
		t.Fatalf("Run() = %v before listening", err)
	}

	stop := sync.OnceValue(func() error {
		cancel()
		return <-done
	})
	t.Cleanup(func() { _ = stop() })

	return addr, stop
}

func TestServer_ServesGRPCOnItsOwnPort(t *testing.T) {
	t.Parallel()

	tlsSrv := httptest.NewUnstartedServer(http.NotFoundHandler())
	tlsSrv.EnableHTTP2 = true
	tlsSrv.StartTLS()
	t.Cleanup(tlsSrv.Close)

	h2c := &http.Transport{Protocols: new(http.Protocols)}
	h2c.Protocols.SetUnencryptedHTTP2(true)
	t.Cleanup(h2c.CloseIdleConnections)

	tests := []struct {
		name   string
		tls    *tls.Config
		scheme string
		client *http.Client
	}{
		{"h2c", nil, "http", &http.Client{Transport: h2c}},
		{"tls", &tls.Config{Certificates: tlsSrv.TLS.Certificates}, "https", tlsSrv.Client()},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ready := make(chan string, 1)
			addr, stop := startServer(t, grpc.Server("127.0.0.1:0", rpcRouter(), grpc.ServerOptions{
				TLS: tt.tls, Ready: ready,
			}), ready)

			client := greetv1connect.NewGreetServiceClient(tt.client, tt.scheme+"://"+addr, connect.WithGRPC())

			resp, err := client.Greet(t.Context(), authed(&greetv1.GreetRequest{Name: "Ada"}))
			if err != nil {
				t.Fatalf("Greet() error = %v", err)
			}

			if got, want := resp.Msg.GetGreeting(), "Hello, Ada from ada"; got != want {
				t.Errorf("Greet() = %q, want %q", got, want)
			}

			if err := stop(); err != nil {
				t.Errorf("Run() after cancel = %v, want nil", err)
			}
		})
	}
}

func TestServer_ListenErrorFails(t *testing.T) {
	t.Parallel()

	err := grpc.Server("127.0.0.1:-1", rpcRouter(), grpc.ServerOptions{}).Run(t.Context())
	if err == nil {
		t.Fatal("Run(invalid addr) = nil, want error")
	}
}

func newApp(t *testing.T, env map[string]string) *vukem.App {
	t.Helper()

	app, err := vukem.New(
		vukem.WithSource(config.MapSource(env)),
		vukem.WithLogger(slog.New(slog.DiscardHandler)),
		vukem.WithStdout(io.Discard),
	)
	if err != nil {
		t.Fatal(err)
	}

	return app
}

func TestServe_AddsRunnerToApp(t *testing.T) {
	t.Parallel()

	app := newApp(t, map[string]string{"APP_KEY": config.GenerateKey(), "APP_ENV": "development", "PORT": "0"})

	ready := make(chan string, 1)
	if err := grpc.Serve(app, "127.0.0.1:0", rpcRouter(), grpc.ServerOptions{Ready: ready}); err != nil {
		t.Fatalf("Serve() error = %v", err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)

	go func() { done <- app.Run(ctx) }()

	addr := <-ready

	h2c := &http.Transport{Protocols: new(http.Protocols)}
	h2c.Protocols.SetUnencryptedHTTP2(true)
	t.Cleanup(h2c.CloseIdleConnections)

	client := greetv1connect.NewGreetServiceClient(&http.Client{Transport: h2c}, "http://"+addr, connect.WithGRPC())
	if _, err := client.Greet(t.Context(), authed(&greetv1.GreetRequest{Name: "Ada"})); err != nil {
		t.Fatalf("Greet() error = %v", err)
	}

	cancel()

	if err := <-done; err != nil {
		t.Errorf("App.Run() after cancel = %v, want nil", err)
	}
}

// In production a cleartext gRPC port needs the same opt-in as the main
// server's h2c (HTTP_H2C=true): otherwise it must be TLS.
func TestSecurity_A02_GRPCServerCleartextInProductionNeedsOptIn(t *testing.T) {
	t.Parallel()

	prod := map[string]string{"APP_KEY": config.GenerateKey(), "APP_ENV": "production"}

	err := grpc.Serve(newApp(t, prod), ":0", rpcRouter(), grpc.ServerOptions{})
	if !errors.Is(err, grpc.ErrCleartextInProduction) {
		t.Fatalf("Serve(production, no TLS) = %v, want ErrCleartextInProduction", err)
	}

	if err := grpc.Serve(newApp(t, prod), ":0", rpcRouter(), grpc.ServerOptions{TLS: &tls.Config{}}); err != nil {
		t.Errorf("Serve(production, TLS) = %v, want nil", err)
	}

	optIn := map[string]string{"APP_KEY": config.GenerateKey(), "APP_ENV": "production", "HTTP_H2C": "true"}
	if err := grpc.Serve(newApp(t, optIn), ":0", rpcRouter(), grpc.ServerOptions{}); err != nil {
		t.Errorf("Serve(production, HTTP_H2C=true) = %v, want nil", err)
	}

	if !strings.Contains(err.Error(), "HTTP_H2C") {
		t.Errorf("error %q does not name HTTP_H2C", err)
	}
}
