package z

import (
	"encoding/json"
	"errors"
	"testing"
	"time"
)

var errNope = errors.New("nope")

func TestModifiersEveryType(t *testing.T) {
	t.Parallel()

	ref := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	never := func() error { return errNope }

	type pair struct {
		A string `json:"a"`
	}

	tests := []struct {
		name string
		run  func() error
		want string // expected issue code; "" means success
	}{
		// String
		{"string nullable", func() error { _, err := String().Nullable().Parse(nil); return err }, ""},
		{"string optional", func() error {
			_, err := Struct[pair](Shape{"a": String().Optional()}).Parse(map[string]any{})
			return err
		}, ""},
		{"string describe+message", func() error {
			_, err := String().Describe("d").Message(CodeRequired, "need it").Parse(nil)
			return err
		}, CodeRequired},

		// Number
		{"number nullable", func() error { _, err := Int().Nullable().Parse(nil); return err }, ""},
		{"number describe message", func() error {
			_, err := Int().Describe("d").Message(CodeInvalidType, "num").Parse("x")
			return err
		}, CodeInvalidType},
		{"number refine", func() error {
			_, err := Int().Refine(func(int) error { return never() }).Parse(json.Number("1"))
			return err
		}, CodeCustom},

		// Bool
		{"bool nullable", func() error { _, err := Bool().Nullable().Parse(nil); return err }, ""},
		{"bool optional", func() error { _, err := Bool().Optional().Parse(nil); return err }, ""},
		{
			"bool refine",
			func() error { _, err := Bool().Refine(func(bool) error { return never() }).Parse(true); return err },
			CodeCustom,
		},
		{"bool message describe", func() error {
			_, err := Bool().Message(CodeInvalidType, "bool!").Describe("d").Parse(3)
			return err
		}, CodeInvalidType},

		// Time
		{"time optional", func() error { _, err := Time().Optional().Parse(nil); return err }, ""},
		{"time nullable", func() error { _, err := Time().Nullable().Parse(nil); return err }, ""},
		{
			"time default",
			func() error { v, err := Time().Default(ref).Parse(nil); return check(v.Equal(ref), err) },
			"",
		},
		{
			"time refine",
			func() error { _, err := Time().Refine(func(time.Time) error { return never() }).Parse(ref); return err },
			CodeCustom,
		},
		{"time message describe", func() error {
			_, err := Time().Message(CodeInvalidDate, "date!").Describe("d").Parse("x")
			return err
		}, CodeInvalidDate},

		// Enum
		{"enum optional", func() error { _, err := Enum("a").Optional().Parse(nil); return err }, ""},
		{"enum nullable", func() error { _, err := Enum("a").Nullable().Parse(nil); return err }, ""},
		{
			"enum refine",
			func() error { _, err := Enum("a").Refine(func(string) error { return never() }).Parse("a"); return err },
			CodeCustom,
		},
		{"enum message describe", func() error {
			_, err := Enum("a").Message(CodeInvalidEnum, "pick").Describe("d").Parse("b")
			return err
		}, CodeInvalidEnum},
		{"enum named reflect", func() error {
			type st string
			_, err := EnumOf[st]("a").Parse(st("a"))
			return err
		}, ""},

		// Slice / Map
		{"slice nullable", func() error { _, err := Slice(Int()).Nullable().Parse(nil); return err }, ""},
		{"slice default", func() error {
			v, err := Slice(Int()).Default([]int{1}).Parse(nil)
			return check(len(v.([]int)) == 1, err)
		}, ""},
		{"slice message describe", func() error {
			_, err := Slice(Int()).Min(2).Message(CodeTooSmall, "more").Describe("d").Parse([]any{})
			return err
		}, CodeTooSmall},
		{"slice optional elems", func() error { _, err := Slice(Int().Optional()).Parse([]any{""}); return err }, ""},
		{"map nullable", func() error { _, err := Map(Int()).Nullable().Parse(nil); return err }, ""},
		{
			"map optional values",
			func() error { _, err := Map(Int().Optional()).Parse(map[string]any{"a": ""}); return err },
			"",
		},

		// Struct
		{
			"struct optional",
			func() error { _, err := Struct[pair](Shape{"a": String()}).Optional().Parse(nil); return err },
			"",
		},
		{
			"struct nullable",
			func() error { _, err := Struct[pair](Shape{"a": String()}).Nullable().Parse(nil); return err },
			"",
		},
		{"struct transform describe", func() error {
			v, err := Struct[pair](
				Shape{"a": String()},
			).Transform(func(p pair) pair { p.A += "!"; return p }).
				Describe("d").
				Parse(map[string]any{"a": "x"})
			return check(v.A == "x!", err)
		}, ""},
		{"struct plain refine error", func() error {
			_, err := Struct[pair](
				Shape{"a": String()},
			).Refine(func(pair) error { return never() }).
				Parse(map[string]any{"a": "x"})
			return err
		}, CodeCustom},

		// Custom
		{"custom check", func() error { return Custom(func(any) (int, error) { return 1, nil }).Check(1) }, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := tt.run()
			if tt.want == "" {
				if err != nil {
					t.Fatalf("err = %v", err)
				}
				return
			}

			is, ok := AsIssues(err)
			if !ok || is[0].Code != tt.want {
				t.Fatalf("err = %v, want code %s", err, tt.want)
			}
		})
	}
}

func check(ok bool, err error) error {
	if err != nil {
		return err
	}

	if !ok {
		return errNope
	}

	return nil
}

func TestTagNumericKinds(t *testing.T) {
	t.Parallel()

	type nums struct {
		U  uint8   `json:"u"  validate:"required,max=200"`
		F  float32 `json:"f"`
		I8 int8    `json:"i8"`
	}

	s := For[nums]()

	got, err := s.Parse(map[string]any{"u": "200", "f": "1.5", "i8": json.Number("-5")})
	if err != nil || got.U != 200 || got.F != 1.5 || got.I8 != -5 {
		t.Fatalf("nums = %+v %v", got, err)
	}

	if _, err := s.Parse(map[string]any{"u": "300"}); err == nil {
		t.Fatal("uint8 overflow accepted")
	}
	if _, err := s.Parse(map[string]any{"u": "201"}); err == nil {
		t.Fatal("max ignored")
	}
	if _, err := s.Parse(map[string]any{"u": "1", "i8": "200"}); err == nil {
		t.Fatal("int8 overflow accepted")
	}
}
