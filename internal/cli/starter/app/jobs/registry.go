// Package jobs holds the queued jobs.
package jobs

import "github.com/AlexGalhardo/vukem/queue"

// All lists the job handlers; vukem make:job appends here.
func All() []queue.Registration {
	return []queue.Registration{}
}
