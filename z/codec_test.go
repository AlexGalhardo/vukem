package z

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
)

// ttl keeps seconds on the wire and a time.Duration in the domain.
func ttl() CodecSchema[int64, time.Duration] {
	return Codec(Int64().Min(0),
		func(sec int64) (time.Duration, error) { return time.Duration(sec) * time.Second, nil },
		func(d time.Duration) (int64, error) { return int64(d / time.Second), nil },
	)
}

func TestCodec(t *testing.T) {
	t.Parallel()

	t.Run("parse decodes", func(t *testing.T) {
		t.Parallel()

		got, err := ttl().Parse(json.Number("90"))
		if err != nil || got != 90*time.Second {
			t.Errorf("Parse = %v, %v", got, err)
		}

		// A form sends text; the wire schema coerces it.
		if got, err := ttl().Parse("5"); err != nil || got != 5*time.Second {
			t.Errorf(`Parse("5") = %v, %v`, got, err)
		}
	})

	t.Run("wire issues keep their key", func(t *testing.T) {
		t.Parallel()

		_, err := ttl().Parse(json.Number("-1"))
		if diff := cmp.Diff(
			[]issueRow{{"", CodeTooSmall, "too_small.number", map[string]any{"min": int64(0)}}},
			issueRows(t, err),
		); diff != "" {
			t.Errorf("issues (-want +got):\n%s", diff)
		}
	})

	t.Run("a plain decode error is generic", func(t *testing.T) {
		t.Parallel()

		s := Codec(String(),
			func(string) (int, error) { return 0, errors.New("sql: connection refused to db-7") },
			func(int) (string, error) { return "", nil },
		)

		_, err := s.Parse("x")

		is, ok := AsIssues(err)
		if !ok || len(is) != 1 || is[0].Code != CodeCustom || is[0].Key != "invalid" {
			t.Fatalf("issues = %+v", is)
		}

		if strings.Contains(is[0].Message, "db-7") || is[0].Err == nil {
			t.Errorf("the error text leaked or was dropped: message %q, Err %v", is[0].Message, is[0].Err)
		}
	})

	t.Run("a field error is honored", func(t *testing.T) {
		t.Parallel()

		s := Codec(String(),
			func(string) (int, error) { return 0, FieldErrorKey("", "my.key", "Not a valid code.", nil) },
			func(int) (string, error) { return "", nil },
		)

		_, err := s.Parse("x")
		if diff := cmp.Diff([]issueRow{{"", CodeCustom, "my.key", nil}}, issueRows(t, err)); diff != "" {
			t.Errorf("issues (-want +got):\n%s", diff)
		}
	})

	t.Run("encode goes back and checks the wire schema", func(t *testing.T) {
		t.Parallel()

		sec, err := ttl().Encode(90 * time.Second)
		if err != nil || sec != 90 {
			t.Errorf("Encode = %d, %v", sec, err)
		}

		if _, err := ttl().Encode(-time.Minute); err == nil {
			t.Error("Encode of a negative duration passed the wire schema")
		}

		if err := ttl().Check(time.Minute); err != nil {
			t.Errorf("Check(valid) = %v", err)
		}

		if err := ttl().Check(-time.Minute); err == nil {
			t.Error("Check(invalid) = nil")
		}
	})

	t.Run("check fails when encode fails", func(t *testing.T) {
		t.Parallel()

		s := Codec(Int(), func(n int) (int, error) { return n, nil }, func(int) (int, error) { return 0, errNope })
		if err := s.Check(1); !errors.Is(err, errNope) {
			t.Errorf("Check = %v, want the encode error", err)
		}
	})

	t.Run("nil arguments", func(t *testing.T) {
		t.Parallel()

		dec := func(int) (int, error) { return 0, nil }

		mustPanic(t, "nil wire", func() { Codec[int, int](nil, dec, dec) })
		mustPanic(t, "nil decode", func() { Codec[int, int](Int(), nil, dec) })
		mustPanic(t, "nil encode", func() { Codec[int, int](Int(), dec, nil) })
	})

	t.Run("json schema is the wire schema", func(t *testing.T) {
		t.Parallel()

		want := map[string]any{"type": "integer", "minimum": int64(0), "description": "seconds"}
		if diff := cmp.Diff(want, JSONSchema(ttl().Describe("seconds"))); diff != "" {
			t.Errorf("schema (-want +got):\n%s", diff)
		}
	})

	t.Run("field and element", func(t *testing.T) {
		t.Parallel()

		type job struct {
			Timeout time.Duration   `json:"timeout"`
			Retries []time.Duration `json:"retries"`
		}

		s := Struct[job](Shape{"timeout": ttl(), "retries": Slice(ttl()).Optional()})

		got, err := s.Parse(
			map[string]any{"timeout": json.Number("2"), "retries": []any{json.Number("1"), json.Number("3")}},
		)
		if err != nil || got.Timeout != 2*time.Second || len(got.Retries) != 2 || got.Retries[1] != 3*time.Second {
			t.Errorf("Parse = %+v, %v", got, err)
		}

		_, err = s.Parse(
			map[string]any{"timeout": json.Number("2"), "retries": []any{json.Number("1"), json.Number("-3")}},
		)
		if diff := cmp.Diff(
			[]issueRow{{"retries.1", CodeTooSmall, "too_small.number", map[string]any{"min": int64(0)}}},
			issueRows(t, err),
		); diff != "" {
			t.Errorf("element issue (-want +got):\n%s", diff)
		}
	})

	t.Run("default is not decoded", func(t *testing.T) {
		t.Parallel()

		got, err := ttl().Default(time.Hour).Parse(nil)
		if err != nil || got != time.Hour {
			t.Errorf("Default = %v, %v", got, err)
		}
	})
}
