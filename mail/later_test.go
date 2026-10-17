package mail

import (
	"context"
	"errors"
	"testing"

	"github.com/google/go-cmp/cmp"

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
