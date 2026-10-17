package grpc_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"connectrpc.com/grpchealth"
	"connectrpc.com/grpcreflect"
	"github.com/google/go-cmp/cmp"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/AlexGalhardo/vukem"
	"github.com/AlexGalhardo/vukem/auth"
	"github.com/AlexGalhardo/vukem/config"
	"github.com/AlexGalhardo/vukem/contrib/grpc"
	"github.com/AlexGalhardo/vukem/contrib/grpc/grpctest"
	greetv1 "github.com/AlexGalhardo/vukem/contrib/grpc/testdata/gen/greet/v1"
	"github.com/AlexGalhardo/vukem/contrib/grpc/testdata/gen/greet/v1/greetv1connect"
	"github.com/AlexGalhardo/vukem/errs"
	"github.com/AlexGalhardo/vukem/ratelimit"
	"github.com/AlexGalhardo/vukem/router"
	"github.com/AlexGalhardo/vukem/web"
	"github.com/AlexGalhardo/vukem/z"
)

const token = "secret-token"

// bearerGuard authenticates "Authorization: Bearer secret-token" as "ada".
type bearerGuard struct{}

func (bearerGuard) Name() string { return "api" }

func (bearerGuard) Check(c *web.Ctx) bool {
	if c.Header("Authorization") != "Bearer "+token {
		return false
	}

	auth.SetUser(c, "api", "ada")

	return true
}

type greeter struct {
	greetv1connect.UnimplementedGreetServiceHandler
}

func (greeter) Greet(
	ctx context.Context,
	req *connect.Request[greetv1.GreetRequest],
) (*connect.Response[greetv1.GreetResponse], error) {
	switch req.Msg.GetName() {
	case "boom":
		return nil, errors.New("query failed: password=hunter2")
	case "panic":
		panic("kaboom: password=hunter2")
	case "ghost":
		return nil, errs.ErrNotFound
	case "taken":
		return nil, errs.NewValidation().Add("name", "is taken")
	}

	user, err := auth.UserFrom[string](ctx, "api")
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&greetv1.GreetResponse{Greeting: "Hello, " + req.Msg.GetName() + " from " + user}), nil
}

func (greeter) GreetStream(
	_ context.Context,
	req *connect.Request[greetv1.GreetStreamRequest],
	stream *connect.ServerStream[greetv1.GreetStreamResponse],
) error {
	for i := range req.Msg.GetCount() {
		if err := stream.Send(
			&greetv1.GreetStreamResponse{Greeting: fmt.Sprintf("%d: %s", i, req.Msg.GetName())},
		); err != nil {
			return err
		}
	}

	return nil
}

type setup struct {
	env     map[string]string
	opts    grpc.Options
	mw      []router.Middleware
	routing func(r *router.Router, app *vukem.App)
}

// newHandler builds a web app (sessions, CSRF: the hardest stack for RPC
// clients) serving the greet service behind the bearer guard.
func newHandler(t *testing.T, s setup) http.Handler {
	t.Helper()

	env := map[string]string{"APP_KEY": config.GenerateKey(), "APP_ENV": "development", "APP_NAME": "Shop"}
	maps.Copy(env, s.env)

	if s.opts.Rules == nil {
		s.opts.Rules = []grpc.Rule{
			grpc.Validate(z.Struct[greetv1.GreetRequest](z.Shape{"name": z.String().Min(1)})),
		}
	}

	app, err := vukem.New(
		vukem.WithSource(config.MapSource(env)),
		vukem.WithLogger(slog.New(slog.DiscardHandler)),
		vukem.WithStdout(io.Discard),
		vukem.WithRoutes(func(r *router.Router, app *vukem.App) error {
			if err := vukem.DefaultMiddleware(app, r); err != nil {
				return err
			}

			path, h := greetv1connect.NewGreetServiceHandler(greeter{}, grpc.HandlerOptions(s.opts)...)
			grpc.Mount(r, path, h).Use(append([]router.Middleware{grpc.Required(bearerGuard{})}, s.mw...)...)

			if s.routing != nil {
				s.routing(r, app)
			}

			return nil
		}),
	)
	if err != nil {
		t.Fatal(err)
	}

	h, err := app.Handler()
	if err != nil {
		t.Fatal(err)
	}

	return h
}

func authed[T any](msg *T) *connect.Request[T] {
	req := connect.NewRequest(msg)
	req.Header().Set("Authorization", "Bearer "+token)

	return req
}

