package z

import (
	"encoding/json"
	"errors"
	"reflect"
	"regexp"
	"slices"
	"strings"
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
			return check(len(v) == 1, err)
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

		{"struct default", func() error {
			v, err := Struct[pair](Shape{"a": String()}).Default(pair{A: "d"}).Parse(nil)
			return check(v.A == "d", err)
		}, ""},
		{"struct message", func() error {
			_, err := Struct[pair](Shape{"a": String()}).Message(CodeInvalidType, "obj!").Parse("x")
			return err
		}, CodeInvalidType},
		{"struct message required", func() error {
			_, err := Struct[pair](Shape{"a": String()}).Message(CodeRequired, "need").Parse(nil)
			return err
		}, CodeRequired},

		// Custom
		{"custom check", func() error { return Custom(func(any) (int, error) { return 1, nil }).Check(1) }, ""},
		{"custom nullable", func() error {
			_, err := Custom(func(any) (int, error) { return 1, nil }).Nullable().Parse(nil)
			return err
		}, ""},
		{"custom default", func() error {
			v, err := Custom(func(any) (int, error) { return 1, nil }).Default(7).Parse(nil)
			return check(v == 7, err)
		}, ""},
		{"custom refine", func() error {
			_, err := Custom(
				func(any) (int, error) { return 1, nil },
			).Refine(func(int) error { return never() }).
				Parse("x")
			return err
		}, CodeCustom},
		{"custom transform", func() error {
			v, err := Custom(
				func(any) (int, error) { return 1, nil },
			).Transform(func(n int) int { return n + 1 }).
				Parse("x")
			return check(v == 2, err)
		}, ""},
		{"custom message describe", func() error {
			_, err := Custom(
				func(any) (int, error) { return 1, nil },
			).Message(CodeRequired, "need").
				Describe("d").
				Parse(nil)
			return err
		}, CodeRequired},

		// Transform on the scalar types
		{"bool transform", func() error {
			v, err := Bool().Transform(func(b bool) bool { return !b }).Parse(true)
			return check(!v, err)
		}, ""},
		{"time transform", func() error {
			v, err := Time().Transform(func(t time.Time) time.Time { return t.UTC() }).
				Parse("2026-01-01T01:00:00+01:00")
			return check(v.Location() == time.UTC && v.Hour() == 0, err)
		}, ""},
		{"enum transform", func() error {
			v, err := Enum("a", "b").Transform(func(string) string { return "b" }).Parse("a")
			return check(v == "b", err)
		}, ""},

		// JSON
		{"json default", func() error {
			v, err := JSON().Default(map[string]any{"k": 1}).Parse(nil)
			return check(v != nil, err)
		}, ""},
		{"json transform", func() error {
			v, err := JSON().Transform(func(any) any { return "t" }).Parse(map[string]any{})
			return check(v == "t", err)
		}, ""},
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

func TestStringPreprocess(t *testing.T) {
	t.Parallel()

	digits := func(s string) string {
		return strings.Map(func(r rune) rune {
			if r >= '0' && r <= '9' {
				return r
			}

			return -1
		}, s)
	}
	e164 := regexp.MustCompile(`^\d{10,11}$`)

	tests := []struct {
		name    string
		schema  StringSchema
		in      string
		want    string
		wantErr bool
	}{
		{
			"checks see normalized value",
			String().Preprocess(digits).Regex(e164),
			"(11) 98765-4321",
			"11987654321",
			false,
		},
		{"transform does not feed checks", String().Transform(digits).Regex(e164), "(11) 98765-4321", "", true},
		{"declaration order", String().Trim().Preprocess(func(s string) string { return "+" + s }), " 1 ", "+1", false},
		{"before transform", String().Transform(strings.ToUpper).Preprocess(digits), "a1b2", "12", false},
		{"failing check", String().Preprocess(digits).Min(3), "a1", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := tt.schema.Parse(tt.in)
			if (err != nil) != tt.wantErr {
				t.Fatalf("Parse(%q) error = %v, wantErr %v", tt.in, err, tt.wantErr)
			}

			if got != tt.want {
				t.Errorf("Parse(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestStringPreprocess_RevalidatesOutput(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		fn   func(string) string
	}{
		{"control char", func(s string) string { return s + "\x00" }},
		{"invalid utf8", func(s string) string { return s + "\xff" }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := String().Preprocess(tt.fn).Parse("ok")

			is, ok := AsIssues(err)
			if !ok || len(is) != 1 || is[0].Key != "string.utf8" {
				t.Errorf("Parse(%q) error = %v, want one string.utf8 issue", "ok", err)
			}
		})
	}
}

func TestStringPreprocess_NilPanics(t *testing.T) {
	t.Parallel()

	defer func() {
		if r := recover(); r == nil {
			t.Error("Preprocess(nil) did not panic")
		}
	}()

	String().Preprocess(nil)
}

// TestModifierMatrix keeps every public schema on the same set of modifiers:
// a type added later that forgets one fails here, not in a user's code.
func TestModifierMatrix(t *testing.T) {
	t.Parallel()

	type pair struct {
		A string `json:"a"`
	}

	all := []string{"Optional", "Nullable", "Default", "Refine", "Transform", "Message", "Describe", "Parse", "Check"}

	tests := []struct {
		name    string
		schema  any
		without []string // modifiers that make no sense for the type
	}{
		{"String", String(), nil},
		{"Number", Int(), nil},
		{"Bool", Bool(), nil},
		{"Time", Time(), nil},
		{"Enum", Enum("a"), nil},
		{"Slice", Slice(String()), nil},
		{"Map", Map(String()), nil},
		{"Struct", Struct[pair](Shape{"a": String()}), nil},
		{"Custom", Custom(func(any) (int, error) { return 0, nil }), nil},
		{"JSON", JSON(), nil},
		{"Literal", Literal("a"), nil},
		{"Union", Union[any](Int(), String()), nil},
		{"DiscriminatedUnion", paymentSchema(), nil},
		{"Tuple", pointSchema(), nil},
		{"Lazy", Lazy(func() Schema[string] { return String() }), nil},
		// An upload has no default and no useful null or rewritten form.
		{"File", File(), []string{"Nullable", "Default", "Transform"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rt := reflect.TypeOf(tt.schema)

			for _, m := range all {
				_, has := rt.MethodByName(m)
				if want := !slices.Contains(tt.without, m); has != want {
					t.Errorf("%s.%s present = %v, want %v", rt, m, has, want)
				}
			}
		})
	}
}
