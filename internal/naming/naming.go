// Package naming converts identifiers between Go and database/file
// conventions. The ORM and the CLI generators share it so table names and
// generated files always agree.
package naming

import (
	"strings"
	"unicode"
)

// Snake converts "UserProfile" to "user_profile" and "HTTPCode" to "http_code".
func Snake(s string) string {
	var b strings.Builder

	runes := []rune(s)
	for i, r := range runes {
		switch {
		case r == '-' || r == ' ':
			r = '_'
		case unicode.IsUpper(r):
			if i > 0 && runes[i-1] != '_' &&
				(unicode.IsLower(runes[i-1]) || unicode.IsDigit(runes[i-1]) ||
					i+1 < len(runes) && unicode.IsLower(runes[i+1])) {
				b.WriteByte('_')
			}

			r = unicode.ToLower(r)
		}

		b.WriteRune(r)
	}

	return b.String()
}

// Kebab converts "SendReports" to "send-reports".
func Kebab(s string) string { return strings.ReplaceAll(Snake(s), "_", "-") }

// Pascal converts "user_profile", "user-profile" or "userProfile" to "UserProfile".
func Pascal(s string) string {
	var b strings.Builder

	for word := range strings.SplitSeq(Snake(s), "_") {
		if word == "" {
			continue
		}

		r := []rune(word)
		b.WriteRune(unicode.ToUpper(r[0]))
		b.WriteString(string(r[1:]))
	}

	return b.String()
}

// Camel converts "user_profile" to "userProfile".
func Camel(s string) string {
	p := []rune(Pascal(s))
	if len(p) == 0 {
		return ""
	}

	p[0] = unicode.ToLower(p[0])

	return string(p)
}

// Plural applies common English rules to the last word.
func Plural(s string) string {
	switch {
	case strings.HasSuffix(s, "y") && len(s) > 1 && !strings.ContainsRune("aeiou", rune(s[len(s)-2])):
		return s[:len(s)-1] + "ies"
	case strings.HasSuffix(s, "s"), strings.HasSuffix(s, "x"), strings.HasSuffix(s, "z"),
		strings.HasSuffix(s, "ch"), strings.HasSuffix(s, "sh"):
		return s + "es"
	default:
		return s + "s"
	}
}
