package stores_test

import (
	"fmt"

	"github.com/AlexGalhardo/vukem/schema/stores"
)

// ExampleTables lists the tables the framework owns, so an app can see
// which names are taken before naming a table of its own.
func ExampleTables() {
	for _, t := range stores.Tables(stores.Options{}) {
		fmt.Println(t)
	}

	// Output:
	// jobs
	// failed_jobs
	// sessions
	// cache
	// webhook_deliveries
}
