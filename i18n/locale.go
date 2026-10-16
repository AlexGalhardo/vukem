package i18n

import (
	"slices"
	"strconv"
	"strings"
)

// maxAcceptLanguages bounds the work done per Accept-Language header.
const maxAcceptLanguages = 16

// Canonical validates a locale tag and returns its canonical form:
// language (2-3 letters, lower case), optional script (4 letters, title
// case) and optional region (2 letters upper case, or 3 digits), joined by
// "-" ("pt_br" → "pt-BR", "zh-hant-tw" → "zh-Hant-TW"). Variants,
// extensions and anything else are rejected, so a canonical tag is always
// safe in headers, cookies and file names.
//
// shortcut: no BCP 47 variants/extensions (en-US-u-ca-...); add them if an
// app needs them.
func Canonical(tag string) (string, bool) {
	if tag == "" || len(tag) > len("fil-Latn-419") {
		return "", false
	}

	parts := strings.FieldsFunc(tag, func(r rune) bool { return r == '-' || r == '_' })
	if len(parts) == 0 || len(parts) > 3 || strings.Count(tag, "-")+strings.Count(tag, "_") != len(parts)-1 {
		return "", false
	}

	lang := parts[0]
	if len(lang) < 2 || len(lang) > 3 || !isAlpha(lang) {
		return "", false
	}

	out := []string{strings.ToLower(lang)}
	rest := parts[1:]

	if len(rest) > 0 && len(rest[0]) == 4 && isAlpha(rest[0]) {
		out = append(out, strings.ToUpper(rest[0][:1])+strings.ToLower(rest[0][1:]))
		rest = rest[1:]
	}

	if len(rest) > 0 {
		switch r := rest[0]; {
		case len(r) == 2 && isAlpha(r):
			out = append(out, strings.ToUpper(r))
		case len(r) == 3 && isDigits(r):
			out = append(out, r)
		default:
			return "", false
		}

		rest = rest[1:]
	}

	if len(rest) > 0 {
		return "", false
	}

	return strings.Join(out, "-"), true
}

func isAlpha(s string) bool {
	for _, c := range []byte(s) {
		if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') {
			return false
		}
	}

	return true
}

func isDigits(s string) bool {
	for _, c := range []byte(s) {
		if c < '0' || c > '9' {
			return false
		}
	}

	return true
}

// truncations returns a canonical tag and its shorter forms:
// "zh-Hant-TW" → ["zh-Hant-TW", "zh-Hant", "zh"].
func truncations(canon string) []string {
	out := []string{canon}

	for i := strings.LastIndexByte(canon, '-'); i > 0; i = strings.LastIndexByte(canon, '-') {
		canon = canon[:i]
		out = append(out, canon)
	}

	return out
}

func baseLanguage(canon string) string {
	lang, _, _ := strings.Cut(canon, "-")
	return lang
}

// parseAcceptLanguage returns the valid tags of an Accept-Language header
// (RFC 9110 §12.5.4) ordered by quality, ties in header order. Tags with
// q=0, invalid q-values or invalid tags, and "*" are dropped.
func parseAcceptLanguage(header string) []string {
	type weighted struct {
		tag string
		q   float64
	}

	var items []weighted

	for part := range strings.SplitSeq(header, ",") {
		if len(items) == maxAcceptLanguages {
			break
		}

		tag, params, _ := strings.Cut(part, ";")
		tag = strings.TrimSpace(tag)

		q, ok := quality(params)
		if !ok || q == 0 || tag == "*" {
			continue
		}

		canon, ok := Canonical(tag)
		if !ok || slices.ContainsFunc(items, func(w weighted) bool { return w.tag == canon }) {
			continue
		}

		items = append(items, weighted{canon, q})
	}

	slices.SortStableFunc(items, func(a, b weighted) int {
		switch {
		case a.q > b.q:
			return -1
		case a.q < b.q:
			return 1
		default:
			return 0
		}
	})

	out := make([]string, 0, len(items))
	for _, w := range items {
		out = append(out, w.tag)
	}

	if len(out) == 0 {
		return nil
	}

	return out
}

// quality parses the "q=0.8" weight of an Accept-Language entry (1 when
// absent).
func quality(params string) (float64, bool) {
	params = strings.TrimSpace(params)
	if params == "" {
		return 1, true
	}

	name, value, ok := strings.Cut(params, "=")
	if !ok || !strings.EqualFold(strings.TrimSpace(name), "q") {
		return 0, false
	}

	value = strings.TrimSpace(value)
	if len(value) > 5 {
		return 0, false
	}

	q, err := strconv.ParseFloat(value, 64)
	if err != nil || q < 0 || q > 1 {
		return 0, false
	}

	return q, true
}
