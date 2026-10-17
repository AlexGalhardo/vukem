package grpc_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"

	"connectrpc.com/connect"

	"github.com/AlexGalhardo/vukem/contrib/grpc"
	greetv1 "github.com/AlexGalhardo/vukem/contrib/grpc/testdata/gen/greet/v1"
	"github.com/AlexGalhardo/vukem/contrib/grpc/testdata/gen/greet/v1/greetv1connect"
	"github.com/AlexGalhardo/vukem/errs"
	"github.com/AlexGalhardo/vukem/router"
	"github.com/AlexGalhardo/vukem/z"
)

type exampleGreeter struct {
	greetv1connect.UnimplementedGreetServiceHandler
}

func (exampleGreeter) Greet(
	_ context.Context,
	req *connect.Request[greetv1.GreetRequest],
) (*connect.Response[greetv1.GreetResponse], error) {
	if req.Msg.GetName() == "ghost" {
		return nil, errs.ErrNotFound // reaches the client as NotFound
	}

	return connect.NewResponse(&greetv1.GreetResponse{Greeting: "Hello, " + req.Msg.GetName()}), nil
}

// A connect-go service mounted on the Vukem router answers gRPC, gRPC-Web
// and Connect on the same port. Rules validate requests before the method
// runs, and Vukem errors become RPC codes.
func Example() {
	opts := grpc.HandlerOptions(grpc.Options{
		Log: slog.New(slog.DiscardHandler),
		Rules: []grpc.Rule{
			grpc.Validate(z.Struct[greetv1.GreetRequest](z.Shape{"name": z.String().Min(1)})),
		},
	})

	r := router.New(nil)
	path, h := greetv1connect.NewGreetServiceHandler(exampleGreeter{}, opts...)
	grpc.Mount(r, path, h) // .Use(grpc.Required(apiGuard)) to require auth

	srv := httptest.NewServer(r)
	defer srv.Close()

	client := greetv1connect.NewGreetServiceClient(http.DefaultClient, srv.URL)
	ctx := context.Background()

	resp, err := client.Greet(ctx, connect.NewRequest(&greetv1.GreetRequest{Name: "Ada"}))
	if err != nil {
		panic(err)
	}

	fmt.Println(resp.Msg.GetGreeting())

	_, err = client.Greet(ctx, connect.NewRequest(&greetv1.GreetRequest{Name: "ghost"}))
	fmt.Println(connect.CodeOf(err))

	_, err = client.Greet(ctx, connect.NewRequest(&greetv1.GreetRequest{}))
	fmt.Println(connect.CodeOf(err))
	// Output:
	// Hello, Ada
	// not_found
	// invalid_argument
}

// Error never leaks the text of unexpected errors to clients.
func ExampleError() {
	fmt.Println(grpc.Error(errs.New(http.StatusTooManyRequests, "slow down")))
	fmt.Println(grpc.Error(errors.New("query failed: password=hunter2")))
	// Output:
	// resource_exhausted: slow down
	// internal: internal error
}

func ExampleCodeOf() {
	fmt.Println(grpc.CodeOf(http.StatusUnauthorized), grpc.CodeOf(http.StatusConflict))
	// Output: unauthenticated already_exists
}
