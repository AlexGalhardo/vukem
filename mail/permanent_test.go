package mail

import (
	"errors"
	"fmt"
	"testing"

	"github.com/AlexGalhardo/vukem/queue"
)

// A queued mail job retries only what can succeed later: a rejected request
// must read as queue.ErrPermanent, even when the handler wraps it.
func TestTransportErrorsArePermanentUnlessRetryable(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		status    int
		permanent bool
	}{
		{400, true},
		{401, true},
		{422, true},
		{429, false},
		{500, false},
		{503, false},
	} {
		for _, err := range []error{
			&ResendError{Status: tt.status},
			&MailtrapError{Status: tt.status},
		} {
			wrapped := fmt.Errorf("send welcome: %w", err)
			if got := errors.Is(wrapped, queue.ErrPermanent); got != tt.permanent {
				t.Errorf("errors.Is(%T %d, queue.ErrPermanent) = %v, want %v", err, tt.status, got, tt.permanent)
			}
		}
	}
}
