package z

import "testing"

func TestStringSchema_HasClasses(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		schema StringSchema
		key    string
		ok     []string
		bad    []string
	}{
		{"lower", String().HasLower(), "string.has_lower", []string{"aB", "ção", "ñ"}, []string{"AB", "123", "", "日本"}},
		{"upper", String().HasUpper(), "string.has_upper", []string{"aB", "Ção", "Ñ"}, []string{"ab", "123", "", "日本"}},
		{"digit", String().HasDigit(), "string.has_digit", []string{"a1", "٣"}, []string{"abc", "", "½"}},
		{
			"symbol",
			String().HasSymbol(),
			"string.has_symbol",
			[]string{"a!", "a$", "a€", "a_", "a-"},
			[]string{"abc1", "a b", ""},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			for _, in := range tt.ok {
				if _, err := tt.schema.Parse(in); err != nil {
					t.Errorf("Parse(%q) = %v, want nil", in, err)
				}
			}

			for _, in := range tt.bad {
				_, err := tt.schema.Parse(in)

				is, ok := AsIssues(err)
				if !ok || len(is) != 1 || is[0].Code != CodeInvalidString || is[0].Key != tt.key {
					t.Errorf("Parse(%q) = %v, want one %s issue with key %s", in, err, CodeInvalidString, tt.key)
				}
			}
		})
	}
}

func TestStringSchema_HasClassesCombine(t *testing.T) {
	t.Parallel()

	s := String().Min(8).HasLower().HasUpper().HasDigit().HasSymbol()

	if _, err := s.Parse("Abcdef1!"); err != nil {
		t.Fatalf("Parse(strong) = %v, want nil", err)
	}

	_, err := s.Parse("abcdefgh")

	is, _ := AsIssues(err)
	if len(is) != 3 {
		t.Fatalf("Parse(weak) = %v, want 3 issues (upper, digit, symbol)", err)
	}
}
