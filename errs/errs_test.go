package errs

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestHTTPError(t *testing.T) {
	t.Parallel()

	cause := errors.New("db down")
	e := Wrap(http.StatusServiceUnavailable, cause)

	if !errors.Is(e, cause) {
		t.Error("errors.Is(e, cause) = false")
	}
	if got := e.Error(); got != "503 Service Unavailable: db down" {
		t.Errorf("Error() = %q", got)
	}
	if got := New(400, "bad input").Error(); got != "400 bad input" {
		t.Errorf("Error() = %q", got)
	}

	wrapped := fmt.Errorf("load post: %w", New(http.StatusNotFound, ""))
	if !errors.Is(wrapped, ErrNotFound) {
		t.Error("errors.Is(wrapped 404, ErrNotFound) = false")
	}
	if errors.Is(wrapped, ErrForbidden) {
		t.Error("errors.Is(404, ErrForbidden) = true")
	}
	if errors.Is(New(404, "post not found"), New(404, "user not found")) {
		t.Error("different messages matched")
	}

	h := ErrTooManyRequests.WithHeader("Retry-After", "30")
	if h.Headers.Get("Retry-After") != "30" || ErrTooManyRequests.Headers != nil {
		t.Error("WithHeader mutated the sentinel or lost the header")
	}
}

func TestValidationError(t *testing.T) {
	t.Parallel()

	v := NewValidation()
	if !v.Empty() {
		t.Fatal("new ValidationError not empty")
	}

	v.Add("title", "required").Add("title", "too short").Add("body", "required")

	if v.First("title") != "required" || v.First("nope") != "" {
		t.Errorf("First() wrong: %v", v.Fields)
	}
	if got := v.Error(); got != "validation failed: body: required, title: required; too short" {
		t.Errorf("Error() = %q", got)
	}

	var zero ValidationError
	zero.Add("x", "y")
	if zero.First("x") != "y" {
		t.Error("Add on zero value failed")
	}
}

func TestValidationError_Append(t *testing.T) {
	t.Parallel()

	v := NewValidation()
	v.Append("price", "locked")
	v.Append("price", "too low")

	var zero ValidationError
	zero.Append("x", "y")

	if diff := cmp.Diff(map[string][]string{"price": {"locked", "too low"}}, v.Fields); diff != "" {
		t.Errorf("Append fields (-want +got):\n%s", diff)
	}

	if got := zero.First("x"); got != "y" {
		t.Errorf("zero.First(x) = %q, want %q", got, "y")
	}
}

func TestStatus(t *testing.T) {
	t.Parallel()

	tests := []struct {
		err  error
		want int
	}{
		{ErrNotFound, 404},
		{fmt.Errorf("x: %w", ErrConflict), 409},
		{NewValidation().Add("a", "b"), 422},
		{errors.New("boom"), 500},
		{coded{}, 418},
	}
	for _, tt := range tests {
		if got := Status(tt.err); got != tt.want {
			t.Errorf("Status(%v) = %d, want %d", tt.err, got, tt.want)
		}
	}
	if !strings.Contains(ErrInternal.Error(), "500") {
		t.Error("ErrInternal message")
	}
}

type coded struct{}

func (coded) Error() string   { return "teapot" }
func (coded) HTTPStatus() int { return 418 }

var errSentinel = New(500, "sentinel")

func newHere() error { return New(500, "boom") }

func TestStack(t *testing.T) {
	t.Parallel()

	st := Stack(newHere())
	if len(st) == 0 || !strings.HasSuffix(st[0].Function, "errs.newHere") {
		t.Fatalf("Stack(New) = %v, want newHere first", st)
	}

	if got := Stack(errSentinel); got != nil {
		t.Errorf("Stack(sentinel) = %v, want nil (init stack)", got)
	}

	deep := WithStack(errors.New("db down"))
	wrapped := Wrap(500, fmt.Errorf("load user: %w", deep))

	if got := Stack(wrapped); len(got) == 0 || got[0].Line != Stack(deep)[0].Line {
		t.Errorf("Stack(wrapped) = %v, want the deepest stack", got)
	}

	if Stack(errors.New("plain")) != nil || WithStack(nil) != nil {
		t.Error("errors without stacks reported one")
	}

	if !errors.Is(wrapped, deep) || wrapped.Error() != "500 Internal Server Error: load user: db down" {
		t.Errorf("wrapped = %v", wrapped)
	}
}

func TestHTTPErrorWithExtension(t *testing.T) {
	t.Parallel()

	e := ErrTooManyRequests.WithExtension("code", "quota-exceeded").WithExtension("limit", 100)
	if diff := cmp.Diff(map[string]any{"code": "quota-exceeded", "limit": 100}, e.Extensions); diff != "" {
		t.Errorf("Extensions diff (-want +got):\n%s", diff)
	}

	if ErrTooManyRequests.Extensions != nil {
		t.Error("WithExtension mutated the sentinel")
	}

	if !errors.Is(e, ErrTooManyRequests) {
		t.Error("an error with extensions no longer matches its sentinel")
	}
}
