package vukem

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/AlexGalhardo/vukem/database"
	"github.com/AlexGalhardo/vukem/internal/testutil"
	"github.com/AlexGalhardo/vukem/mail"
	"github.com/AlexGalhardo/vukem/queue"
)

type hookLog struct {
	mu   sync.Mutex
	name string
	log  []string
}

func (h *hookLog) add(s string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.log = append(h.log, h.name+" "+s)
}

func (h *hookLog) ObserveQuery(_ context.Context, q database.Query) { h.add("query " + q.SQL) }

func (h *hookLog) ObserveJob(_ context.Context, e queue.JobEvent) {
	h.add("job " + e.Kind + " " + string(e.Status))
}

type hookTransport struct {
	next mail.Transport
	log  *hookLog
}

func (t hookTransport) Send(ctx context.Context, msg *mail.Message) error {
	t.log.add("mail " + msg.Subject)
	return t.next.Send(ctx, msg)
}

func instrumented(t *testing.T, logs ...*hookLog) *App {
	t.Helper()

	app := newTestApp(t,
		WithSource(testSource(map[string]string{
			"DATABASE_URL": "file:" + filepath.Join(testutil.TempDir(t), "i.db"),
			"MAIL_MAILER":  "array",
		})),
		WithJobs(queue.Handle(func(context.Context, pingJob) error { return nil })),
	)
	t.Cleanup(func() { _ = app.Shutdown(context.Background()) })

	for _, l := range logs {
		err := app.Instrument(Instrumentation{
			Queries: l,
			Jobs:    l,
			Mail:    func(next mail.Transport) mail.Transport { return hookTransport{next: next, log: l} },
		})
		if err != nil {
			t.Fatalf("Instrument() = %v", err)
		}
	}

	return app
}

func TestInstrumentReachesDBQueueAndMailer(t *testing.T) {
	t.Parallel()

	a, b := &hookLog{name: "a"}, &hookLog{name: "b"}
	app := instrumented(t, a, b)

	db, err := app.DB()
	if err != nil {
		t.Fatal(err)
	}

	if _, err := db.ExecContext(t.Context(), "SELECT 1"); err != nil {
		t.Fatal(err)
	}

	q, err := app.Queue()
	if err != nil {
		t.Fatal(err)
	}

	if err := q.Dispatch(t.Context(), pingJob{N: 1}); err != nil {
		t.Fatal(err)
	}

	m, err := app.Mailer()
	if err != nil {
		t.Fatal(err)
	}

	if err := m.Send(t.Context(), plainMail{}); err != nil {
		t.Fatal(err)
	}

	want := map[string][]string{
		"a": {"a query SELECT 1", "a job ping dispatched", "a job ping processed", "a mail Shipped"},
		"b": {"b query SELECT 1", "b job ping dispatched", "b job ping processed", "b mail Shipped"},
	}
	got := map[string][]string{"a": a.log, "b": b.log}

	if diff := cmp.Diff(want, got); diff != "" {
		t.Fatalf("hooks (-want +got):\n%s", diff)
	}

	// The last wrapper registered is the outermost transport.
	arr, ok := m.Transport().(hookTransport).next.(hookTransport).next.(*mail.Array)
	if !ok || len(arr.Sent()) != 1 {
		t.Fatalf("Mailer().Transport() = %T, want hooks around *mail.Array", m.Transport())
	}
}

func TestInstrumentAfterUseIsAnError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		use  func(*App) error
		in   func(*hookLog) Instrumentation
	}{
		{
			name: "DB",
			use:  func(a *App) error { _, err := a.DB(); return err },
			in:   func(l *hookLog) Instrumentation { return Instrumentation{Queries: l} },
		},
		{
			name: "Queue",
			use:  func(a *App) error { _, err := a.Queue(); return err },
			in:   func(l *hookLog) Instrumentation { return Instrumentation{Jobs: l} },
		},
		{
			name: "Mailer",
			use:  func(a *App) error { _, err := a.Mailer(); return err },
			in: func(*hookLog) Instrumentation {
				return Instrumentation{Mail: func(t mail.Transport) mail.Transport { return t }}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			app := instrumented(t)
			if err := tt.use(app); err != nil {
				t.Fatal(err)
			}

			if err := app.Instrument(tt.in(&hookLog{})); !errors.Is(err, ErrInstrumentTooLate) {
				t.Fatalf("Instrument() after %s() = %v, want ErrInstrumentTooLate", tt.name, err)
			}

			// Hooks for services not built yet are still accepted.
			if err := app.Instrument(Instrumentation{}); err != nil {
				t.Fatalf("Instrument(empty) = %v", err)
			}
		})
	}
}
