package suggest

import "testing"

func TestClosest(t *testing.T) {
	t.Parallel()

	candidates := []string{"migrate", "route:list", "serve", "queue:work"}

	tests := []struct {
		name, want string
	}{
		{"migrte", "migrate"},
		{"route:lst", "route:list"},
		{"servee", "serve"},
		{"qeueu:work", "queue:work"},
		{"zzzzzz", ""},
		{"x", ""},
		{"", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := Closest(tt.name, candidates); got != tt.want {
				t.Errorf("Closest(%q) = %q, want %q", tt.name, got, tt.want)
			}
		})
	}

	if got := Closest("migrte", nil); got != "" {
		t.Errorf("Closest with no candidates = %q, want empty", got)
	}
}

func TestDistance(t *testing.T) {
	t.Parallel()

	tests := []struct {
		a, b string
		want int
	}{
		{"", "", 0},
		{"abc", "abc", 0},
		{"abc", "", 3},
		{"kitten", "sitting", 3},
		{"ab", "ba", 1}, // adjacent transposition is one edit
		{"migrte", "migrate", 1},
	}

	for _, tt := range tests {
		if got := Distance(tt.a, tt.b); got != tt.want {
			t.Errorf("Distance(%q, %q) = %d, want %d", tt.a, tt.b, got, tt.want)
		}
	}
}
