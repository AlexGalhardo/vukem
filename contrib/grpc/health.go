package grpc

import (
	"connectrpc.com/connect"
	"connectrpc.com/grpchealth"
	"connectrpc.com/grpcreflect"

	"github.com/AlexGalhardo/vukem"
	"github.com/AlexGalhardo/vukem/router"
)

// ReflectionInProductionEnv opts in to server reflection in production.
const ReflectionInProductionEnv = "GRPC_REFLECTION_IN_PRODUCTION"

// Health mounts the standard grpc.health.v1.Health service, so load
// balancers, Kubernetes probes and grpc-health-probe can check the app.
// grpchealth.NewStaticChecker(names...) reports the listed services as
// serving; implement grpchealth.Checker to report real state.
func Health(r Mounter, checker grpchealth.Checker, opts ...connect.HandlerOption) *router.Route {
	path, h := grpchealth.NewHandler(checker, opts...)
	return Mount(r, path, h)
}

// Reflection mounts the gRPC server reflection service (v1 and v1alpha)
// for the named services (e.g. greetv1connect.GreetServiceName), so tools
// like grpcurl and buf curl can discover them. Reflection publishes the
// whole API surface, so in production it registers nothing and returns
// false unless GRPC_REFLECTION_IN_PRODUCTION=true.
func Reflection(app *vukem.App, r Mounter, services ...string) bool {
	if app.Config.IsProduction() && app.Source.Get(ReflectionInProductionEnv) != "true" {
		return false
	}

	reflector := grpcreflect.NewStaticReflector(services...)
	path, h := grpcreflect.NewHandlerV1(reflector)
	Mount(r, path, h)

	path, h = grpcreflect.NewHandlerV1Alpha(reflector)
	Mount(r, path, h)

	return true
}
