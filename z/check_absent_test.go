package z

import (
	"testing"
)

// A Go value has no "absent" state, except the one its JSON form gives it:
// a field tagged omitzero (or omitempty) that holds its zero value is left
// out, so Check reports it as missing with the required code, the same as
// Parse given a map without the key.
func TestCheckSeesAnOmittedZeroFieldAsAbsent(t *testing.T) {
	t.Parallel()

	type profile struct {
		Name string `json:"name,omitzero" validate:"required,min=3"`
		Bio  string `json:"bio"           validate:"required,min=3"`
	}

	err := For[profile]().Check(profile{})

	is, _ := AsIssues(err)
	got := map[string]string{}

	for _, i := range is {
		if _, seen := got[i.Path]; !seen {
			got[i.Path] = i.Code
		}
	}

	if got["name"] != CodeRequired {
		t.Errorf("Check(profile{}) name code = %q, want %q (omitzero leaves it out)", got["name"], CodeRequired)
	}

	if got["bio"] != CodeTooSmall {
		t.Errorf(
			"Check(profile{}) bio code = %q, want %q (an empty value, not an absent one)",
			got["bio"],
			CodeTooSmall,
		)
	}

	if _, err := For[profile]().Parse(map[string]any{"bio": "abc"}); err == nil {
		t.Error("Parse without name = nil error, want required")
	}
}
