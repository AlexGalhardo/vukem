package z

import (
	"testing"

	"github.com/google/go-cmp/cmp"
)

type localizeInput struct {
	Name  string   `json:"name"  validate:"required"`
	Age   int      `json:"age"   validate:"positive"`
	Email string   `json:"email" validate:"required,email"`
	Tags  []string `json:"tags"  validate:"max=1"`
}

func TestIssues_Key(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		schema AnySchema
		in     any
		want   []string
	}{
		{"required", Struct[struct {
			A string `json:"a"`
		}](Shape{"a": String()}), map[string]any{}, []string{CodeRequired}},
		{"detailed key", String().Min(3), "ab", []string{"too_small.string"}},
		{"format", String().Email(), "nope", []string{"string.email"}},
		{"positive", Int().Positive(), 0, []string{"too_small.positive"}},
		{"override clears key", String().Min(3).Message(CodeTooSmall, "short"), "ab", []string{""}},
		{"refine is custom", String().Refine(func(string) error { return stringError("bad") }), "x", []string{""}},
		{
			"tags", For[localizeInput](),
			map[string]any{"name": "", "age": 0, "email": "x", "tags": []any{"a", "b"}},
			[]string{"too_small.positive", "string.email", CodeRequired, "too_big.array"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := parseRoot[any](tt.schema, tt.in)

			is, ok := AsIssues(err)
			if !ok {
				t.Fatalf("Parse(%v) error = %v, want Issues", tt.in, err)
			}

			got := make([]string, 0, len(is))
			for _, i := range is {
				got = append(got, i.Key)
			}

			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("Issue keys (-want +got):\n%s", diff)
			}
		})
	}
}

func TestIssues_Localize(t *testing.T) {
	t.Parallel()

	catalog := map[string]string{
		"required":          "O campo {field} é obrigatório.",
		"too_small.string":  "O campo {field} deve ter pelo menos {min} caracteres.",
		"invalid_string":    "O campo {field} é inválido.",
		"attributes.e_mail": "e-mail",
	}
	lookup := func(key string) (string, bool) {
		s, ok := catalog[key]
		return s, ok
	}

	is := Issues{
		{Path: "name", Code: CodeRequired, Key: CodeRequired, Message: "The name field is required."},
		{
			Path:    "user.first_name",
			Code:    CodeTooSmall,
			Key:     "too_small.string",
			Message: "en",
			Params:  map[string]any{"min": 3},
		},
		{Path: "e_mail", Code: CodeInvalidString, Key: "string.email", Message: "en"},
		{Path: "age", Code: CodeTooBig, Key: "too_big.number", Message: "untranslated"},
		{Path: "x", Code: CodeCustom, Message: "custom stays"},
	}

	got := is.Localize(lookup)

	want := []string{
		"O campo name é obrigatório.",
		"O campo first name deve ter pelo menos 3 caracteres.",
		"O campo e-mail é inválido.",
		"untranslated",
		"custom stays",
	}

	msgs := make([]string, 0, len(got))
	for _, i := range got {
		msgs = append(msgs, i.Message)
	}

	if diff := cmp.Diff(want, msgs); diff != "" {
		t.Errorf("Localize messages (-want +got):\n%s", diff)
	}

	if is[0].Message != "The name field is required." {
		t.Errorf("Localize mutated its receiver: %q", is[0].Message)
	}

	if nilLookup := is.Localize(nil); nilLookup[0].Message != is[0].Message {
		t.Errorf("Localize(nil) changed messages")
	}
}

func TestPositive_Message(t *testing.T) {
	t.Parallel()

	_, err := Int().Positive().Parse(0)
	if got, want := err.Error(), "z: (root): The value field must be greater than 0."; got != want {
		t.Errorf("Int().Positive().Parse(0) = %q, want %q", got, want)
	}
}

type stringError string

func (e stringError) Error() string { return string(e) }

type confirmInput struct {
	Password             string `json:"password"`
	PasswordConfirmation string `json:"password_confirmation"`
}

