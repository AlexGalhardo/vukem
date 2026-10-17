package grpctest_test

import (
	"fmt"

	"github.com/AlexGalhardo/vukem/contrib/grpc/grpctest"
)

// A table-driven test runs the same calls once per protocol:
//
//	func TestGreet(t *testing.T) {
//		srv := grpctest.NewServer(t, handler) // TLS + HTTP/2, closed with the test
//		for _, p := range grpctest.Protocols() {
//			t.Run(p.Name, func(t *testing.T) {
//				client := greetv1connect.NewGreetServiceClient(srv.Client, srv.URL, p.Options...)
//				resp, err := client.Greet(t.Context(), connect.NewRequest(&greetv1.GreetRequest{Name: "Ada"}))
//				...
//			})
//		}
//	}
func ExampleProtocols() {
	for _, p := range grpctest.Protocols() {
		fmt.Println(p.Name)
	}
	// Output:
	// grpc
	// grpc-web
	// connect
	// connect+json
}
