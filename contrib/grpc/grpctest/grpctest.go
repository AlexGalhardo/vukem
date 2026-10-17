// Package grpctest runs an app's RPC services in process for tests: an
// HTTP/2 test server (TLS or h2c) around the app handler and the client
// options of each protocol, so one table-driven test can call a service
// over gRPC, gRPC-Web and Connect.
//
//	srv := grpctest.NewServer(t, handler)
//	for _, p := range grpctest.Protocols() {
//		client := greetv1connect.NewGreetServiceClient(srv.Client, srv.URL, p.Options...)
//		...
//	}
//
// See https://github.com/AlexGalhardo/vukem/blob/main/docs/api.md.
package grpctest

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect"
)

// Server is an HTTP/2 test server.
type Server struct {
	// URL is the base URL to pass to generated clients.
	URL string
	// Client speaks HTTP/2 to the server (and trusts its certificate).
	Client *http.Client
}

// NewServer serves h over TLS with HTTP/2, as production does; the server
// closes when the test ends.
func NewServer(tb testing.TB, h http.Handler) *Server {
	tb.Helper()

	srv := httptest.NewUnstartedServer(h)
	srv.EnableHTTP2 = true
	srv.StartTLS()
	tb.Cleanup(srv.Close)

	return &Server{URL: srv.URL, Client: srv.Client()}
}

// NewH2CServer serves h over HTTP/2 without TLS (prior knowledge), as
// `vukem serve` does in development.
func NewH2CServer(tb testing.TB, h http.Handler) *Server {
	tb.Helper()

	srv := httptest.NewUnstartedServer(h)
	srv.Config.Protocols = new(http.Protocols)
	srv.Config.Protocols.SetHTTP1(true)
	srv.Config.Protocols.SetUnencryptedHTTP2(true)
	srv.Start()
	tb.Cleanup(srv.Close)

	transport := &http.Transport{Protocols: new(http.Protocols)}
	transport.Protocols.SetUnencryptedHTTP2(true)
	tb.Cleanup(transport.CloseIdleConnections)

	return &Server{URL: srv.URL, Client: &http.Client{Transport: transport}}
}

// Protocol names a wire protocol and the client options selecting it.
type Protocol struct {
	Name    string
	Options []connect.ClientOption
}

// Protocols returns gRPC, gRPC-Web, Connect (binary) and Connect (JSON).
func Protocols() []Protocol {
	return []Protocol{
		{Name: "grpc", Options: []connect.ClientOption{connect.WithGRPC()}},
		{Name: "grpc-web", Options: []connect.ClientOption{connect.WithGRPCWeb()}},
		{Name: "connect", Options: nil},
		{Name: "connect+json", Options: []connect.ClientOption{connect.WithProtoJSON()}},
	}
}
