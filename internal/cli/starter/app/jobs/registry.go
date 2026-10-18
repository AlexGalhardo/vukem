// Package jobs holds the queued jobs.
package jobs

import (
	vukem "github.com/AlexGalhardo/vukem"
	"github.com/AlexGalhardo/vukem/queue"
)

// All lists the job handlers, built from the app so they reach its
// database, clock and mailer; vukem make:job appends here and names the
// parameter "app" with the first handler that needs it.
func All(_ *vukem.App) []queue.Registration {
	return []queue.Registration{}
}
