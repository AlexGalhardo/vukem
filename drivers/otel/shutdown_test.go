package otel

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// blockingExporter never finishes shutting down until its context ends,
// like an exporter stuck on an unreachable collector.
type blockingExporter struct{}

func (blockingExporter) ExportSpans(context.Context, []sdktrace.ReadOnlySpan) error { return nil }

func (blockingExporter) Shutdown(ctx context.Context) error {
	<-ctx.Done()
	return ctx.Err()
}

func TestProvider_ShutdownBoundedWithoutDeadline(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		p := &Provider{tp: sdktrace.NewTracerProvider(sdktrace.WithSyncer(blockingExporter{}))}

		start := time.Now()
		err := p.Shutdown(context.Background(), nil)

		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("Shutdown() = %v, want DeadlineExceeded", err)
		}

		if got := time.Since(start); got != shutdownTimeout {
			t.Fatalf("Shutdown() took %v, want %v", got, shutdownTimeout)
		}
	})
}
