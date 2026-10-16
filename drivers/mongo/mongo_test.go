package mongo

import (
	"context"
	"testing"
	"time"
)

func TestOpenErrors(t *testing.T) {
	t.Parallel()

	if _, err := Open(t.Context(), "not-a-uri"); err == nil {
		t.Error("bad URI accepted")
	}

	if _, err := Open(t.Context(), "mongodb://localhost:1"); err == nil {
		t.Error("URI without a database accepted")
	}

	// Without a server the ping fails once the context ends.
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()

	if _, err := Open(ctx, "mongodb://127.0.0.1:1/app?serverSelectionTimeoutMS=500"); err == nil {
		t.Error("Open without a server succeeded")
	}
}
