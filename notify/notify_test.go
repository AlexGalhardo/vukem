package notify_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/AlexGalhardo/vukem/database"
	"github.com/AlexGalhardo/vukem/mail"
	"github.com/AlexGalhardo/vukem/notify"
	"github.com/AlexGalhardo/vukem/queue"
	"github.com/AlexGalhardo/vukem/schema"
)

type commentReplied struct {
	PostID int64  `json:"post_id"`
	By     string `json:"by"`
}

func (commentReplied) Kind() string { return "comment.replied" }

type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

func newNotifier(t *testing.T) (*notify.Notifier, *database.DB, *clock) {
	t.Helper()

	ctx := t.Context()

	db, err := database.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = db.Close() })

	m, err := schema.NewMigrator(db, []schema.Migration{notify.Migration("1_create_notifications_table")})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := m.Up(ctx); err != nil {
		t.Fatal(err)
	}

	c := &clock{t: time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)}

	return notify.New(db, c.now), db, c
}

func TestSendListAndRead(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	nf, _, c := newNotifier(t)

	var ids []string

	for i := range 3 {
		s, err := nf.Send(ctx, "u1", commentReplied{PostID: int64(i), By: "ana"}, notify.Database)
		if err != nil {
			t.Fatal(err)
		}

		ids = append(ids, s.ID)
		c.t = c.t.Add(time.Second)
	}

	if n, err := nf.Unread(ctx, "u1"); err != nil || n != 3 {
		t.Fatalf("Unread = %d, %v, want 3", n, err)
	}

	page, next, err := nf.List(ctx, "u1", "", 2)
	if err != nil {
		t.Fatal(err)
	}

	if got := []string{page[0].ID, page[1].ID}; !cmp.Equal(got, []string{ids[2], ids[1]}) || next != ids[1] {
		t.Fatalf("List page 1 = %v next %q, want newest first", got, next)
	}

	var data commentReplied
	if err := json.Unmarshal(page[0].Data, &data); err != nil || data.PostID != 2 || page[0].Kind != "comment.replied" {
		t.Errorf("page[0] = %+v (%v)", page[0], err)
	}

	page, next, err = nf.List(ctx, "u1", next, 2)
	if err != nil || len(page) != 1 || page[0].ID != ids[0] || next != "" {
		t.Fatalf("List page 2 = %+v next %q err %v", page, next, err)
	}

	if n, err := nf.MarkRead(ctx, "u1", ids[0], ids[0]); err != nil || n != 1 {
		t.Fatalf("MarkRead = %d, %v, want 1", n, err)
	}

	page, _, _ = nf.List(ctx, "u1", "", 10)
	if page[2].ReadAt == nil || !page[2].ReadAt.Equal(c.t) || page[0].ReadAt != nil {
		t.Errorf("read_at = %v / %v", page[2].ReadAt, page[0].ReadAt)
	}

	if n, err := nf.MarkAllRead(ctx, "u1"); err != nil || n != 2 {
		t.Fatalf("MarkAllRead = %d, %v, want 2", n, err)
	}

	if n, _ := nf.Unread(ctx, "u1"); n != 0 {
		t.Errorf("Unread after MarkAllRead = %d", n)
	}
}

func TestPrune(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	nf, _, c := newNotifier(t)

	if _, err := nf.Send(ctx, "u1", commentReplied{}, notify.Database); err != nil {
		t.Fatal(err)
	}

	cutoff := c.t.Add(time.Hour)
	c.t = c.t.Add(2 * time.Hour)

	if _, err := nf.Send(ctx, "u1", commentReplied{}, notify.Database); err != nil {
		t.Fatal(err)
	}

	if n, err := nf.Prune(ctx, cutoff); err != nil || n != 1 {
		t.Fatalf("Prune = %d, %v, want 1", n, err)
	}

	if n, _ := nf.Unread(ctx, "u1"); n != 1 {
		t.Errorf("Unread after Prune = %d, want 1", n)
	}
}

func TestSendValidates(t *testing.T) {
	t.Parallel()

	nf, _, _ := newNotifier(t)

	if _, err := nf.Send(t.Context(), "u1", commentReplied{}); !errors.Is(err, notify.ErrNoChannels) {
		t.Errorf("Send without channels = %v, want ErrNoChannels", err)
	}

	if _, err := nf.Send(t.Context(), "", commentReplied{}, notify.Database); err == nil {
		t.Error("Send with an empty user id succeeded")
	}
}

// A rolled-back transaction leaves no notification, no queued mail and no
// broadcast.
func TestSendTxRollback(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	nf, db, _ := newNotifier(t)

	m := mail.New(mail.NewArray(), "app@example.com", nil)

	var queued int

	m.SetEnqueueTx(func(context.Context, database.Executor, *mail.Message, ...queue.Option) error {
		queued++
		return nil
	})

	pub := &publisher{}
	boom := errors.New("boom")

	err := db.Tx(ctx, func(tx *database.Tx) error {
		_, err := nf.SendTx(ctx, tx, "u1", commentReplied{PostID: 7},
			notify.Database, notify.Mail(m, replyMail), notify.Broadcast(pub, userChannel))
		if err != nil {
			t.Fatal(err)
		}

		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("Tx = %v", err)
	}

	if n, _ := nf.Unread(ctx, "u1"); n != 0 || len(pub.events) != 0 {
		t.Errorf("after rollback: unread %d, broadcasts %v", n, pub.events)
	}

	err = db.Tx(ctx, func(tx *database.Tx) error {
		_, err := nf.SendTx(ctx, tx, "u1", commentReplied{PostID: 7},
			notify.Database, notify.Mail(m, replyMail), notify.Broadcast(pub, userChannel))

		return err
	})
	if err != nil {
		t.Fatal(err)
	}

	if n, _ := nf.Unread(ctx, "u1"); n != 1 || queued != 2 {
		t.Errorf(
			"after commit: unread %d, enqueue calls %d, want 1, 2 (the database queue drops the rolled-back insert)",
			n,
			queued,
		)
	}

	if want := []string{"private-user.u1 comment.replied"}; !cmp.Equal(pub.events, want) {
		t.Errorf("broadcasts = %v, want %v", pub.events, want)
	}
}

func TestMailSkipsWhenBuildReturnsNil(t *testing.T) {
	t.Parallel()

	nf, _, _ := newNotifier(t)
	m := mail.New(mail.NewArray(), "app@example.com", nil)

	skip := func(context.Context, string, notify.Notification) (mail.Mailable, error) { return nil, nil }

	if _, err := nf.Send(t.Context(), "u1", commentReplied{}, notify.Mail(m, skip)); err != nil {
		t.Errorf("Send with a skipped mail = %v", err)
	}
}

type publisher struct{ events []string }

func (p *publisher) Publish(_ context.Context, channel, event string, data any) error {
	if _, ok := data.(notify.Stored); !ok {
		return errors.New("data is not a notify.Stored")
	}

	p.events = append(p.events, channel+" "+event)

	return nil
}

func userChannel(userID string) string { return "private-user." + userID }

type replyMailable struct{}

func (replyMailable) Envelope() mail.Envelope {
	return mail.Envelope{To: []string{"ana@example.com"}, Subject: "New reply"}
}

func (replyMailable) Content() mail.Content { return mail.HTML("<p>New reply</p>", "New reply") }

func replyMail(context.Context, string, notify.Notification) (mail.Mailable, error) {
	return replyMailable{}, nil
}
