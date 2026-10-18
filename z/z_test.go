package z

import (
	"encoding/json"
	"errors"
	"math"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
)

func issuesOf(t *testing.T, err error) Issues {
	t.Helper()

	if err == nil {
		return nil
	}

	is, ok := AsIssues(err)
	if !ok {
		t.Fatalf("error %v is not Issues", err)
	}

	return is
}

func codes(is Issues) []string {
	if len(is) == 0 {
		return nil
	}

	out := make([]string, len(is))
	for i, x := range is {
		out[i] = x.Path + ":" + x.Code
	}

	return out
}

func TestString(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		schema StringSchema
		in     any
		want   string
		codes  []string
	}{
		{name: "plain", schema: String(), in: "hi", want: "hi"},
		{name: "empty is valid", schema: String(), in: "", want: ""},
		{name: "wrong type", schema: String(), in: json.Number("1"), codes: []string{":invalid_type"}},
		{name: "nil required", schema: String(), in: nil, codes: []string{":required"}},
		{name: "trim then min", schema: String().Trim().Min(3), in: "  ab  ", codes: []string{":too_small"}},
		{name: "trim ok", schema: String().Trim().Min(2), in: "  ab  ", want: "ab"},
		{name: "lower upper", schema: String().ToLower(), in: "AbC", want: "abc"},
		{name: "upper", schema: String().ToUpper(), in: "abc", want: "ABC"},
		{name: "max runes", schema: String().Max(3), in: "çãõ", want: "çãõ"},
		{name: "max fail", schema: String().Max(2), in: "abc", codes: []string{":too_big"}},
		{name: "len", schema: String().Len(2), in: "abc", codes: []string{":too_big"}},
		{name: "len short", schema: String().Len(4), in: "abc", codes: []string{":too_small"}},
		{name: "email ok", schema: String().Email(), in: "a.b@ex.com", want: "a.b@ex.com"},
		{
			name:   "email name rejected",
			schema: String().Email(),
			in:     "Bob <a@ex.com>",
			codes:  []string{":invalid_string"},
		},
		{name: "email no tld", schema: String().Email(), in: "a@localhost", codes: []string{":invalid_string"}},
		{name: "url ok", schema: String().URL(), in: "https://x.dev/a", want: "https://x.dev/a"},
		{name: "url js", schema: String().URL(), in: "javascript:alert(1)", codes: []string{":invalid_string"}},
		{
			name:   "uuid",
			schema: String().UUID(),
			in:     "123e4567-e89b-12d3-a456-426614174000",
			want:   "123e4567-e89b-12d3-a456-426614174000",
		},
		{name: "uuid bad", schema: String().UUID(), in: "nope", codes: []string{":invalid_string"}},
		{name: "ulid", schema: String().ULID(), in: "01ARZ3NDEKTSV4RRFFQ69G5FAV", want: "01ARZ3NDEKTSV4RRFFQ69G5FAV"},
		{name: "alpha", schema: String().Alpha(), in: "abcÉ", want: "abcÉ"},
		{name: "alpha bad", schema: String().Alpha(), in: "ab1", codes: []string{":invalid_string"}},
		{name: "alphanum", schema: String().Alphanum(), in: "ab1", want: "ab1"},
		{name: "numeric", schema: String().Numeric(), in: "-1.5", want: "-1.5"},
		{name: "regex", schema: String().Regex(regexp.MustCompile(`^[a-z-]+$`)), in: "a-b", want: "a-b"},
		{
			name:   "regex bad",
			schema: String().Regex(regexp.MustCompile(`^[a-z-]+$`)),
			in:     "A",
			codes:  []string{":invalid_string"},
		},
		{
			name:   "starts ends includes",
			schema: String().StartsWith("a").EndsWith("z").Includes("m"),
			in:     "amz",
			want:   "amz",
		},
		{
			name:   "starts fail",
			schema: String().StartsWith("a").EndsWith("z"),
			in:     "bmy",
			codes:  []string{":invalid_string", ":invalid_string"},
		},
		{name: "oneof", schema: String().OneOf("x", "y"), in: "z", codes: []string{":invalid_enum"}},
		{name: "nonempty", schema: String().NonEmpty(), in: "", codes: []string{":too_small"}},
		{name: "invalid utf8", schema: String(), in: "a\xffb", codes: []string{":invalid_string"}},
		{name: "control char", schema: String(), in: "a\x00b", codes: []string{":invalid_string"}},
		{name: "newline ok", schema: String(), in: "a\nb\tc", want: "a\nb\tc"},
		{name: "default", schema: String().Default("d"), in: nil, want: "d"},
		{name: "nullable", schema: String().Nullable(), in: nil, want: ""},
		{name: "transform", schema: String().Transform(strings.ToUpper), in: "a", want: "A"},
		{name: "refine", schema: String().Refine(func(s string) error {
			if s == "admin" {
				return errors.New("reserved name")
			}
			return nil
		}), in: "admin", codes: []string{":custom"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := tt.schema.Parse(tt.in)
			if diff := cmp.Diff(tt.codes, codes(issuesOf(t, err))); diff != "" {
				t.Fatalf("issues mismatch (-want +got):\n%s\n%v", diff, err)
			}
			if err == nil && got != tt.want {
				t.Fatalf("Parse(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestMessages(t *testing.T) {
	t.Parallel()

	_, err := String().Min(3).Parse("a")
	is := issuesOf(t, err)
	if is[0].Message != "The value field must be at least 3 characters." || is[0].Params["min"] != 3 {
		t.Fatalf("message = %q params = %v", is[0].Message, is[0].Params)
	}

	_, err = String().Min(3).Message("too_small", "Need {min}+ chars for {field}.").Parse("a")
	if got := issuesOf(t, err)[0].Message; got != "Need 3+ chars for value." {
		t.Fatalf("override = %q", got)
	}

	_, err = Struct[struct {
		FirstName string `json:"first_name"`
	}](Shape{"first_name": String().Min(2)}).Parse(map[string]any{"first_name": "a"})
	if got := issuesOf(t, err)[0].Message; got != "The first name field must be at least 2 characters." {
		t.Fatalf("field name rendering = %q", got)
	}
}

func TestNumbers(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		parse func() (any, error)
		want  any
		codes []string
	}{
		{name: "int json number", parse: func() (any, error) { return Int().Parse(json.Number("42")) }, want: 42},
		{name: "int float64 integral", parse: func() (any, error) { return Int().Parse(42.0) }, want: 42},
		{
			name:  "int fraction",
			parse: func() (any, error) { return Int().Parse(1.5) },
			codes: []string{":invalid_type"},
		},
		{name: "int form string", parse: func() (any, error) { return Int().Parse(" 7 ") }, want: 7},
		{
			name:  "int bad string",
			parse: func() (any, error) { return Int().Parse("7a") },
			codes: []string{":invalid_type"},
		},
		{name: "int bool", parse: func() (any, error) { return Int().Parse(true) }, codes: []string{":invalid_type"}},
		{
			name:  "int32 overflow",
			parse: func() (any, error) { return Int32().Parse(json.Number("3000000000")) },
			codes: []string{":invalid_type"},
		},
		{
			name:  "int64 big",
			parse: func() (any, error) { return Int64().Parse(json.Number("9223372036854775807")) },
			want:  int64(math.MaxInt64),
		},
		{
			name:  "uint negative",
			parse: func() (any, error) { return Uint().Parse(json.Number("-1")) },
			codes: []string{":invalid_type"},
		},
		{
			name:  "uint64 max",
			parse: func() (any, error) { return Uint64().Parse(json.Number("18446744073709551615")) },
			want:  uint64(math.MaxUint64),
		},
		{name: "float", parse: func() (any, error) { return Float().Parse("1.25") }, want: 1.25},
		{
			name:  "float nan",
			parse: func() (any, error) { return Float().Parse("NaN") },
			codes: []string{":invalid_type"},
		},
		{
			name:  "float32",
			parse: func() (any, error) { return Float32().Parse(json.Number("0.5")) },
			want:  float32(0.5),
		},
		{name: "native int", parse: func() (any, error) { return Int64().Parse(int32(5)) }, want: int64(5)},
		{
			name:  "named type",
			parse: func() (any, error) { return NumberOf[cents]().Parse(json.Number("99")) },
			want:  cents(99),
		},
		{
			name:  "min max",
			parse: func() (any, error) { return Int().Min(1).Max(3).Parse(json.Number("5")) },
			codes: []string{":too_big"},
		},
		{
			name:  "min",
			parse: func() (any, error) { return Int().Min(1).Parse(json.Number("0")) },
			codes: []string{":too_small"},
		},
		{
			name:  "positive",
			parse: func() (any, error) { return Int().Positive().Parse(json.Number("0")) },
			codes: []string{":too_small"},
		},
		{
			name:  "nonnegative",
			parse: func() (any, error) { return Int().NonNegative().Parse(json.Number("0")) },
			want:  0,
		},
		{
			name:  "multiple",
			parse: func() (any, error) { return Int().MultipleOf(5).Parse(json.Number("12")) },
			codes: []string{":not_multiple_of"},
		},
		{name: "empty string absent", parse: func() (any, error) { return Int().Optional().Parse("") }, want: 0},
		{
			name:  "empty string required",
			parse: func() (any, error) { return Int().Parse("") },
			codes: []string{":required"},
		},
		{name: "default", parse: func() (any, error) { return Int().Default(9).Parse(nil) }, want: 9},
		{name: "transform refine", parse: func() (any, error) {
			return Int().Transform(func(n int) int { return n * 2 }).Refine(func(n int) error {
				if n > 10 {
					return errors.New("too big")
				}
				return nil
			}).Parse(json.Number("6"))
		}, codes: []string{":custom"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := tt.parse()
			if diff := cmp.Diff(tt.codes, codes(issuesOf(t, err))); diff != "" {
				t.Fatalf("issues mismatch (-want +got):\n%s", diff)
			}
			if err == nil && got != tt.want {
				t.Fatalf("got %v (%T), want %v (%T)", got, got, tt.want, tt.want)
			}
		})
	}
}

type cents int64

func TestBoolTimeEnum(t *testing.T) {
	t.Parallel()

	for in, want := range map[any]bool{true: true, "on": true, "YES": true, "1": true, "false": false, "off": false, "0": false} {
		if got, err := Bool().Parse(in); err != nil || got != want {
			t.Errorf("Bool().Parse(%v) = %v, %v", in, got, err)
		}
	}
	if _, err := Bool().Parse("maybe"); err == nil {
		t.Error("Bool().Parse(maybe) = nil error")
	}
	if got, err := Bool().Default(false).Parse(nil); err != nil || got {
		t.Error("unchecked checkbox default")
	}

	ref := time.Date(2026, 5, 6, 7, 8, 0, 0, time.UTC)
	for _, in := range []any{"2026-05-06T07:08:00Z", "2026-05-06T07:08", ref} {
		if got, err := Time().Parse(in); err != nil || !got.Equal(ref) {
			t.Errorf("Time().Parse(%v) = %v, %v", in, got, err)
		}
	}
	if got, _ := Time().Parse("2026-05-06"); got.Day() != 6 {
		t.Error("date-only layout")
	}
	if _, err := Time().Parse("06/05/2026"); err == nil || issuesOf(t, err)[0].Code != CodeInvalidDate {
		t.Error("bad date accepted")
	}
	if _, err := Time().Layouts("02/01/2006").Parse("06/05/2026"); err != nil {
		t.Errorf("custom layout: %v", err)
	}
	if _, err := Time().After(ref).Parse(ref.Format(time.RFC3339)); err == nil {
		t.Error("After not strict")
	}
	if _, err := Time().Before(ref).Parse(ref.Add(time.Hour).Format(time.RFC3339)); err == nil {
		t.Error("Before not enforced")
	}
	if _, err := Time().Parse(1); err == nil {
		t.Error("number accepted as time")
	}

	type status string
	s := EnumOf[status]("draft", "published").Default("draft")
	if got, err := s.Parse(nil); err != nil || got != "draft" {
		t.Errorf("enum default = %q %v", got, err)
	}
	if got, err := s.Parse("published"); err != nil || got != "published" {
		t.Errorf("enum = %q %v", got, err)
	}
	if _, err := s.Parse("deleted"); err == nil || issuesOf(t, err)[0].Params["options"] != "draft, published" {
		t.Errorf("enum invalid = %v", err)
	}
	if got := Enum("a", "b").Values(); len(got) != 2 {
		t.Error("Values()")
	}
}

type Author struct {
	Email string `json:"email"`
}

type Post struct {
	Title       string     `json:"title"`
	Tags        []string   `json:"tags"`
	Status      string     `json:"status"`
	PublishedAt *time.Time `json:"published_at"`
	Author      Author     `json:"author"`
	Views       int        `                    form:"views"`
	Meta        map[string]int
	Internal    string `json:"-"`
}

var postSchema = Struct[Post](Shape{
	"title":        String().Trim().Min(3).Max(50),
	"tags":         Slice(String().Trim().ToLower().Alphanum()).Max(3).Default([]string{}),
	"status":       Enum("draft", "published").Default("draft"),
	"published_at": Time().Optional(),
	"author":       Struct[Author](Shape{"email": String().Email()}),
	"views":        Int().Min(0).Optional(),
	"meta":         Map(Int()).Optional(),
}).Refine(func(p Post) error {
	if p.Status == "published" && p.PublishedAt == nil {
		return FieldError("published_at", "Required when the post is published.")
	}
	return nil
})

func TestStruct(t *testing.T) {
	t.Parallel()

	got, err := postSchema.Parse(map[string]any{
		"title":        "  Hello World ",
		"tags":         []any{"Go", " WEB "},
		"published_at": "2026-01-02T03:04:05Z",
		"author":       map[string]any{"email": "a@b.co"},
		"views":        "12",
		"meta":         map[string]any{"likes": json.Number("3")},
		"extra":        "ignored",
	})
	if err != nil {
		t.Fatal(err)
	}

	want := Post{
		Title: "Hello World", Tags: []string{"go", "web"}, Status: "draft",
		Author: Author{Email: "a@b.co"}, Views: 12, Meta: map[string]int{"likes": 3},
	}
	if got.PublishedAt == nil || got.PublishedAt.Year() != 2026 {
		t.Fatalf("PublishedAt = %v", got.PublishedAt)
	}
	got.PublishedAt = nil
	if diff := cmp.Diff(want, got); diff != "" {
		t.Fatalf("Parse() mismatch (-want +got):\n%s", diff)
	}
}

func TestStructIssues(t *testing.T) {
	t.Parallel()

	_, err := postSchema.Parse(map[string]any{
		"title":  "x",
		"tags":   []any{"ok", "not ok!", 3, "x", "y"},
		"status": "published",
		"author": map[string]any{"email": "nope"},
		"views":  "-1",
		"meta":   map[string]any{"a": "x"},
	})

	want := []string{
		"author.email:invalid_string",
		"meta.a:invalid_type",
		"tags:too_big",
		"tags.1:invalid_string",
		"tags.2:invalid_type",
		"title:too_small",
		"views:too_small",
	}
	if diff := cmp.Diff(want, codes(issuesOf(t, err))); diff != "" {
		t.Fatalf("issues mismatch (-want +got):\n%s", diff)
	}

	_, err = postSchema.Parse(
		map[string]any{"title": "Hello", "status": "published", "author": map[string]any{"email": "a@b.co"}},
	)
	is := issuesOf(t, err)
	if len(is) != 1 || is[0].Path != "published_at" || is[0].Code != CodeCustom {
		t.Fatalf("refine issue = %v", is)
	}
	if fields := is.Fields(); fields["published_at"][0] != "Required when the post is published." {
		t.Fatalf("Fields() = %v", fields)
	}

	if _, err := postSchema.Parse("not an object"); err == nil || issuesOf(t, err)[0].Code != CodeInvalidType {
		t.Fatal("non-object accepted")
	}
}

func TestStructStrictAndForms(t *testing.T) {
	t.Parallel()

	type login struct {
		Email string `form:"email"`
	}

	s := Struct[login](Shape{"email": String().Email()}).Strict()
	_, err := s.Parse(map[string]any{"email": "a@b.co", "admin": "true"})
	if diff := cmp.Diff([]string{"admin:unrecognized_keys"}, codes(issuesOf(t, err))); diff != "" {
		t.Fatal(diff)
	}
	if keys := s.Keys(); len(keys) != 1 || keys[0] != "email" {
		t.Fatal("Keys()")
	}

	// A single form value is accepted as a one-element list.
	type f struct {
		IDs []int `form:"ids"`
	}
	got, err := Struct[f](Shape{"ids": Slice(Int())}).Parse(map[string]any{"ids": "4"})
	if err != nil || len(got.IDs) != 1 || got.IDs[0] != 4 {
		t.Fatalf("single form value = %v %v", got, err)
	}
}

func TestStructConstructionPanics(t *testing.T) {
	t.Parallel()

	mustPanic := func(name string, fn func()) {
		t.Helper()
		defer func() {
			if recover() == nil {
				t.Errorf("%s: no panic", name)
			}
		}()
		fn()
	}

	mustPanic("unknown key", func() { Struct[Author](Shape{"nope": String()}) })
	mustPanic("wrong type", func() { Struct[Author](Shape{"email": Int()}) })
	mustPanic("not a struct", func() { Struct[int](Shape{}) })
	mustPanic("empty enum", func() { Enum() })
	mustPanic("bad slice default", func() { Slice(Int()).Default([]string{}) })
	mustPanic("ignored field", func() { Struct[Post](Shape{"Internal": String()}) })
}

func TestSliceAndMapRules(t *testing.T) {
	t.Parallel()

	if _, err := Slice(Int()).Min(2).Parse([]any{json.Number("1")}); err == nil {
		t.Error("Min")
	}
	if _, err := Slice(Int()).Len(2).Parse([]any{json.Number("1"), json.Number("2"), json.Number("3")}); err == nil {
		t.Error("Len")
	}
	if _, err := Slice(Int()).NonEmpty().Parse([]any{}); err == nil {
		t.Error("NonEmpty")
	}
	if _, err := Slice(
		Int(),
	).Unique().
		Parse([]any{json.Number("1"), json.Number("1")}); err == nil ||
		issuesOf(t, err)[0].Code != CodeNotUnique {
		t.Error("Unique")
	}
	if got, err := Slice(Int()).Parse([]int{1, 2}); err != nil || len(got.([]int)) != 2 {
		t.Errorf("native slice = %v %v", got, err)
	}
	if _, err := Slice(Int()).Parse(map[string]any{}); err == nil {
		t.Error("object as list")
	}
	if got, err := Slice(Int()).Optional().Parse(nil); err != nil || got != nil {
		t.Errorf("optional slice = %v %v", got, err)
	}
	if _, err := Map(String()).Min(1).Parse(map[string]any{}); err == nil {
		t.Error("map Min")
	}
	if _, err := Map(String()).Max(0).Parse(map[string]any{"a": "b"}); err == nil {
		t.Error("map Max")
	}
	if _, err := Map(String()).Parse([]any{}); err == nil {
		t.Error("list as map")
	}
}

func TestCustomAndParseJSON(t *testing.T) {
	t.Parallel()

	hex := Custom(func(in any) ([]byte, error) {
		s, _ := in.(string)
		if len(s)%2 != 0 {
			return nil, errors.New("must have an even length")
		}
		return []byte(s), nil
	})

	if _, err := hex.Parse("abc"); err == nil || issuesOf(t, err)[0].Err.Error() != "must have an even length" {
		t.Fatalf("custom = %v", err)
	}
	if got, err := hex.Optional().Parse(nil); err != nil || got != nil {
		t.Fatal("custom optional")
	}

	got, err := ParseJSON(strings.NewReader(`{"email":"a@b.co"}`), Struct[Author](Shape{"email": String().Email()}))
	if err != nil || got.Email != "a@b.co" {
		t.Fatalf("ParseJSON = %v %v", got, err)
	}
	if _, err := ParseJSON(strings.NewReader(`{`), String()); err == nil {
		t.Fatal("bad JSON accepted")
	}
}

func TestCheckOutgoing(t *testing.T) {
	t.Parallel()

	good := Post{Title: "Hello", Status: "draft", Author: Author{Email: "a@b.co"}, Tags: []string{"go"}}
	if err := postSchema.Check(good); err != nil {
		t.Fatalf("Check(good) = %v", err)
	}

	bad := good
	bad.Author.Email = "nope"
	bad.Title = "x"
	if diff := cmp.Diff(
		[]string{"author.email:invalid_string", "title:too_small"},
		codes(issuesOf(t, postSchema.Check(bad))),
	); diff != "" {
		t.Fatal(diff)
	}

	// Check does not coerce strings into numbers.
	if err := String().Check("x"); err != nil {
		t.Fatal(err)
	}
	if err := Int().Check(3); err != nil {
		t.Fatal(err)
	}
	if err := Bool().Check(true); err != nil || Time().Check(time.Now()) != nil || Enum("a").Check("a") != nil {
		t.Fatal("scalar Check")
	}

	// Typed values passed to Parse are re-validated too.
	if _, err := postSchema.Parse(bad); err == nil {
		t.Fatal("Parse(typed bad value) = nil")
	}
}

type tagged struct {
	Email    string   `json:"email"    validate:"required,email"`
	Password string   `json:"password" validate:"required,min=12"`
	Name     string   `json:"name"     validate:"max=5"`
	Role     role     `json:"role"     validate:"oneof=admin user"`
	Age      int      `json:"age"      validate:"min=18,max=130"`
	Score    *float64 `json:"score"    validate:"positive"`
	Tags     []string `json:"tags"     validate:"max=2,dive,alphanum"`
	Code     string   `json:"code"     validate:"regex=^[A-Z]{2},[0-9]$"`
	Accept   bool     `json:"accept"   validate:"required"`
	When     time.Time
	Address  struct {
		City string `json:"city" validate:"required"`
	} `json:"address"`
	Skip string `json:"-"        validate:"required"`
}

type role string

func TestFromTags(t *testing.T) {
	t.Parallel()

	s := For[tagged]()

	got, err := s.Parse(map[string]any{
		"email": "a@b.co", "password": "correct horse", "name": "Ann", "role": "user",
		"age": "30", "score": "1.5", "tags": []any{"a1"}, "code": "AB,7", "accept": "on",
		"address": map[string]any{"city": "Rio"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Role != "user" || got.Age != 30 || *got.Score != 1.5 || got.Address.City != "Rio" || !got.Accept ||
		got.Code != "AB,7" {
		t.Fatalf("FromTags parse = %+v", got)
	}

	_, err = s.Parse(map[string]any{
		"email": "", "password": "short", "name": "toolong", "role": "root",
		"age": "10", "score": "-1", "tags": []any{"!", "b", "c"}, "code": "x", "address": map[string]any{},
	})
	want := []string{
		"accept:required",
		"address.city:required",
		"age:too_small",
		"code:invalid_string",
		"email:too_small",
		"email:invalid_string",
		"name:too_big",
		"password:too_small",
		"role:invalid_enum",
		"score:too_small",
		"tags:too_big",
		"tags.0:invalid_string",
	}
	if diff := cmp.Diff(want, codes(issuesOf(t, err))); diff != "" {
		t.Fatalf("FromTags issues mismatch (-want +got):\n%s", diff)
	}
	if issuesOf(t, err).First("email") != "The email field is required." {
		t.Fatalf("required string message = %q", issuesOf(t, err).First("email"))
	}

	// For caches: same schema value semantics.
	if diff := cmp.Diff(s.Keys(), For[tagged]().Keys()); diff != "" {
		t.Fatal(diff)
	}
	if FromTags[tagged]().Keys()[0] != s.Keys()[0] {
		t.Fatal("FromTags/For disagree")
	}
}

func TestFromTagsPanics(t *testing.T) {
	t.Parallel()

	cases := map[string]func(){
		"unknown rule": func() {
			FromTags[struct {
				A string `validate:"shiny"`
			}]()
		},
		"bad min": func() {
			FromTags[struct {
				A string `validate:"min=x"`
			}]()
		},
		"bad regex": func() {
			FromTags[struct {
				A string `validate:"regex=("`
			}]()
		},
		"number rule": func() {
			FromTags[struct {
				A int `validate:"email"`
			}]()
		},
		"bad number": func() {
			FromTags[struct {
				A int `validate:"min=x"`
			}]()
		},
		"list rule": func() {
			FromTags[struct {
				A []int `validate:"email"`
			}]()
		},
		"bad list arg": func() {
			FromTags[struct {
				A []int `validate:"max=x"`
			}]()
		},
		"bool rule": func() {
			FromTags[struct {
				A bool `validate:"min=1"`
			}]()
		},
		"time rule": func() {
			FromTags[struct {
				A time.Time `validate:"min=1"`
			}]()
		},
		"struct rule": func() {
			FromTags[struct {
				A Author `validate:"min=1"`
			}]()
		},
		"unsupported":  func() { FromTags[struct{ A chan int }]() },
		"not a struct": func() { FromTags[int]() },
		"dive bad": func() {
			FromTags[struct {
				A []string `validate:"dive,nope"`
			}]()
		},
	}

	for name, fn := range cases {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("%s: no panic", name)
				}
			}()
			fn()
		}()
	}
}

func TestIssuesHelpers(t *testing.T) {
	t.Parallel()

	is := Issues{{Path: "", Code: "x", Message: "root bad"}, {Path: "a", Code: "y", Message: "a bad"}}
	if is.Error() != "z: (root): root bad; a: a bad" {
		t.Fatalf("Error() = %q", is.Error())
	}
	if is.Fields()["_"][0] != "root bad" || is.First("a") != "a bad" || is.First("b") != "" {
		t.Fatal("helpers")
	}
	if _, ok := AsIssues(errors.New("x")); ok {
		t.Fatal("AsIssues(other)")
	}
	if FieldError("a", "m").Error() != "a: m" {
		t.Fatal("FieldError")
	}
	if fieldName("tags.2") != "tags.2" || fieldName("") != "value" {
		t.Fatal("fieldName")
	}
}

func TestImmutableAndConcurrent(t *testing.T) {
	t.Parallel()

	base := String().Min(2)
	stricter := base.Max(3)

	if _, err := base.Parse("abcdef"); err != nil {
		t.Fatal("modifier mutated the base schema")
	}
	if _, err := stricter.Parse("abcdef"); err == nil {
		t.Fatal("derived schema lost its check")
	}

	var wg sync.WaitGroup
	for i := range 50 {
		wg.Go(func() {
			in := map[string]any{
				"title":  "Hello",
				"author": map[string]any{"email": "a@b.co"},
				"views":  json.Number("1"),
			}
			if i%2 == 0 {
				in["title"] = "x"
			}
			_, _ = postSchema.Parse(in)
		})
	}
	wg.Wait()
}

func FuzzParse(f *testing.F) {
	for _, s := range []string{`{"title":"Hello","author":{"email":"a@b.co"}}`, `{"tags":[1,2,"x"]}`, `[]`, `null`, `"x"`} {
		f.Add(s)
	}
	f.Fuzz(func(_ *testing.T, data string) {
		_, _ = ParseJSON(strings.NewReader(data), postSchema)
		_, _ = ParseJSON(strings.NewReader(data), For[tagged]())
	})
}

func TestFromTagsRequiredList(t *testing.T) {
	t.Parallel()

	type list struct {
		Data []int `json:"data" validate:"required"`
		IDs  []int `json:"ids"  validate:"required,min=1"`
	}

	tests := []struct {
		name string
		in   map[string]any
		want []string
	}{
		{"empty lists", map[string]any{"data": []any{}, "ids": []any{1}}, nil},
		{"min=1 rejects empty", map[string]any{"data": []any{}, "ids": []any{}}, []string{"ids:too_small"}},
		{"missing", map[string]any{}, []string{"data:required", "ids:required"}},
		{"null", map[string]any{"data": nil, "ids": nil}, []string{"data:required", "ids:required"}},
	}

	s := For[list]()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := s.Parse(tt.in)

			var got []string
			if err != nil {
				got = codes(issuesOf(t, err))
			}

			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("Parse(%v) issues (-want +got):\n%s", tt.in, diff)
			}
		})
	}

	js := JSONSchema(s)
	if _, ok := js["properties"].(map[string]any)["data"].(map[string]any)["minItems"]; ok {
		t.Errorf("JSONSchema(required list) has minItems: %v", js)
	}
}

