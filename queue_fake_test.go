package vukem

import (
	"context"
	"errors"
	"strings"
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

type resetMail struct{}

func (resetMail) Envelope() mail.Envelope {
	return mail.Envelope{To: []string{"ana@example.com"}, Subject: "Reset your password"}
}

func (resetMail) Content() mail.Content {
	return mail.Text("https://app.test/reset-password/s3cr3t-t0ken")
}

// Queued mail is stored encrypted: reset links and other tokens in the
// body never sit in the jobs tables (or queue:failed output) in clear text.
func TestSecurity_A04_QueuedMailPayloadIsEncrypted(t *testing.T) {
	t.Parallel()

	fake := queue.NewFake()
	app := newTestApp(t, WithSource(testSource(map[string]string{"MAIL_MAILER": "array"})), WithQueueDriver(fake))
	ctx := t.Context()

	m, err := app.Mailer()
	if err != nil {
		t.Fatal(err)
	}

	if err := m.SendLater(ctx, resetMail{}); err != nil {
		t.Fatal(err)
	}

	msgs := fake.Messages()
	if len(msgs) != 1 || msgs[0].Kind != MailJobKind {
		t.Fatalf("queued = %+v, want one %s job", msgs, MailJobKind)
	}

	for _, secret := range []string{"s3cr3t-t0ken", "ana@example.com", "Reset your password"} {
		if strings.Contains(string(msgs[0].Payload), secret) {
			t.Errorf("job payload holds %q in clear text: %s", secret, msgs[0].Payload)
		}
	}

	q, err := app.Queue()
	if err != nil {
		t.Fatal(err)
	}

	if err := q.Work(ctx, queue.WorkOptions{StopWhenEmpty: true}); err != nil {
		t.Fatal(err)
	}

	sent := m.Transport().(*mail.Array).Sent()
	if len(sent) != 1 || !strings.Contains(sent[0].Text, "s3cr3t-t0ken") {
		t.Fatalf("sent = %+v, want the decrypted reset mail", sent)
	}

	if len(fake.Failed()) != 0 {
		t.Fatalf("failed jobs = %+v", fake.Failed())
	}

	// A tampered payload fails instead of sending.
	tampered := queue.NewFake()
	other := newTestApp(t, WithSource(testSource(map[string]string{"MAIL_MAILER": "array"})), WithQueueDriver(tampered))
	oq, _ := other.Queue()

	if err := oq.DispatchKind(ctx, MailJobKind, map[string]string{"sealed": "v1.bogus"}, queue.Retries(0)); err != nil {
		t.Fatal(err)
	}

	_ = oq.Work(ctx, queue.WorkOptions{StopWhenEmpty: true})

	if len(tampered.Failed()) != 1 {
		t.Errorf("tampered mail job failed %d times, want 1", len(tampered.Failed()))
	}
}

// Mail jobs queued before payloads were encrypted still send after an
// upgrade.
func TestQueuedMailLegacyPayload(t *testing.T) {
	t.Parallel()

	fake := queue.NewFake()
	app := newTestApp(t, WithSource(testSource(map[string]string{"MAIL_MAILER": "array"})), WithQueueDriver(fake))
	ctx := t.Context()

	q, err := app.Queue()
	if err != nil {
		t.Fatal(err)
	}

	legacy := mail.Message{ID: "legacy-1", From: "a@b.co", To: []string{"ana@example.com"}, Subject: "Old", Text: "x"}
	if err := q.DispatchKind(ctx, MailJobKind, legacy); err != nil {
		t.Fatal(err)
	}

	if err := q.Work(ctx, queue.WorkOptions{StopWhenEmpty: true}); err != nil {
		t.Fatal(err)
	}

	m, _ := app.Mailer()
	if sent := m.Transport().(*mail.Array).Sent(); len(sent) != 1 || sent[0].ID != "legacy-1" {
		t.Fatalf("sent = %+v, want the legacy message", sent)
	}
}
