package queue_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/AlexGalhardo/vukem/database"
	"github.com/AlexGalhardo/vukem/queue"
)

func TestSyncRejectsDelay(t *testing.T) {
	t.Parallel()

	ran := 0
	q := newQueue(t, queue.Sync{}, queue.Handle(func(context.Context, sendInvoice) error { ran++; return nil }))

	err := q.Dispatch(t.Context(), sendInvoice{OrderID: 1}, queue.Delay(time.Minute))
	if !errors.Is(err, queue.ErrInlineDelay) {
		t.Fatalf("Dispatch(Delay) on Sync = %v, want ErrInlineDelay", err)
	}

	if ran != 0 {
		t.Fatalf("delayed job ran %d times on Sync, want 0", ran)
	}

	if err := q.Dispatch(t.Context(), sendInvoice{OrderID: 2}, queue.Delay(0)); err != nil || ran != 1 {
		t.Fatalf("Dispatch(Delay(0)) = %v, ran %d; want nil, 1", err, ran)
	}
}

// DispatchTx on drivers without transactional storage defers the dispatch
// until the transaction commits: nothing for rolled-back work.
func TestDispatchTxAfterCommit(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		driver func() queue.Driver
		work   bool
	}{
		{"sync", func() queue.Driver { return queue.Sync{} }, false},
		{"memory", func() queue.Driver { return queue.NewMemory() }, true},
		{"fake", func() queue.Driver { return queue.NewFake() }, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			db := openSQLite(t)
			ctx := t.Context()

			var ran []int64

			q := newQueue(t, tt.driver(), queue.Handle(func(_ context.Context, j sendInvoice) error {
				ran = append(ran, j.OrderID)
				return nil
			}))

			rollback := errors.New("rollback")

			err := db.Tx(ctx, func(tx *database.Tx) error {
				if err := q.DispatchTx(ctx, tx, sendInvoice{OrderID: 1}); err != nil {
					return err
				}

				return rollback
			})
			if !errors.Is(err, rollback) {
				t.Fatalf("Tx() = %v, want rollback", err)
			}

			err = db.Tx(ctx, func(tx *database.Tx) error {
				if err := q.DispatchTx(ctx, tx, sendInvoice{OrderID: 2}); err != nil {
					return err
				}

				if len(ran) != 0 {
					t.Error("job ran before the transaction committed")
				}

				return nil
			})
			if err != nil {
				t.Fatal(err)
			}

			// Outside a transaction there is nothing to wait for.
			if err := q.DispatchTx(ctx, db, sendInvoice{OrderID: 3}); err != nil {
				t.Fatal(err)
			}

			if tt.work {
				if err := q.Work(ctx, queue.WorkOptions{StopWhenEmpty: true}); err != nil {
					t.Fatal(err)
				}
			}

			if diff := cmp.Diff([]int64{2, 3}, ran); diff != "" {
				t.Errorf("jobs run (-want +got):\n%s", diff)
			}
		})
	}
}

func TestDispatchTxAfterCommitErrors(t *testing.T) {
	t.Parallel()

	db := openSQLite(t)
	ctx := t.Context()

	var logs bytes.Buffer

	q := queue.New(queue.Sync{}, slog.New(slog.NewTextHandler(&logs, nil)))
	if err := q.Register(
		queue.Handle(func(context.Context, sendInvoice) error { return errors.New("boom") }),
	); err != nil {
		t.Fatal(err)
	}

	// Errors known before the commit are returned inside the transaction.
	err := db.Tx(ctx, func(tx *database.Tx) error { return q.DispatchTx(ctx, tx, unknownJob{}) })
	if !errors.Is(err, queue.ErrUnknownKind) {
		t.Errorf("DispatchTx(unknown) = %v, want ErrUnknownKind", err)
	}

	err = db.Tx(ctx, func(tx *database.Tx) error {
		return q.DispatchTx(ctx, tx, sendInvoice{OrderID: 1}, queue.Delay(time.Minute))
	})
	if !errors.Is(err, queue.ErrInlineDelay) {
		t.Errorf("DispatchTx(Delay) on Sync = %v, want ErrInlineDelay", err)
	}

	// The handler's error after the commit cannot reach the caller: it is logged.
	err = db.Tx(ctx, func(tx *database.Tx) error { return q.DispatchTx(ctx, tx, sendInvoice{OrderID: 2}) })
	if err != nil {
		t.Fatalf("Tx() = %v, want nil (the commit succeeded)", err)
	}

	if !strings.Contains(logs.String(), "boom") || !strings.Contains(logs.String(), "send_invoice") {
		t.Errorf("log = %q, want the after-commit job error", logs.String())
	}

	if err := q.DispatchTx(ctx, nil, sendInvoice{OrderID: 3}); !errors.Is(err, queue.ErrNoTx) {
		t.Errorf("DispatchTx(nil executor) = %v, want ErrNoTx", err)
	}
}
