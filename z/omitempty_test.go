package z_test

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/AlexGalhardo/vukem/z"
)

// A111: an optional enum could not be written as a tag. "omitempty" was
// accepted and thrown away, so validate:"omitempty,oneof=fast slow"
// rejected "" — and Optional() did not help either, because it governs an
// absent key, not an empty value. Apps hand-wrote a Normalise() per option
// struct to work around it.
func TestOmitEmptySkipsTheRulesOnAnEmptyString(t *testing.T) {
	t.Parallel()

	type options struct {
		Mode    string `json:"mode"    validate:"omitempty,oneof=fast slow"`
		Profile string `json:"profile" validate:"omitempty,min=3,max=8"`
		Need    string `json:"need"    validate:"required,oneof=a b"`
	}

	s := z.For[options]()

	for name, body := range map[string]map[string]any{
		"empty strings": {"mode": "", "profile": "", "need": "a"},
		"absent keys":   {"need": "a"},
		"filled in":     {"mode": "fast", "profile": "small", "need": "b"},
		"spaces only":   {"mode": "", "profile": "", "need": "a"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if _, err := s.Parse(body); err != nil {
				t.Errorf("Parse(%v) = %v", body, err)
			}
		})
	}

	// A value that is present and wrong is still refused: omitempty skips
	// the rules only for the empty value.
	for _, body := range []map[string]any{
		{"mode": "bogus", "need": "a"},
		{"profile": "xy", "need": "a"},
	} {
		if _, err := s.Parse(body); err == nil {
			t.Errorf("Parse(%v) = nil, want the rule to apply", body)
		}
	}

	// required still wins: it is not weakened by anything here.
	if _, err := s.Parse(map[string]any{"mode": "fast"}); err == nil {
		t.Error("a missing required field passed")
	}
}

// Trim runs first, so a form field holding only spaces is empty.
func TestOmitEmptyAfterTrim(t *testing.T) {
	t.Parallel()

	s := z.String().Trim().OmitEmpty().OneOf("fast", "slow")

	if got, err := s.Parse("   "); err != nil || got != "" {
		t.Errorf("Parse(spaces) = %q, %v, want \"\", nil", got, err)
	}

	if got, err := s.Parse("  fast "); err != nil || got != "fast" {
		t.Errorf("Parse(padded) = %q, %v", got, err)
	}

	if _, err := s.Parse("bogus"); err == nil {
		t.Error("Parse(bogus) = nil, want an error")
	}
}

// The empty value is describable: JSON Schema says the enum includes "".
func TestOmitEmptyJSONSchema(t *testing.T) {
	t.Parallel()

	got := z.JSONSchema(z.String().OmitEmpty().OneOf("fast", "slow"))

	if diff := cmp.Diff([]string{"fast", "slow", ""}, got["enum"]); diff != "" {
		t.Errorf("enum diff (-want +got):\n%s", diff)
	}
}

// A rule the tag cannot enforce is refused, not ignored: omitempty has a
// meaning only where "empty" is unambiguous. On a number, skipping the
// rules when the value is zero would make validate:"omitempty,min=1"
// accept 0, which is the opposite of what it says.
func TestOmitEmptyIsRefusedWhereItCannotApply(t *testing.T) {
	t.Parallel()

	type bad struct {
		Count int `json:"count" validate:"omitempty,min=1"`
	}

	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("z.For[bad]() did not panic")
		}

		msg, _ := r.(string)
		for _, want := range []string{"omitempty", "int"} {
			if !strings.Contains(msg, want) {
				t.Errorf("panic %q does not mention %q", msg, want)
			}
		}
	}()

	_ = z.For[bad]()
}

// A114: the builder has Default and the JSON schema emits it, but no tag
// reached it, so every option struct opened its normaliser with
// `if o.Format == "" { o.Format = "png" }` — about 150 lines across 19
// structs in one app, and a default the OpenAPI document never mentioned.
func TestDefaultTag(t *testing.T) {
	t.Parallel()

	type options struct {
		Format  string  `json:"format"  validate:"omitempty,default=png,oneof=png jpeg"`
		Quality int     `json:"quality" validate:"default=80,min=1,max=100"`
		Scale   float64 `json:"scale"   validate:"default=1.5"`
	}

	s := z.For[options]()

	// Absent keys take the defaults.
	got, err := s.Parse(map[string]any{})
	if err != nil {
		t.Fatalf("Parse({}) = %v", err)
	}

	want := options{Format: "png", Quality: 80, Scale: 1.5}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("Parse({}) diff (-want +got):\n%s", diff)
	}

	// An empty string takes it too, with omitempty: that is the form case,
	// and the line of Go this replaces.
	if got, err = s.Parse(map[string]any{"format": ""}); err != nil || got.Format != "png" {
		t.Errorf("Parse({format: \"\"}) = %+v, %v, want format png", got, err)
	}

	// A value that is given is kept, and still checked.
	if got, err = s.Parse(map[string]any{"format": "jpeg", "quality": float64(10)}); err != nil ||
		got.Format != "jpeg" || got.Quality != 10 {
		t.Errorf("Parse(given) = %+v, %v", got, err)
	}

	if _, err := s.Parse(map[string]any{"quality": float64(0)}); err == nil {
		t.Error("Parse({quality: 0}) = nil, want min=1 to apply to a value that was given")
	}

	// The default reaches the document, where before only the doc: prose
	// could mention it.
	props, _ := z.JSONSchema(s)["properties"].(map[string]any)

	format, _ := props["format"].(map[string]any)
	if format["default"] != "png" {
		t.Errorf("format schema = %v, want default png", format)
	}

	quality, _ := props["quality"].(map[string]any)
	if quality["default"] != float64(80) {
		t.Errorf("quality schema = %v, want default 80", quality)
	}
}

func TestDefaultTagRejectsBadArguments(t *testing.T) {
	t.Parallel()

	for name, build := range map[string]func(){
		// A default that the field's own rules refuse is a boot error: it
		// would otherwise be returned for every absent key and fail
		// nothing, because defaults are not re-validated.
		"not a number": func() {
			type bad struct {
				N int `json:"n" validate:"default=lots"`
			}
			_ = z.For[bad]()
		},
		// A bool takes no rules at all: its zero value is false, and a
		// field that should default to true is a *bool or an inverted
		// name (DisableSharpen), not a rule that would need a scope of
		// its own in the tag vocabulary.
		"a bool default": func() {
			type bad struct {
				B bool `json:"b" validate:"default=true"`
			}
			_ = z.For[bad]()
		},
		// A default is returned for every absent key without being
		// parsed, so one the field's own rules refuse would pass silently
		// forever.
		"outside its own enum": func() {
			type bad struct {
				S string `json:"s" validate:"default=gif,oneof=png jpeg"`
			}
			_ = z.For[bad]()
		},
		"outside its own bounds": func() {
			type bad struct {
				N int `json:"n" validate:"default=0,min=1,max=100"`
			}
			_ = z.For[bad]()
		},
		// required means the caller must provide it; a default means they
		// need not. Picking one silently is how omitempty went wrong.
		"required and default": func() {
			type bad struct {
				S string `json:"s" validate:"required,default=x"`
			}
			_ = z.For[bad]()
		},
		"required and omitempty": func() {
			type bad struct {
				S string `json:"s" validate:"required,omitempty"`
			}
			_ = z.For[bad]()
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			defer func() {
				if recover() == nil {
					t.Error("z.For did not panic")
				}
			}()

			build()
		})
	}
}
