package i18n

import "math"

// CLDR plural categories, the keys of a plural message.
const (
	Zero  = "zero"
	One   = "one"
	Two   = "two"
	Few   = "few"
	Many  = "many"
	Other = "other"
)

var categories = []string{Zero, One, Two, Few, Many, Other}

// PluralCategory returns the CLDR cardinal plural category of n for locale.
// Rules cover en, de, nl, sv, da, nb, fi, et, it, ca, es, el, hu, tr, bg,
// pt, pt-PT, fr, ru, uk, be, pl, cs, sk, ar, he, and ja, zh, ko, th, vi,
// id, ms (always "other"); other languages use the English rule.
//
// shortcut: n is a float64, so visible fraction digits ("1.0") and compact
// exponents are not modelled; the integer/fraction split of n is used.
func PluralCategory(locale string, n float64) string {
	n = math.Abs(n)
	i := int64(n)
	integer := n == math.Trunc(n)

	canon, _ := Canonical(locale)

	switch lang := baseLanguage(canon); lang {
	case "ja", "zh", "ko", "th", "vi", "id", "ms", "lo", "my", "km":
		return Other
	case "es", "el", "hu", "tr", "bg":
		if n == 1 {
			return One
		}

		if lang == "es" && millions(i, integer) {
			return Many
		}

		return Other
	case "pt":
		if canon == "pt-PT" {
			return oneIfOne(i, integer, millions(i, integer))
		}

		if i <= 1 {
			return One
		}

		return manyOr(millions(i, integer))
	case "fr":
		if i <= 1 {
			return One
		}

		return manyOr(millions(i, integer))
	case "it", "ca":
		return oneIfOne(i, integer, millions(i, integer))
	case "ru", "uk", "be":
		return eastSlavic(i, integer)
	case "pl":
		return polish(i, integer)
	case "cs", "sk":
		switch {
		case !integer:
			return Many
		case i == 1:
			return One
		case i >= 2 && i <= 4:
			return Few
		default:
			return Other
		}
	case "ar":
		return arabic(i, integer)
	case "he", "iw":
		switch {
		case i == 1 && integer, i == 0 && !integer:
			return One
		case i == 2 && integer:
			return Two
		default:
			return Other
		}
	default:
		return oneIfOne(i, integer, false)
	}
}

// millions is CLDR's "many" for es/fr/it/pt/ca: a non-zero integer
// multiple of 1,000,000 ("1 millón de usuarios").
func millions(i int64, integer bool) bool {
	return integer && i != 0 && i%1_000_000 == 0
}

func oneIfOne(i int64, integer, many bool) string {
	if i == 1 && integer {
		return One
	}

	return manyOr(many)
}

func manyOr(many bool) string {
	if many {
		return Many
	}

	return Other
}

func eastSlavic(i int64, integer bool) string {
	if !integer {
		return Other
	}

	mod10, mod100 := i%10, i%100

	switch {
	case mod10 == 1 && mod100 != 11:
		return One
	case mod10 >= 2 && mod10 <= 4 && (mod100 < 12 || mod100 > 14):
		return Few
	default:
		return Many
	}
}

func polish(i int64, integer bool) string {
	if !integer {
		return Other
	}

	mod10, mod100 := i%10, i%100

	switch {
	case i == 1:
		return One
	case mod10 >= 2 && mod10 <= 4 && (mod100 < 12 || mod100 > 14):
		return Few
	default:
		return Many
	}
}

func arabic(i int64, integer bool) string {
	if !integer {
		return Other
	}

	mod100 := i % 100

	switch {
	case i == 0:
		return Zero
	case i == 1:
		return One
	case i == 2:
		return Two
	case mod100 >= 3 && mod100 <= 10:
		return Few
	case mod100 >= 11:
		return Many
	default:
		return Other
	}
}
