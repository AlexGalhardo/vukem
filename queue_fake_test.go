package vukem

import (
	"context"
	"errors"
	"testing"

	"github.com/AlexGalhardo/vukem/events"
	"github.com/AlexGalhardo/vukem/mail"
	"github.com/AlexGalhardo/vukem/queue"
)

type orderShipped struct {
	OrderID int64 `json:"order_id"`
}

type plainMail struct{}

func (plainMail) Envelope() mail.Envelope {
	return mail.Envelope{To: []string{"ana@example.com"}, Subject: "Shipped"}
}
func (plainMail) Content() mail.Content { return mail.Text("Your order is on its way.") }

func TestQueuedListenersMailAndFake(t *testing.T) {
	t.Parallel()

	var notified []int64

	fake := queue.NewFake()
	app := newTestApp(t,
		WithSource(testSource(map[string]string{"MAIL_MAILER": "array"})),
		WithQueueDriver(fake),
		WithJobs(queue.Handle(func(context.Context, pingJob) error { return nil })),
		WithListeners(events.OnQueued("notify_customer", func(_ context.Context, e orderShipped) error {
			notified = append(notified, e.OrderID)
			return nil
		})),
	)

	ctx := t.Context()

	if err := app.Events.Dispatch(ctx, orderShipped{OrderID: 42}); err != nil {
		t.Fatal(err)
	}

	if err := app.MailLater(ctx, plainMail{}); err != nil {
		t.Fatal(err)
	}

	q, err := app.Queue()
	if err != nil {
		t.Fatal(err)
	}

	_ = q.Dispatch(ctx, pingJob{N: 3})

	// Nothing ran yet: the fake only recorded the jobs.
	if len(notified) != 0 || len(fake.Messages()) != 3 {
		t.Fatalf("notified=%v messages=%d", notified, len(fake.Messages()))
	}

	if got := queue.Dispatched[pingJob](fake); len(got) != 1 || got[0].N != 3 {
		t.Fatalf("Dispatched[pingJob] = %v", got)
	}

	if err := q.Work(ctx, queue.WorkOptions{StopWhenEmpty: true}); err != nil {
		t.Fatal(err)
	}

	m, _ := app.Mailer()
	sent := m.Transport().(*mail.Array).Sent()

	if len(notified) != 1 || notified[0] != 42 || len(sent) != 1 || sent[0].Subject != "Shipped" {
		t.Fatalf("after work: notified=%v sent=%+v", notified, sent)
	}
}

func TestQueuedListenerWithoutQueue(t *testing.T) {
	t.Parallel()

	bus := events.New(discardLogger(), 0)
	bus.Register(events.OnQueued("x", func(context.Context, orderShipped) error { return nil }))

	if err := bus.Dispatch(t.Context(), orderShipped{}); !errors.Is(err, events.ErrNoQueue) {
		t.Fatalf("dispatch without queue = %v", err)
	}
}
