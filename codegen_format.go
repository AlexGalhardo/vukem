package vukem

import (
	"regexp"
	"slices"
	"strings"
)

// The generator writes TypeScript that is already what Biome formats (the
// config vukem new writes: 120 columns, 2 spaces, double quotes), so a file it
// wrote stays stable under `biome format` and "codegen -check" does not flip
// after a format run. The generator emits one declaration per line, so the
// only lines that can pass the width are a type alias or a property; these
// functions break them the way Biome 2 does:
//
//   - a union that does not fit goes one "| member" per line, after the "=" of
//     an alias or the ":" of a property, except "Name | null" which stays whole;
//   - an object alias whose "export type X = {" line is too long moves the
//     object under the "=", indented;
//   - any other alias that is too long breaks after the "=";
//   - "Record<K, V>" that does not fit puts its arguments one per line.
//
// A single name longer than the width cannot be broken, and is left as it is.

// wrapLongUnions applies those rules to src for the given line width.
func wrapLongUnions(src string, width int) string {
	lines := strings.Split(src, "\n")
	lines = indentLongObjectAliases(lines, width)
	out := make([]string, 0, len(lines))

	for _, line := range lines {
		out = append(out, wrapLine(line, width)...)
	}

	return strings.Join(out, "\n")
}

var objectAliasRe = regexp.MustCompile(`^export type \S+ = \{$`)

// indentLongObjectAliases moves the body of "export type X = {" under the "="
// when that first line is wider than width:
//
//	export type X =
//	  {
//	    a: string;
//	  };
func indentLongObjectAliases(lines []string, width int) []string {
	out := make([]string, 0, len(lines))

	for i := 0; i < len(lines); i++ {
		line := lines[i]
		if !objectAliasRe.MatchString(line) || len([]rune(line)) <= width {
			out = append(out, line)
			continue
		}

		end := slices.Index(lines[i:], "};")
		if end < 0 {
			out = append(out, line)
			continue
		}

		out = append(out, strings.TrimSuffix(line, " {"), "  {")

		for _, body := range lines[i+1 : i+end] {
			if body == "" {
				out = append(out, body)
			} else {
				out = append(out, "  "+body)
			}
		}

		out = append(out, "  };")
		i += end
	}

	return out
}

// simpleNameRe is a type reference or a keyword type: what Biome keeps next to
// "| null" on one line.
var simpleNameRe = regexp.MustCompile(`^[A-Za-z_$][A-Za-z0-9_$]*$`)

func wrapLine(line string, width int) []string {
	if len([]rune(line)) <= width || !strings.HasSuffix(line, ";") {
		return []string{line}
	}

	trimmed := strings.TrimLeft(line, " ")
	indent := line[:len(line)-len(trimmed)]

	var head, value string

	alias := strings.HasPrefix(trimmed, "export type ")

	switch {
	case alias:
		i := strings.Index(trimmed, " = ")
		if i < 0 {
			return []string{line}
		}

		head, value = trimmed[:i+2], trimmed[i+3:]
	default:
		i := strings.Index(trimmed, ": ")
		if i < 0 {
			return []string{line}
		}

		head, value = trimmed[:i+1], trimmed[i+2:]
	}

	value = strings.TrimSuffix(value, ";")

	if members := splitTopLevelUnion(value); len(members) >= 2 {
		// Biome keeps "Name | null" whole, whatever its width.
		if len(members) == 2 && slices.Contains(members, "null") &&
			simpleNameRe.MatchString(otherThan(members, "null")) {
			return []string{line}
		}

		out := []string{indent + head}
		for _, m := range members {
			out = append(out, indent+"  | "+m)
		}

		out[len(out)-1] += ";"

		return out
	}

	if alias {
		// Only the part after the "=" can still be broken: it moves to its own line.
		return append([]string{indent + head}, wrapGeneric(indent+"  ", "", value+";", width)...)
	}

	return wrapGeneric(indent, head+" ", value+";", width)
}

func otherThan(members []string, not string) string {
	for _, m := range members {
		if m != not {
			return m
		}
	}

	return ""
}

// wrapGeneric writes prefix+value (value ends in ";" or ","), breaking a
// "Name<A, B>" that does not fit into one argument per line, recursively.
func wrapGeneric(indent, prefix, value string, width int) []string {
	line := indent + prefix + value
	if len([]rune(line)) <= width {
		return []string{line}
	}

	end := value[len(value)-1:]
	body := strings.TrimSuffix(value, end)

	open := strings.Index(body, "<")
	if open <= 0 || !strings.HasSuffix(body, ">") {
		return []string{line}
	}

	args := splitTopLevel(body[open+1:len(body)-1], ", ")
	if len(args) < 2 {
		return []string{line}
	}

	out := []string{indent + prefix + body[:open+1]}

	for i, a := range args {
		sep := ","
		if i == len(args)-1 {
			sep = ""
		}

		out = append(out, wrapArg(indent+"  ", a, sep, width)...)
	}

	return append(out, indent+">"+end)
}

// wrapArg writes one type argument: whole when it fits, otherwise a union one
// member per line or a generic one argument per line.
func wrapArg(indent, arg, sep string, width int) []string {
	if len([]rune(indent+arg+sep)) <= width {
		return []string{indent + arg + sep}
	}

	if members := splitTopLevelUnion(arg); len(members) >= 2 {
		out := make([]string, len(members))
		for i, m := range members {
			out[i] = indent + "| " + m
		}

		out[len(out)-1] += sep

		return out
	}

	return wrapGeneric(indent, "", arg+sep, width)
}

// splitTopLevelUnion splits s at " | " outside brackets, angle brackets,
// parentheses and string literals.
func splitTopLevelUnion(s string) []string {
	return splitTopLevel(s, " | ")
}

// splitTopLevel splits s at sep (a two or three character separator) outside
// brackets, angle brackets, parentheses and string literals.
func splitTopLevel(s, sep string) []string {
	var (
		parts   []string
		depth   int
		quote   rune
		escaped bool
		start   int
	)

	rs := []rune(s)
	sr := []rune(sep)

	for i := 0; i < len(rs); i++ {
		r := rs[i]

		switch {
		case quote != 0:
			switch {
			case escaped:
				escaped = false
			case r == '\\':
				escaped = true
			case r == quote:
				quote = 0
			}
		case r == '"' || r == '\'':
			quote = r
		case strings.ContainsRune("{[(<", r):
			depth++
		case strings.ContainsRune("}])>", r):
			depth--
		case depth == 0 && i+len(sr) <= len(rs) && string(rs[i:i+len(sr)]) == sep:
			parts = append(parts, string(rs[start:i]))
			start = i + len(sr)
			i += len(sr) - 1
		}
	}

	return append(parts, string(rs[start:]))
}
