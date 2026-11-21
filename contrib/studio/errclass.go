package studio

import (
	"context"
	"errors"
	"strings"

	"go.mongodb.org/mongo-driver/v2/mongo"
)

// errorClass names a failure without the text of the error. Drivers quote the
// offending data ("E11000 dup key: { email: ... }"), so a write failure is
// logged by class only: the audit promises that values never appear.
func errorClass(err error) string {
	var state interface{ SQLState() string }

	switch {
	case err == nil:
		return ""
	case errors.Is(err, ErrNotFound):
		return "not_found"
	case errors.Is(err, errInput):
		return "invalid_input"
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	case mongo.IsDuplicateKeyError(err):
		return "duplicate_key"
	case errors.As(err, &state):
		return "sqlstate_" + state.SQLState()
	}

	if _, ok := errors.AsType[*queryError](err); ok {
		return "query_failed"
	}

	if strings.HasPrefix(err.Error(), studioPrefix) {
		return "refused"
	}

	return "failed"
}
