package z

import "testing"

// Check validates a Go value, where a missing field is already its zero
// value, so it cannot tell "absent" from "empty". Parse over the decoded map
// can.
func TestParseDistinguishesAMissingFieldFromAnEmptyOne(t *testing.T) {
	t.Parallel()

	type signup struct {
		Email string `json:"email"`
	}

	s := Struct[signup](Shape{"email": String().Email()})

	code := func(err error) string {
		t.Helper()

		is, ok := AsIssues(err)
		if !ok || len(is) != 1 {
			t.Fatalf("error = %v, want one issue", err)
		}

		return is[0].Code
	}

	_, missing := s.Parse(map[string]any{})
	_, empty := s.Parse(map[string]any{"email": ""})
	checked := s.Check(signup{})

	if got, want := code(missing), CodeRequired; got != want {
		t.Errorf("Parse(absent key) code = %v, want %v", got, want)
	}

	if got := code(empty); got == CodeRequired {
		t.Errorf("Parse(empty value) code = %v, want anything but %v", got, CodeRequired)
	}

	if got, want := code(checked), code(empty); got != want {
		t.Errorf("Check(zero value) code = %v, want the empty-value code %v", got, want)
	}
}