func TestFieldErrorKey(t *testing.T) {
	t.Parallel()

	confirm := Struct[confirmInput](Shape{
		"password":              String(),
		"password_confirmation": String(),
	}).Refine(func(in confirmInput) error {
		if in.Password != in.PasswordConfirmation {
			return FieldErrorKey("password_confirmation", "confirmed",
				"The {field} field does not match {other}.", map[string]any{"other": "password"})
		}

		return nil
	})

	tests := []struct {
		name   string
		schema AnySchema
		in     any
		want   Issues
	}{
		{
			"struct field", confirm,
			map[string]any{"password": "a", "password_confirmation": "b"},
			Issues{{
				Path:    "password_confirmation",
				Code:    CodeCustom,
				Key:     "confirmed",
				Message: "The password confirmation field does not match password.",
				Params:  map[string]any{"other": "password"},
			}},
		},
		{
			"root value", String().Refine(func(string) error {
				return FieldErrorKey("", "phone", "The {field} field must be a phone number.", nil)
			}), "x",
			Issues{{Code: CodeCustom, Key: "phone", Message: "The value field must be a phone number."}},
		},
		{
			"nested under parent path", Struct[struct {
				User confirmInput `json:"user"`
			}](Shape{"user": confirm}),
			map[string]any{"user": map[string]any{"password": "a", "password_confirmation": "b"}},
			Issues{{
				Path:    "user.password_confirmation",
				Code:    CodeCustom,
				Key:     "confirmed",
				Message: "The password confirmation field does not match password.",
				Params:  map[string]any{"other": "password"},
			}},
		},
		{
			"custom schema", Custom(func(any) (string, error) {
				return "", FieldErrorKey("", "custom_rule", "Bad {field}.", nil)
			}), "x",
			Issues{{Code: CodeCustom, Key: "custom_rule", Message: "Bad value."}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := parseRoot[any](tt.schema, tt.in)

			got, ok := AsIssues(err)
			if !ok {
				t.Fatalf("Parse(%v) error = %v, want Issues", tt.in, err)
			}

			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("Parse(%v) issues (-want +got):\n%s", tt.in, diff)
			}
		})
	}
}

func TestFieldErrorKey_Localize(t *testing.T) {
	t.Parallel()

	catalogs := map[string]map[string]string{
		"en": {"confirmed": "The {field} confirmation does not match."},
		"pt-BR": {
			"confirmed":                        "A confirmação do campo {field} não confere com {other}.",
			"custom":                           "must not be used for keyed custom issues",
			"attributes.password_confirmation": "confirmação de senha",
		},
	}

	is := Issues{
		{
			Path:    "password_confirmation",
			Code:    CodeCustom,
			Key:     "confirmed",
			Message: "fallback",
			Params:  map[string]any{"other": "senha"},
		},
		{Path: "x", Code: CodeCustom, Key: "untranslated", Message: "fallback kept"},
	}

	tests := []struct {
		locale string
		want   []string
	}{
		{"en", []string{"The password confirmation confirmation does not match.", "fallback kept"}},
		{"pt-BR", []string{"A confirmação do campo confirmação de senha não confere com senha.", "fallback kept"}},
	}

	for _, tt := range tests {
		t.Run(tt.locale, func(t *testing.T) {
			t.Parallel()

			got := is.Localize(func(key string) (string, bool) {
				s, ok := catalogs[tt.locale][key]
				return s, ok
			})

			msgs := make([]string, 0, len(got))
			for _, i := range got {
				msgs = append(msgs, i.Message)
			}

			if diff := cmp.Diff(tt.want, msgs); diff != "" {
				t.Errorf("Localize(%s) messages (-want +got):\n%s", tt.locale, diff)
			}
		})
	}
}

func TestCustom_FieldError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want Issues
	}{
		{"field error path", FieldError("sub", "bad"), Issues{{Path: "sub", Code: CodeCustom, Message: "bad"}}},
		{
			"empty key is FieldError",
			FieldErrorKey("sub", "", "bad", nil),
			Issues{{Path: "sub", Code: CodeCustom, Message: "bad"}},
		},
		{"plain error", stringError("plain"), Issues{{Code: CodeCustom, Message: "plain"}}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := Custom(func(any) (string, error) { return "", tt.err }).Parse("x")

			got, _ := AsIssues(err)
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("Custom.Parse issues (-want +got):\n%s", diff)
			}
		})
	}
}
