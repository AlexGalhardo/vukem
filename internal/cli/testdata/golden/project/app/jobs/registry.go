// Package jobs holds the queued jobs.
package jobs

import (
	vukem "github.com/AlexGalhardo/vukem"
	"github.com/AlexGalhardo/vukem/queue"
)

// All lists the job handlers, built from app so they reach its database,
// clock and mailer; vukem make:job appends here.
func All(app *vukem.App) []queue.Registration {
	return []queue.Registration{
		queue.Handle(HandleSendInvoice(app)),
	}
}
