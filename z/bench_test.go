package z

import (
	"encoding/json"
	"testing"
)

type benchAddress struct {
	City string `json:"city"`
}

type benchUser struct {
	Name    string       `json:"name"`
	Age     int          `json:"age"`
	Active  bool         `json:"active"`
	Role    string       `json:"role"`
	Tags    []string     `json:"tags"`
	Address benchAddress `json:"address"`
}

var benchSchema = Struct[benchUser](Shape{
	"name":    String().Trim().Min(2).Max(50),
	"age":     Int().Min(0).Max(150),
	"active":  Bool(),
	"role":    Enum("admin", "member"),
	"tags":    Slice(String().Max(20)).Max(10),
	"address": Struct[benchAddress](Shape{"city": String().Min(1)}),
})

func benchInput() map[string]any {
	return map[string]any{
		"name": "  Ana Souza ", "age": json.Number("31"), "active": true, "role": "member",
		"tags":    []any{"go", "web", "api"},
		"address": map[string]any{"city": "Recife"},
	}
}

func BenchmarkParseString(b *testing.B) {
	s := String().Trim().Email()

	b.ReportAllocs()

	for b.Loop() {
		if _, err := s.Parse("  ana@example.com "); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkParseStruct(b *testing.B) {
	in := benchInput()

	b.ReportAllocs()

	for b.Loop() {
		if _, err := benchSchema.Parse(in); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkParseSlice(b *testing.B) {
	items := make([]any, 100)
	for i := range items {
		items[i] = json.Number("7")
	}

	s := Slice(Int())

	b.ReportAllocs()

	for b.Loop() {
		if _, err := s.Parse(items); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkCheckStruct(b *testing.B) {
	v := benchUser{
		Name: "Ana Souza", Age: 31, Active: true, Role: "member",
		Tags: []string{"go", "web", "api"}, Address: benchAddress{City: "Recife"},
	}

	b.ReportAllocs()

	for b.Loop() {
		if err := benchSchema.Check(v); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkFor(b *testing.B) {
	For[benchUser]() // fill the cache

	b.ReportAllocs()

	for b.Loop() {
		_ = For[benchUser]()
	}
}
