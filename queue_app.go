package vukem

import (
	"context"
	"fmt"
	"strings"

	"github.com/AlexGalhardo/vukem/queue"
)

// WithJobs registers job handlers on the application queue (generated apps
// pass jobs.All(); vukem make:job appends there).
func WithJobs(regs ...queue.Registration) Option {
	return func(o *options) error { o.jobs = append(o.jobs, regs...); return nil }
}

// QueueConfig is read from QUEUE_* variables.
type QueueConfig struct {
	// Connection is "sync" (default: jobs run during Dispatch), "memory"
	// (in process, run by a worker inside serve) or "database".
	Connection string `env:"QUEUE_CONNECTION" default:"sync"`
	// Queues lists the queues workers poll, in priority order.
	Queues string `env:"QUEUE_NAMES" default:"default"`
	// Concurrency is the number of parallel workers.
	Concurrency int `env:"QUEUE_CONCURRENCY" default:"4"`
}

// QueueNames returns Queues as a list.
func (c QueueConfig) QueueNames() []string {
	var out []string

	for name := range strings.SplitSeq(c.Queues, ",") {
		if name = strings.TrimSpace(name); name != "" {
			out = append(out, name)
		}
	}

	return out
}

// Queue returns the application queue, built from QUEUE_* on first use.
func (a *App) Queue() (*queue.Queue, error) {
	a.queueOnce.Do(func() {
		cfg, err := configLoad[QueueConfig](a)
		if err != nil {
			a.queueErr = err
			return
		}

		var d queue.Driver

		switch cfg.Connection {
		case "sync":
			d = queue.Sync{}
		case "memory":
			d = queue.NewMemory()
		default:
			a.queueErr = fmt.Errorf("vukem: unknown QUEUE_CONNECTION %q (sync, memory)", cfg.Connection)
			return
		}

		q := queue.New(d, a.Log)
		if err := q.Register(a.jobs...); err != nil {
			a.queueErr = err
			return
		}

		a.queue = q
	})

	return a.queue, a.queueErr
}

// queueRunner works the in-memory queue inside the web process.
func (a *App) queueRunner(cfg QueueConfig) Runner {
	return RunnerFunc(func(ctx context.Context) error {
		q, err := a.Queue()
		if err != nil {
			return err
		}

		return q.Work(ctx, queue.WorkOptions{Queues: cfg.QueueNames(), Concurrency: cfg.Concurrency})
	})
}
