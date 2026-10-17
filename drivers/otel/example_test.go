package otel_test

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/AlexGalhardo/vukem"
	"github.com/AlexGalhardo/vukem/drivers/otel"
	"github.com/AlexGalhardo/vukem/router"
	"github.com/AlexGalhardo/vukem/web"
)

// The provider reads OTEL_EXPORTER_OTLP_ENDPOINT (traces and metrics are
// off without it), traces and measures every request and flushes both when
// the app shuts down.
func Example() {
	app, err := vukem.New(vukem.WithProviders(otel.New(otel.Config{})))
	if err != nil {
		log.Fatal(err)
	}

	if err := app.Run(context.Background()); err != nil {
		log.Fatal(err)
	}
}

// With a TracerProvider of your own, install the middleware on the router.
// Spans are named after the matched route pattern, never the raw path.
func ExampleMiddleware() {
	sr := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr))

	r := router.New(nil)
	r.UseServer(otel.Middleware(tp))
	r.Get("/posts/{slug}", func(c *web.Ctx) error { return c.String(http.StatusOK, "post") })

	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/posts/hello-world", http.NoBody))

	for _, s := range sr.Ended() {
		fmt.Println(s.Name())
	}
	// Output: GET /posts/{slug}
}
