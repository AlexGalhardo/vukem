package i18n

import (
	"fmt"
	"testing"
)

func TestPluralCategory(t *testing.T) {
	t.Parallel()

	tests := []struct {
		locale string
		want   map[float64]string
	}{
		{"en", map[float64]string{0: Other, 1: One, 2: Other, 1.5: Other, 11: Other, 1e6: Other}},
		{"de-AT", map[float64]string{1: One, 3: Other}},
		{"it", map[float64]string{1: One, 2: Other, 1e6: Many}},
		{"es", map[float64]string{1: One, 0: Other, 2: Other, 1.5: Other, 2e6: Many}},
		{"pt", map[float64]string{0: One, 1: One, 1.5: One, 2: Other, 1e6: Many}},
		{"pt-BR", map[float64]string{0: One, 2: Other}},
		{"pt-PT", map[float64]string{0: Other, 1: One, 1.5: Other, 1e6: Many}},
		{"fr", map[float64]string{0: One, 1: One, 1.9: One, 2: Other, 1e6: Many}},
		{"ru", map[float64]string{
			1: One, 21: One, 101: One, 2: Few, 4: Few, 22: Few, 5: Many, 11: Many, 12: Many, 14: Many, 20: Many,
			111: Many, 1.5: Other,
		}},
		{"uk", map[float64]string{1: One, 3: Few, 7: Many}},
		{"be", map[float64]string{31: One, 33: Few, 35: Many}},
		{"pl", map[float64]string{1: One, 2: Few, 24: Few, 5: Many, 12: Many, 21: Many, 0: Many, 1.5: Other}},
		{"cs", map[float64]string{1: One, 2: Few, 4: Few, 5: Other, 1.5: Many}},
		{
			"ar",
			map[float64]string{
				0:   Zero,
				1:   One,
				2:   Two,
				3:   Few,
				10:  Few,
				103: Few,
				11:  Many,
				99:  Many,
				100: Other,
				1.5: Other,
			},
		},
		{"he", map[float64]string{1: One, 2: Two, 3: Other, 0.5: One}},
		{"ja", map[float64]string{0: Other, 1: Other, 2: Other}},
		{"zh-Hant", map[float64]string{1: Other}},
		{"ko", map[float64]string{1: Other}},
		{"xx", map[float64]string{1: One, 2: Other}},
		{"", map[float64]string{1: One, 2: Other}},
	}

	for _, tt := range tests {
		for n, want := range tt.want {
			t.Run(fmt.Sprintf("%s/%v", tt.locale, n), func(t *testing.T) {
				t.Parallel()

				if got := PluralCategory(tt.locale, n); got != want {
					t.Errorf("PluralCategory(%q, %v) = %q, want %q", tt.locale, n, got, want)
				}

				if n != 0 {
					if got := PluralCategory(tt.locale, -n); got != want {
						t.Errorf("PluralCategory(%q, %v) = %q, want %q", tt.locale, -n, got, want)
					}
				}
			})
		}
	}
}