func TestMessagesSingular(t *testing.T) {
	t.Parallel()

	str := func(s StringSchema, in string) func() error {
		return func() error { _, err := s.Parse(in); return err }
	}
	list := func(s SliceSchema, in []any) func() error {
		return func() error { _, err := s.Parse(in); return err }
	}

	tests := []struct {
		name  string
		parse func() error
		want  string
	}{
		{"min 1 char", str(String().Min(1), ""), "The value field must be at least 1 character."},
		{"max 1 char", str(String().Max(1), "ab"), "The value field must not be greater than 1 character."},
		{"len 1 char", str(String().Len(1), "ab"), "The value field must be 1 character."},
		{"max 2 chars", str(String().Max(2), "abc"), "The value field must not be greater than 2 characters."},
		{"min 1 item", list(Slice(String()).Min(1), []any{}), "The value field must have at least 1 item."},
		{
			"max 1 item",
			list(Slice(String()).Max(1), []any{"a", "b"}),
			"The value field must not have more than 1 item.",
		},
		{"len 1 item", list(Slice(String()).Len(1), []any{"a", "b"}), "The value field must have 1 item."},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := issuesOf(t, tt.parse())[0].Message; got != tt.want {
				t.Fatalf("message = %q, want %q", got, tt.want)
			}
		})
	}
}
