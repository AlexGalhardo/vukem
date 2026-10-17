package mail

import (
	"context"
	"errors"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/AlexGalhardo/vukem/database"
	"github.com/AlexGalhardo/vukem/queue"
)

type note struct{}

func (note) Envelope() Envelope { return Envelope{To: []string{"ana@example.com"}, Subject: "Note"} }
func (note) Content() Content   { return Text("hello") }

func TestSendLater(t *testing.T) {
	t.Parallel()

	arr := NewArray()
	m := New(arr, "App <app@example.com>", nil)

	if err := m.SendLater(t.Context(), note{}); !errors.Is(err, ErrNoQueue) {
		t.Fatalf("SendLater() without a queue = %v, want ErrNoQueue", err)
	}

	var (
		queued []*Message
		nopts  int
	)

	m.SetEnqueue(func(_ context.Context, msg *Message, opts ...queue.Option) error {
		queued = append(queued, msg)
		nopts = len(opts)

		return nil
	})

	if err := m.SendLater(t.Context(), note{}, queue.OnQueue("mail")); err != nil {
		t.Fatal(err)
	}

	if len(queued) != 1 || nopts != 1 || len(arr.Sent()) != 0 {
		t.Fatalf("queued %d (opts %d), sent %d; want 1 (1), 0", len(queued), nopts, len(arr.Sent()))
	}

	if diff := cmp.Diff([]string{"<ana@example.com>"}, queued[0].To); diff != "" || queued[0].Text != "hello" {
		t.Errorf("queued message is not rendered: %+v", queued[0])
	}

	if err := m.SendLater(t.Context(), badMail{}); err == nil {
		t.Error("SendLater() queued a mail without recipients")
	}
}

type badMail struct{}

func (badMail) Envelope() Envelope { return Envelope{Subject: "nobody"} }
func (badMail) Content() Content   { return Text("x") }

func TestSendLaterTx(t *testing.T) {
	t.Parallel()

	m := New(NewArray(), "App <app@example.com>", nil)
	ex := database.FromSQL(nil, database.SQLite)

	if err := m.SendLaterTx(t.Context(), ex, note{}); !errors.Is(err, ErrNoQueue) {
		t.Fatalf("SendLaterTx() without a queue = %v, want ErrNoQueue", err)
	}

	var (
		gotEx  database.Executor
		queued []*Message
	)

	m.SetEnqueueTx(func(_ context.Context, e database.Executor, msg *Message, _ ...queue.Option) error {
		gotEx = e
		queued = append(queued, msg)

		return nil
	})

	if err := m.SendLaterTx(t.Context(), ex, note{}); err != nil {
		t.Fatal(err)
	}

	if len(queued) != 1 || gotEx != ex || queued[0].Text != "hello" {
		t.Fatalf("queued %d with executor %v, want the rendered message with the caller's executor", len(queued), gotEx)
	}

	if err := m.SendLaterTx(t.Context(), ex, badMail{}); err == nil || len(queued) != 1 {
		t.Error("SendLaterTx() queued a mail without recipients")
	}
}
