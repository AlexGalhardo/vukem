package z

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestStructSchema_Normalize(t *testing.T) {
	t.Parallel()

	type user struct {
		Name string `json:"name"`
		Role string `json:"role,omitempty"`
		Tag  string `json:"tag"`
	}

	s := Struct[user](Shape{
		"name": String().Trim().Min(2),
		"role": String().Default("member"),
		"tag":  String().Transform(strings.ToUpper),
	})

	t.Run("applies pre-steps, defaults and transforms", func(t *testing.T) {
		t.Parallel()

		got, err := s.Normalize(user{Name: " ab ", Tag: "go"})
		if err != nil {
			t.Fatalf("Normalize() error = %v", err)
		}

		want := user{Name: "ab", Role: "member", Tag: "GO"}
		if diff := cmp.Diff(want, got); diff != "" {
			t.Fatalf("Normalize() mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("reports issues", func(t *testing.T) {
		t.Parallel()

		if _, err := s.Normalize(user{Name: " a "}); err == nil {
			t.Fatal("Normalize() error = nil, want a too_small issue")
		}
	})

	t.Run("check does not transform", func(t *testing.T) {
		t.Parallel()

		if err := s.Check(user{Name: " ab ", Tag: "go"}); err != nil {
			t.Fatalf("Check() = %v, want nil: the trim pre-step runs before Min", err)
		}
	})
}
