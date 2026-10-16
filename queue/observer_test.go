package queue_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/AlexGalhardo/vukem/queue"
)

type jobLog struct {
	mu  sync.Mutex
	got []string
}

func (l *jobLog) ObserveJob(_ context.Context, e queue.JobEvent) {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.got = append(l.got, e.Kind+":"+string(e.Status))
}

func TestObserverSeesDispatchAndAttempts(t *testing.T) {
	t.Parallel()

	fail := errors.New("boom")
	handler := queue.Handle(func(_ context.Context, j sendInvoice) error {
		if j.OrderID == 2 {
			return fail
		}

		return nil
	}, queue.Retries(0))

	t.Run("sync", func(t *testing.T) {
		t.Parallel()

		obs := &jobLog{}
		q := newQueue(t, queue.Sync{}, handler)
		q.SetObserver(obs)

		_ = q.Dispatch(t.Context(), sendInvoice{OrderID: 1})
		_ = q.Dispatch(t.Context(), sendInvoice{OrderID: 2})

		want := []string{
			"send_invoice:dispatched", "send_invoice:processed",
			"send_invoice:dispatched", "send_invoice:failed",
		}
		if diff := cmp.Diff(want, obs.got); diff != "" {
			t.Errorf("events (-want +got):\n%s", diff)
		}
	})

	t.Run("worker", func(t *testing.T) {
		t.Parallel()

		obs := &jobLog{}
		d := queue.NewMemory()
		q := newQueue(t, d, handler)
		q.SetObserver(obs)

		if err := q.Dispatch(t.Context(), sendInvoice{OrderID: 1}); err != nil {
			t.Fatal(err)
		}

		m, err := d.Pop(t.Context(), []string{"default"})
		if err != nil || m == nil {
			t.Fatalf("Pop() = %v, %v", m, err)
		}

		if err := q.Process(t.Context(), m); err != nil {
			t.Fatal(err)
		}

		if diff := cmp.Diff([]string{"send_invoice:dispatched", "send_invoice:processed"}, obs.got); diff != "" {
			t.Errorf("events (-want +got):\n%s", diff)
		}
	})
}
