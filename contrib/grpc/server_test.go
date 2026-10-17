package grpc_test

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"log/slog"
	"maps"
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

func mustServer(t *testing.T, addr string, opts grpc.ServerOptions) vukem.Runner {
	t.Helper()

	r, err := grpc.Server(&vukem.Config{Env: config.Development}, addr, rpcRouter(), opts)
	if err != nil {
		t.Fatalf("Server() error = %v", err)
	}

	return r
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
			addr, stop := startServer(
				t,
				mustServer(t, "127.0.0.1:0", grpc.ServerOptions{TLS: tt.tls, Ready: ready}),
				ready,
			)

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

	err := mustServer(t, "127.0.0.1:-1", grpc.ServerOptions{}).Run(t.Context())
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

// In production a cleartext gRPC port needs its own opt-in
// (GRPC_INSECURE_CLEARTEXT=true or ServerOptions.InsecureCleartext),
// independent of the main server's HTTP_H2C: otherwise it must be TLS.
func TestSecurity_A02_GRPCServerCleartextInProductionNeedsOptIn(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		env     map[string]string
		opts    grpc.ServerOptions
		wantErr error
	}{
		{"production, no TLS", map[string]string{}, grpc.ServerOptions{}, grpc.ErrCleartextInProduction},
		{"production, TLS", map[string]string{}, grpc.ServerOptions{TLS: &tls.Config{}}, nil},
		{
			"production, HTTP_H2C=true does not open the gRPC port",
			map[string]string{"HTTP_H2C": "true"},
			grpc.ServerOptions{},
			grpc.ErrCleartextInProduction,
		},
		{
			"production, GRPC_INSECURE_CLEARTEXT=true",
			map[string]string{"GRPC_INSECURE_CLEARTEXT": "true"},
			grpc.ServerOptions{},
			nil,
		},
		{
			"production, GRPC_INSECURE_CLEARTEXT=false",
			map[string]string{"GRPC_INSECURE_CLEARTEXT": "false"},
			grpc.ServerOptions{},
			grpc.ErrCleartextInProduction,
		},
		{
			"production, InsecureCleartext option",
			map[string]string{},
			grpc.ServerOptions{InsecureCleartext: true},
			nil,
		},
		{
			"production, invalid GRPC_INSECURE_CLEARTEXT",
			map[string]string{"GRPC_INSECURE_CLEARTEXT": "yes"},
			grpc.ServerOptions{},
			grpc.ErrInvalidInsecureCleartext,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			env := map[string]string{"APP_KEY": config.GenerateKey(), "APP_ENV": "production"}
			maps.Copy(env, tt.env)

			if err := grpc.Serve(newApp(t, env), ":0", rpcRouter(), tt.opts); !errors.Is(err, tt.wantErr) {
				t.Errorf("Serve(%s) = %v, want %v", tt.name, err, tt.wantErr)
			}
		})
	}

	err := grpc.Serve(
		newApp(t, map[string]string{"APP_KEY": config.GenerateKey(), "APP_ENV": "production"}),
		":0",
		rpcRouter(),
		grpc.ServerOptions{},
	)
	if err == nil || !strings.Contains(err.Error(), "GRPC_INSECURE_CLEARTEXT") {
		t.Errorf("Serve(production, no TLS) error %v does not name GRPC_INSECURE_CLEARTEXT", err)
	}

	// Server applies the same rule; a missing config counts as production
	// and HTTP_H2C does not count as the gRPC opt-in.
	for name, cfg := range map[string]*vukem.Config{
		"production":            {Env: config.Production},
		"production + HTTP_H2C": {Env: config.Production, H2C: true},
		"nil config":            nil,
	} {
		if _, err := grpc.Server(
			cfg,
			":0",
			rpcRouter(),
			grpc.ServerOptions{},
		); !errors.Is(
			err,
			grpc.ErrCleartextInProduction,
		) {
			t.Errorf("Server(%s, no TLS) = %v, want ErrCleartextInProduction", name, err)
		}
	}

	prod := &vukem.Config{Env: config.Production}
	if _, err := grpc.Server(prod, ":0", rpcRouter(), grpc.ServerOptions{InsecureCleartext: true}); err != nil {
		t.Errorf("Server(production, InsecureCleartext) = %v, want nil", err)
	}
}
