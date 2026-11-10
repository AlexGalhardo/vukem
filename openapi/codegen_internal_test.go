package openapi

import (
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestZodBase(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		schema map[string]any
		typ    string
		want   string
	}{
		{"plain string", map[string]any{}, "string", "z.string()"},
		{"email", map[string]any{"format": "email"}, "string", "z.email()"},
		{"uri", map[string]any{"format": "uri"}, "string", "z.url()"},
		{"uuid", map[string]any{"format": "uuid"}, "string", "z.guid()"},
		{"date-time", map[string]any{"format": "date-time"}, "string", "z.iso.datetime({ offset: true })"},
		{
			"string bounds and pattern",
			map[string]any{"minLength": 2.0, "maxLength": 5.0, "pattern": `^a+$`},
			"string",
			`z.string().min(2).max(5).regex(new RegExp("^a+$", "u"))`,
		},
		{"integer", map[string]any{"minimum": 1.0}, "integer", "z.int().gte(1)"},
		{
			"number bounds",
			map[string]any{"minimum": 1.0, "maximum": 2.0, "exclusiveMinimum": 0.0, "multipleOf": 0.5},
			"number",
			"z.number().gte(1).lte(2).gt(0).multipleOf(0.5)",
		},
		{"boolean", map[string]any{}, "boolean", "z.boolean()"},
		{
			"array bounds",
			map[string]any{"items": map[string]any{"type": "string"}, "minItems": 1.0, "maxItems": 3.0},
			"array",
			"z.array(z.string()).min(1).max(3)",
		},
		{"object without properties", map[string]any{}, "object", "z.record(z.string(), z.unknown())"},
		{
			"object with additionalProperties schema",
			map[string]any{"additionalProperties": map[string]any{"type": "integer"}},
			"object",
			"z.record(z.string(), z.int())",
		},
		{
			"strict object with optional member",
			map[string]any{
				"properties": map[string]any{
					"a": map[string]any{"type": "string"},
					"b": map[string]any{"type": "integer"},
				},
				"required":             []any{"a"},
				"additionalProperties": false,
			},
			"object",
			"z.strictObject({\n  a: z.string(),\n  b: z.int().optional(),\n})",
		},
		{
			"open object",
			map[string]any{"properties": map[string]any{"a": map[string]any{"type": "boolean"}}},
			"object",
			"z.object({\n  a: z.boolean().optional(),\n})",
		},
		{"unknown type", map[string]any{}, "null", "z.unknown()"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if diff := cmp.Diff(tt.want, zodBase(tt.schema, tt.typ, "")); diff != "" {
				t.Errorf("zodBase() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