func servers(t *testing.T, h http.Handler) map[string]*grpctest.Server {
	t.Helper()

	return map[string]*grpctest.Server{"tls": grpctest.NewServer(t, h), "h2c": grpctest.NewH2CServer(t, h)}
}

func TestProtocols_UnaryAndServerStreaming(t *testing.T) {
	t.Parallel()

	h := newHandler(t, setup{})

	for name, srv := range servers(t, h) {
		for _, p := range grpctest.Protocols() {
			t.Run(name+"/"+p.Name, func(t *testing.T) {
				t.Parallel()

				client := greetv1connect.NewGreetServiceClient(srv.Client, srv.URL, p.Options...)

				res, err := client.Greet(t.Context(), authed(&greetv1.GreetRequest{Name: "Bob"}))
				if err != nil {
					t.Fatalf("Greet() error = %v", err)
				}

				if got, want := res.Msg.GetGreeting(), "Hello, Bob from ada"; got != want {
					t.Errorf("Greet() = %q, want %q", got, want)
				}

				if res.Header().Get("X-Request-ID") == "" {
					t.Error("Greet() response lacks X-Request-ID from the router middleware")
				}

				stream, err := client.GreetStream(
					t.Context(),
					authed(&greetv1.GreetStreamRequest{Name: "Bob", Count: 3}),
				)
				if err != nil {
					t.Fatalf("GreetStream() error = %v", err)
				}

				var got []string
				for stream.Receive() {
					got = append(got, stream.Msg().GetGreeting())
				}

				if err := stream.Err(); err != nil {
					t.Fatalf("GreetStream() stream error = %v", err)
				}

				if diff := cmp.Diff([]string{"0: Bob", "1: Bob", "2: Bob"}, got); diff != "" {
					t.Errorf("GreetStream() (-want +got):\n%s", diff)
				}
			})
		}
	}
}

func TestRequired_UnauthenticatedCallsGetUnauthenticated(t *testing.T) {
	t.Parallel()

	srv := grpctest.NewServer(t, newHandler(t, setup{}))

	for _, p := range grpctest.Protocols() {
		t.Run(p.Name, func(t *testing.T) {
			t.Parallel()

			client := greetv1connect.NewGreetServiceClient(srv.Client, srv.URL, p.Options...)

			_, err := client.Greet(t.Context(), connect.NewRequest(&greetv1.GreetRequest{Name: "Bob"}))
			if got := connect.CodeOf(err); got != connect.CodeUnauthenticated {
				t.Errorf("Greet() code = %v (%v), want %v", got, err, connect.CodeUnauthenticated)
			}

			stream, err := client.GreetStream(t.Context(), connect.NewRequest(&greetv1.GreetStreamRequest{Count: 1}))
			if err == nil {
				for stream.Receive() {
					t.Error("GreetStream() sent a message to an unauthenticated client")
				}

				err = stream.Err()
			}

			if got := connect.CodeOf(err); got != connect.CodeUnauthenticated {
				t.Errorf("GreetStream() code = %v (%v), want %v", got, err, connect.CodeUnauthenticated)
			}
		})
	}
}

func TestHandlerOptions_MapsErrors(t *testing.T) {
	t.Parallel()

	srv := grpctest.NewServer(t, newHandler(t, setup{}))

	tests := []struct {
		name, msg string
		want      connect.Code
		message   string
		fields    map[string]string
	}{
		{"not found", "ghost", connect.CodeNotFound, "Not Found", nil},
		{
			"validation error",
			"taken",
			connect.CodeInvalidArgument,
			"invalid request",
			map[string]string{"name": "is taken"},
		},
		{
			"z rule",
			"",
			connect.CodeInvalidArgument,
			"invalid request",
			map[string]string{"name": "must be at least 1 characters"},
		},
	}

	for _, p := range grpctest.Protocols() {
		for _, tt := range tests {
			t.Run(p.Name+"/"+tt.name, func(t *testing.T) {
				t.Parallel()

				client := greetv1connect.NewGreetServiceClient(srv.Client, srv.URL, p.Options...)

				_, err := client.Greet(t.Context(), authed(&greetv1.GreetRequest{Name: tt.msg}))

				cerr, ok := errors.AsType[*connect.Error](err)
				if !ok {
					t.Fatalf("Greet(%q) error = %v, want *connect.Error", tt.msg, err)
				}

				if cerr.Code() != tt.want || cerr.Message() != tt.message {
					t.Errorf("Greet(%q) = %v %q, want %v %q", tt.msg, cerr.Code(), cerr.Message(), tt.want, tt.message)
				}

				if tt.fields == nil {
					return
				}

				got := map[string]string{}
				for _, d := range cerr.Details() {
					v, err := d.Value()
					if err != nil {
						t.Fatal(err)
					}

					if br, ok := v.(*errdetails.BadRequest); ok {
						for _, fv := range br.GetFieldViolations() {
							got[fv.GetField()] = fv.GetDescription()
						}
					}
				}

				if len(got) == 0 {
					t.Fatalf("Greet(%q) details = %v, want a google.rpc.BadRequest", tt.msg, cerr.Details())
				}

				for field := range tt.fields {
					if got[field] == "" {
						t.Errorf("Greet(%q) violations = %v, want one for %q", tt.msg, got, field)
					}
				}
			})
		}
	}
}

