package listeners

import "github.com/AlexGalhardo/vukem/events"

// All lists the event listeners; vukem make:listener appends here.
func All() []events.Registration {
	return []events.Registration{
		events.OnAsync(NotifyCustomer),
		events.OnQueued("update_stock", UpdateStock),
	}
}
