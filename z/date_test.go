package z

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
)

func TestStringDate(t *testing.T) {
	t.Parallel()

	s := String().Date()

	for _, v := range []string{"2026-02-28", "2028-02-29", "0001-01-01"} {
		if _, err := s.Parse(v); err != nil {
			t.Errorf("Parse(%q) = %v, want nil", v, err)
		}
	}

	for _, v := range []string{"", "2026-02-30", "2027-02-29", "2026-2-3", "20260203", "2026-02-03T00:00:00Z", " 2026-02-03"} {
		_, err := s.Parse(v)

		is, ok := AsIssues(err)
		if !ok || len(is) != 1 || is[0].Code != CodeInvalidString || is[0].Key != "string.iso_date" {
			t.Errorf("Parse(%q) = %v, want one string.iso_date issue", v, err)
		}
	}

	if diff := cmp.Diff(map[string]any{"type": "string", "format": "date"}, JSONSchema(s)); diff != "" {
		t.Errorf("JSON Schema (-want +got):\n%s", diff)
	}
}

func TestStringMinDate(t *testing.T) {
	t.Parallel()

	today := time.Date(2026, time.November, 21, 15, 30, 0, 0, time.UTC)
	s := String().Date().MinDate(func() time.Time { return today })

	for _, v := range []string{"2026-11-21", "2026-11-22", "2027-01-01"} {
		if _, err := s.Parse(v); err != nil {
			t.Errorf("Parse(%q) = %v, want nil", v, err)
		}
	}

	_, err := s.Parse("2026-11-20")

	is, ok := AsIssues(err)
	if !ok || len(is) != 1 || is[0].Code != CodeTooSmall || is[0].Key != "too_small.date_inclusive" ||
		is[0].Params["min"] != "2026-11-21" {
		t.Errorf("Parse(2026-11-20) = %v, want one too_small.date_inclusive issue with min 2026-11-21", err)
	}

	// A malformed date is the Date rule's problem alone.
	_, err = s.Parse("soon")
	if is, ok := AsIssues(err); !ok || len(is) != 1 || is[0].Key != "string.iso_date" {
		t.Errorf("Parse(soon) = %v, want only the string.iso_date issue", err)
	}

	got := JSONSchema(s)
	if diff := cmp.Diff(
		map[string]any{"type": "string", "format": "date", "x-minDate": "2026-11-21"},
		got,
	); diff != "" {
		t.Errorf("JSON Schema (-want +got):\n%s", diff)
	}

	// The keyword follows the clock: it is read when the schema is exported.
	today = today.AddDate(0, 0, 1)

	if got := JSONSchema(s)["x-minDate"]; got != "2026-11-22" {
		t.Errorf("x-minDate after a day = %v, want 2026-11-22", got)
	}

	if _, err := json.Marshal(JSONSchema(s)); err != nil {
		t.Errorf("the exported schema does not marshal: %v", err)
	}
}

func TestStringKeyword(t *testing.T) {
	t.Parallel()

	s := String().Keyword("x-widget", "datepicker").Keyword("x-rank", 3)

	want := map[string]any{"type": "string", "x-widget": "datepicker", "x-rank": 3}
	if diff := cmp.Diff(want, JSONSchema(s)); diff != "" {
		t.Errorf("JSON Schema (-want +got):\n%s", diff)
	}

	if diff := cmp.Diff(map[string]any{"type": "string"}, JSONSchema(String())); diff != "" {
		t.Errorf("a schema built before Keyword changed (-want +got):\n%s", diff)
	}

	if _, err := s.Parse("anything"); err != nil {
		t.Errorf("Keyword changed validation: %v", err)
	}
}
