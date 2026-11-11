package z

import (
	"encoding/json"
	"testing"

	"github.com/google/go-cmp/cmp"
)

type userID int64

func TestBrand(t *testing.T) {
	t.Parallel()

	s := Brand[userID](Int64().Positive())

	t.Run("parse converts", func(t *testing.T) {
		t.Parallel()

		// A form sends text, JSON a number: both reach the inner schema.
		if got, err := s.Parse("42"); err != nil || got != userID(42) {
			t.Errorf(`Parse("42") = %v, %v`, got, err)
		}

		if got, err := s.Parse(json.Number("42")); err != nil || got != userID(42) {
			t.Errorf("Parse(42) = %v, %v", got, err)
		}
	})

	t.Run("inner issues are kept", func(t *testing.T) {
		t.Parallel()

		_, err := s.Parse(json.Number("0"))
		if diff := cmp.Diff(
			[]issueRow{{"", CodeTooSmall, "too_small.positive", map[string]any{"min": 0}}},
			issueRows(t, err),
		); diff != "" {
			t.Errorf("issues (-want +got):\n%s", diff)
		}
	})

	t.Run("check", func(t *testing.T) {
		t.Parallel()

		if err := s.Check(userID(7)); err != nil {
			t.Errorf("Check(7) = %v", err)
		}

		if err := s.Check(userID(0)); err == nil {
			t.Error("Check(0) = nil, want the Positive rule")
		}
	})

	t.Run("struct field of the defined type", func(t *testing.T) {
		t.Parallel()

		type account struct {
			ID    userID  `json:"id"`
			Owner *userID `json:"owner"`
		}

		acc := Struct[account](Shape{"id": s, "owner": s.Optional()})

		got, err := acc.Parse(map[string]any{"id": json.Number("5"), "owner": json.Number("6")})
		if err != nil || got.ID != 5 || got.Owner == nil || *got.Owner != 6 {
			t.Errorf("Parse = %+v, %v", got, err)
		}
	})

	t.Run("other kinds", func(t *testing.T) {
		t.Parallel()

		type slug string

		if got, err := Brand[slug](String().Slug()).Parse("a-b"); err != nil || got != "a-b" {
			t.Errorf("string brand = %v, %v", got, err)
		}
	})

	t.Run("boot check", func(t *testing.T) {
		t.Parallel()

		mustPanic(t, "a string schema for an int type", func() { Brand[userID](String()) })
		mustPanic(t, "an int schema for a string type", func() { Brand[string](Int()) })
		mustPanic(t, "a different number kind", func() { Brand[userID](Int()) })
	})

	t.Run("json schema is the inner one", func(t *testing.T) {
		t.Parallel()

		want := JSONSchema(Int64().Positive())
		if diff := cmp.Diff(want, JSONSchema(s)); diff != "" {
			t.Errorf("schema (-want +got):\n%s", diff)
		}
	})
}
