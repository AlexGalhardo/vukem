package vukem

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

	"github.com/AlexGalhardo/vukem/queue"
)

type pingJob struct {
	N int `json:"n"`
}

func (pingJob) Kind() string { return "ping" }

func TestQueueFromConfig(t *testing.T) {
	t.Parallel()

	var got []int

	handle := queue.Handle(func(_ context.Context, j pingJob) error { got = append(got, j.N); return nil })

	app := newTestApp(t, WithJobs(handle))

	q, err := app.Queue()
	if err != nil {
		t.Fatal(err)
	}

	if err := q.Dispatch(t.Context(), pingJob{N: 1}); err != nil || len(got) != 1 {
		t.Fatalf("sync dispatch = %v got=%v", err, got)
	}

	if _, err := newTestApp(
		t,
		WithSource(testSource(map[string]string{"QUEUE_CONNECTION": "kafka"})),
	).Queue(); err == nil {
		t.Error("unknown connection accepted")
	}

	if _, err := newTestApp(t, WithJobs(handle, handle)).Queue(); err == nil {
		t.Error("duplicate job kind accepted")
	}

	if names := (QueueConfig{Queues: "high, default,,"}).QueueNames(); len(names) != 2 || names[0] != "high" {
		t.Errorf("QueueNames = %v", names)
	}
}

func TestMemoryQueueRunner(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		done := make(chan int, 1)
		app := newTestApp(t, WithSource(testSource(map[string]string{"QUEUE_CONNECTION": "memory"})),
			WithJobs(queue.Handle(func(_ context.Context, j pingJob) error { done <- j.N; return nil })))

		q, err := app.Queue()
		if err != nil {
			t.Fatal(err)
		}

		_ = q.Dispatch(t.Context(), pingJob{N: 7})

		ctx, cancel := context.WithCancel(t.Context())
		stopped := make(chan error)

		go func() { stopped <- app.queueRunner(QueueConfig{Queues: "default", Concurrency: 1}).Run(ctx) }()

		if n := <-done; n != 7 {
			t.Fatalf("job = %d", n)
		}

		time.Sleep(time.Second)
		cancel()

		if err := <-stopped; err != nil {
			t.Fatal(err)
		}
	})
}
