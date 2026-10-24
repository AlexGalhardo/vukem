package factory

import "testing"

func TestPickWeighted(t *testing.T) {
	t.Parallel()

	items := []Weighted[string]{{"a", 7}, {"b", 3}, {"never", 0}}

	counts := map[string]int{}
	f := NewFaker(1)

	for range 10000 {
		counts[PickWeighted(f, items...)]++
	}

	if counts["never"] != 0 {
		t.Fatalf("zero-weight item picked %d times", counts["never"])
	}

	if a := counts["a"]; a < 6700 || a > 7300 {
		t.Fatalf("a picked %d of 10000, want about 7000", a)
	}

	g1, g2 := NewFaker(9), NewFaker(9)
	for range 50 {
		if x, y := PickWeighted(g1, items...), PickWeighted(g2, items...); x != y {
			t.Fatalf("same seed diverged: %q vs %q", x, y)
		}
	}
}

func TestPickWeightedPanics(t *testing.T) {
	t.Parallel()

	tests := map[string][]Weighted[int]{
		"empty":    nil,
		"negative": {{1, 5}, {2, -1}},
		"all zero": {{1, 0}, {2, 0}},
	}

	for name, items := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			defer func() {
				if recover() == nil {
					t.Fatal("PickWeighted did not panic")
				}
			}()

			PickWeighted(NewFaker(1), items...)
		})
	}
}
