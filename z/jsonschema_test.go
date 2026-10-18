package z_test

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/AlexGalhardo/vukem/z"
)

var update = flag.Bool("update", false, "rewrite golden files")

type address struct {
	City string `json:"city"`
	Zip  string `json:"zip"`
}

type signup struct {
	Name    string            `json:"name"`
	Email   string            `json:"email"`
	Age     int               `json:"age"`
	Score   float64           `json:"score"`
	Role    string            `json:"role"`
	Tags    []string          `json:"tags"`
	Meta    map[string]string `json:"meta"`
	Address address           `json:"address"`
	Agree   bool              `json:"agree"`
	Nick    *string           `json:"nick"`
}

type tagged struct {
	Code  string   `json:"code"  validate:"required,len=3,alpha"`
	Price float64  `json:"price" validate:"min=0,max=100"`
	Qty   int      `json:"qty"   validate:"positive"`
	Site  string   `json:"site"  validate:"url"`
	Kind  string   `json:"kind"  validate:"oneof=a b"`
	Items []string `json:"items" validate:"max=3,dive,max=5"`
}

func TestJSONSchemaGolden(t *testing.T) {
	t.Parallel()

	explicit := z.Struct[signup](z.Shape{
		"name":  z.String().Trim().Min(2).Max(50).Describe("Full name"),
		"email": z.String().Email(),
		"age":   z.Int().Min(18).Max(130).Optional(),
		"score": z.Float().Positive().MultipleOf(0.5).Default(1),
		"role":  z.Enum("admin", "user").Nullable().Optional(),
		"tags":  z.Slice(z.String().Regex(regexp.MustCompile(`^[a-z]+$`))).Max(5).Unique().Optional(),
		"meta":  z.Map(z.String().StartsWith("x-")).Max(10).Optional(),
		"address": z.Struct[address](z.Shape{
			"city": z.String().NonEmpty(), "zip": z.String().Numeric().Len(5),
		}).Strict(),
		"agree": z.Bool(),
		"nick":  z.String().UUID().Nullable().Optional(),
	})

	got, err := json.MarshalIndent(map[string]any{
		"explicit": z.JSONSchema(explicit),
		"tags":     z.JSONSchema(z.For[tagged]()),
		"time":     z.JSONSchema(z.Time()),
	}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}

	got = append(got, '\n')
	golden := filepath.Join("testdata", "jsonschema.golden.json")

	if *update {
		if err := os.WriteFile(golden, got, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}

	if diff := cmp.Diff(string(bytes.ReplaceAll(want, []byte("\r\n"), []byte("\n"))), string(got)); diff != "" {
		t.Errorf("JSONSchema (-want +got):\n%s", diff)
	}
}

func TestJSONSchemaUnknown(t *testing.T) {
	t.Parallel()

	custom := z.Custom(func(any) (int, error) { return 1, nil })
	if got := z.JSONSchema(custom); len(got) != 0 {
		t.Errorf("JSONSchema(custom) = %v, want {}", got)
	}
}

func TestJSONSchemaNullableIsNotRequired(t *testing.T) {
	t.Parallel()

	type in struct {
		Name string  `json:"name"`
		Bio  *string `json:"bio"`
		Role *string `json:"role"`
	}

	s := z.Struct[in](z.Shape{
		"name": z.String(),
		"bio":  z.String().Nullable(),
		"role": z.Enum("admin", "user").Nullable(),
	})

	// The parser accepts a Nullable key that is absent, so the schema must
	// not require it (the schema may be looser than the server, never
	// stricter).
	for _, body := range []map[string]any{{"name": "Ana"}, {"name": "Ana", "bio": nil, "role": nil}} {
		if _, err := s.Parse(body); err != nil {
			t.Fatalf("Parse(%v) = %v, want nil", body, err)
		}
	}

	got := z.JSONSchema(s)["required"]
	if diff := cmp.Diff([]string{"name"}, got); diff != "" {
		t.Fatalf("required mismatch (-want +got):\n%s", diff)
	}
}