func TestSecurity_A10_GRPCInternalErrorsHidden(t *testing.T) {
	t.Parallel()

	var logs syncBuffer

	srv := grpctest.NewServer(
		t,
		newHandler(t, setup{opts: grpc.Options{Log: slog.New(slog.NewTextHandler(&logs, nil))}}),
	)

	for _, p := range grpctest.Protocols() {
		for _, name := range []string{"boom", "panic"} {
			t.Run(p.Name+"/"+name, func(t *testing.T) {
				t.Parallel()

				client := greetv1connect.NewGreetServiceClient(srv.Client, srv.URL, p.Options...)

				_, err := client.Greet(t.Context(), authed(&greetv1.GreetRequest{Name: name}))

				cerr, ok := errors.AsType[*connect.Error](err)
				if !ok || cerr.Code() != connect.CodeInternal || cerr.Message() != "internal error" {
					t.Fatalf("Greet(%q) = %v, want internal error", name, err)
				}

				if strings.Contains(err.Error(), "hunter2") {
					t.Errorf("Greet(%q) leaked the cause to the client: %v", name, err)
				}
			})
		}
	}

	t.Cleanup(func() {
		if out := logs.String(); !strings.Contains(out, "hunter2") || !strings.Contains(out, "rpc panic") {
			t.Errorf("server log lacks the hidden causes:\n%s", out)
		}
	})
}

func TestSecurity_A10_GRPCMessageSizeLimited(t *testing.T) {
	t.Parallel()

	srv := grpctest.NewServer(t, newHandler(t, setup{opts: grpc.Options{MaxMessageBytes: 64}}))

	for _, p := range grpctest.Protocols() {
		t.Run(p.Name, func(t *testing.T) {
			t.Parallel()

			client := greetv1connect.NewGreetServiceClient(srv.Client, srv.URL, p.Options...)

			_, err := client.Greet(t.Context(), authed(&greetv1.GreetRequest{Name: strings.Repeat("a", 1024)}))
			if got := connect.CodeOf(err); got != connect.CodeResourceExhausted {
				t.Errorf("Greet(1 KiB) code = %v (%v), want %v", got, err, connect.CodeResourceExhausted)
			}
		})
	}
}

func TestMount_RateLimitIsResourceExhausted(t *testing.T) {
	t.Parallel()

	limiter := ratelimit.NewMemory(1, time.Minute)
	srv := grpctest.NewServer(
		t,
		newHandler(t, setup{mw: []router.Middleware{ratelimit.Middleware(limiter, ratelimit.ByIP)}}),
	)
	client := greetv1connect.NewGreetServiceClient(srv.Client, srv.URL, connect.WithGRPC())

	if _, err := client.Greet(t.Context(), authed(&greetv1.GreetRequest{Name: "Bob"})); err != nil {
		t.Fatalf("first Greet() error = %v", err)
	}

	_, err := client.Greet(t.Context(), authed(&greetv1.GreetRequest{Name: "Bob"}))
	if got := connect.CodeOf(err); got != connect.CodeResourceExhausted {
		t.Fatalf("second Greet() code = %v (%v), want %v", got, err, connect.CodeResourceExhausted)
	}

	if cerr, ok := errors.AsType[*connect.Error](err); !ok || cerr.Meta().Get("Retry-After") == "" {
		t.Errorf("second Greet() = %v, want Retry-After metadata", err)
	}
}

