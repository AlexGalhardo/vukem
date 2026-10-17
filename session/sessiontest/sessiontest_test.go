package sessiontest_test

import (
	"testing"

	"github.com/AlexGalhardo/vukem/session"
	"github.com/AlexGalhardo/vukem/session/sessiontest"
)

// The suite is exercised by every store's own tests (session, drivers/redis);
// this run keeps the package itself covered.
func TestRunMemoryStore(t *testing.T) {
	t.Parallel()

	sessiontest.Run(t, session.NewMemoryStore())
}
