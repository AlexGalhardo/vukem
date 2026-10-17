package queuetest_test

import (
	"testing"

	"github.com/AlexGalhardo/vukem/queue"
	"github.com/AlexGalhardo/vukem/queue/queuetest"
)

// A driver runs the suite from one test function. Pending counts stored
// jobs; Advance (nil here) moves the driver's clock to test abandoned
// reservations.
func ExampleRun() {
	_ = func(t *testing.T) {
		t.Helper()

		var driver queue.Driver = queue.NewMemory()
		queuetest.Run(t, driver, queuetest.Options{})
	}
}