func TestHandlerOptions_LogsEachRPC(t *testing.T) {
	t.Parallel()

	var logs syncBuffer

	srv := grpctest.NewServer(
		t,
		newHandler(t, setup{opts: grpc.Options{Log: slog.New(slog.NewTextHandler(&logs, nil))}}),
	)
	client := greetv1connect.NewGreetServiceClient(srv.Client, srv.URL, connect.WithGRPC())

	req := authed(&greetv1.GreetRequest{Name: "ghost"})
	req.Header().Set("X-Request-ID", "req-42")

	if _, err := client.Greet(t.Context(), req); connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("Greet(ghost) = %v", err)
	}

	out := logs.String()
	for _, want := range []string{"msg=rpc", "procedure=/greet.v1.GreetService/Greet", "protocol=grpc", "code=not_found", "request_id=req-42"} {
		if !strings.Contains(out, want) {
			t.Errorf("log lacks %q:\n%s", want, out)
		}
	}
}

func TestHealth(t *testing.T) {
	t.Parallel()

	h := newHandler(t, setup{routing: func(r *router.Router, _ *vukem.App) {
		grpc.Health(r, grpchealth.NewStaticChecker(greetv1connect.GreetServiceName))
	}})
	srv := grpctest.NewServer(t, h)

	for _, p := range grpctest.Protocols() {
		t.Run(p.Name, func(t *testing.T) {
			t.Parallel()

			client := grpchealth.NewClient(srv.Client, srv.URL, p.Options...)

			res, err := client.Check(t.Context(), &grpchealth.CheckRequest{Service: greetv1connect.GreetServiceName})
			if err != nil || res.Status != grpchealth.StatusServing {
				t.Errorf("Check() = %v, %v, want serving", res, err)
			}

			_, err = client.Check(t.Context(), &grpchealth.CheckRequest{Service: "nope.v1.Nope"})
			if connect.CodeOf(err) != connect.CodeNotFound {
				t.Errorf("Check(unknown) = %v, want not_found", err)
			}
		})
	}
}

func reflectionHandler(t *testing.T, env map[string]string, registered *bool) http.Handler {
	t.Helper()

	return newHandler(t, setup{env: env, routing: func(r *router.Router, app *vukem.App) {
		*registered = grpc.Reflection(app, r, greetv1connect.GreetServiceName)
	}})
}

func TestReflection(t *testing.T) {
	t.Parallel()

	var registered bool

	srv := grpctest.NewServer(t, reflectionHandler(t, nil, &registered))
	if !registered {
		t.Fatal("Reflection() = false in development")
	}

	stream := grpcreflect.NewClient(srv.Client, srv.URL).NewStream(t.Context())
	t.Cleanup(func() { _, _ = stream.Close() })

	names, err := stream.ListServices()
	if err != nil {
		t.Fatal(err)
	}

	if diff := cmp.Diff([]protoreflect.FullName{greetv1connect.GreetServiceName}, names); diff != "" {
		t.Errorf("ListServices() (-want +got):\n%s", diff)
	}

	files, err := stream.FileContainingSymbol(greetv1connect.GreetServiceName)
	if err != nil || len(files) == 0 || files[0].GetName() != "greet/v1/greet.proto" {
		t.Errorf("FileContainingSymbol() = %v, %v", files, err)
	}
}

func TestSecurity_A02_GRPCReflectionOffInProduction(t *testing.T) {
	t.Parallel()

	var registered bool

	srv := grpctest.NewServer(t, reflectionHandler(t, map[string]string{"APP_ENV": "production"}, &registered))
	if registered {
		t.Fatal("Reflection() = true in production")
	}

	stream := grpcreflect.NewClient(srv.Client, srv.URL).NewStream(t.Context())
	t.Cleanup(func() { _, _ = stream.Close() })

	if _, err := stream.ListServices(); err == nil {
		t.Fatal("ListServices() succeeded in production")
	}

	reflectionHandler(
		t,
		map[string]string{"APP_ENV": "production", "GRPC_INSECURE_REFLECTION_IN_PRODUCTION": "true"},
		&registered,
	)
	if !registered {
		t.Error("Reflection() = false with GRPC_INSECURE_REFLECTION_IN_PRODUCTION=true")
	}
}

// syncBuffer is a bytes.Buffer safe for concurrent log writes.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.b.String()
}
