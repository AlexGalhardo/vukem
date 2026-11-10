package principal

import (
	"context"
	"testing"
)

func TestWithFrom(t *testing.T) {
	t.Parallel()

	if _, _, ok := From(context.Background()); ok {
		t.Error("From on an empty context reported ok")
	}

	ctx := With(context.Background(), "web", "42")

	if guard, id, ok := From(ctx); guard != "web" || id != "42" || !ok {
		t.Errorf("From = %q, %q, %v", guard, id, ok)
	}

	if _, _, ok := From(With(ctx, "web", "")); ok {
		t.Error("an empty id did not clear the identity")
	}
}
