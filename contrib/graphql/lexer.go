package graphql

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

type tokenKind uint8

const (
	tokEOF tokenKind = iota
	tokBang
	tokDollar
	tokAmp
	tokParenL
	tokParenR
	tokSpread
	tokColon
	tokEquals
	tokAt
	tokBracketL
	tokBracketR
	tokBraceL
	tokPipe
	tokBraceR
	tokName
	tokInt
	tokFloat
	tokString
	tokBlockString
)

var tokenNames = [...]string{
	tokEOF: "<EOF>", tokBang: "!", tokDollar: "$", tokAmp: "&", tokParenL: "(", tokParenR: ")",
	tokSpread: "...", tokColon: ":", tokEquals: "=", tokAt: "@", tokBracketL: "[", tokBracketR: "]",
	tokBraceL: "{", tokPipe: "|", tokBraceR: "}", tokName: "Name", tokInt: "Int", tokFloat: "Float",
	tokString: "String", tokBlockString: "BlockString",
}

func (k tokenKind) String() string { return tokenNames[k] }

type token struct {
	kind  tokenKind
	value string // names, numbers and decoded strings
	pos   Location
}

func (t token) describe() string {
	switch t.kind {
	case tokName, tokInt, tokFloat:
		return fmt.Sprintf("%s %q", t.kind, t.value)
	case tokString, tokBlockString:
		return t.kind.String()
	default:
		return fmt.Sprintf("%q", t.kind.String())
	}
}

// lexer turns a GraphQL source into tokens (spec §2.1). Ignored tokens
// (whitespace, commas, comments, BOM) are skipped.
type lexer struct {
	src       string
	off       int
	line      int
	lineStart int
	count     int
	maxTokens int
}

func newLexer(src string, maxTokens int) *lexer {
	return &lexer{src: src, line: 1, maxTokens: maxTokens}
}

func (l *lexer) loc(off int) Location {
	return Location{Line: l.line, Column: utf8.RuneCountInString(l.src[l.lineStart:off]) + 1}
}

func (l *lexer) errorf(off int, format string, args ...any) *Error {
	return syntaxError(l.loc(off), fmt.Sprintf(format, args...))
}

func (l *lexer) skipIgnored() {
	for l.off < len(l.src) {
		c := l.src[l.off]
		switch {
		case c == ' ' || c == '\t' || c == ',':
			l.off++
		case c == '\n':
			l.off++
			l.newline()
		case c == '\r':
			l.off++
			if l.off < len(l.src) && l.src[l.off] == '\n' {
				l.off++
			}

			l.newline()
		case c == '#':
			for l.off < len(l.src) && l.src[l.off] != '\n' && l.src[l.off] != '\r' {
				l.off++
			}
		case strings.HasPrefix(l.src[l.off:], "\uFEFF"):
			l.off += len("\uFEFF")
		default:
			return
		}
	}
}

func (l *lexer) newline() {
	l.line++
	l.lineStart = l.off
}

func (l *lexer) next() (token, error) {
	l.skipIgnored()

	start := l.off
	if start >= len(l.src) {
		return token{kind: tokEOF, pos: l.loc(start)}, nil
	}

	l.count++
	if l.maxTokens > 0 && l.count > l.maxTokens {
		return token{}, &Error{
			Message:    fmt.Sprintf("Syntax Error: document exceeds the limit of %d tokens.", l.maxTokens),
			Locations:  []Location{l.loc(start)},
			Extensions: map[string]any{"code": CodeParseFailed},
		}
	}

	c := l.src[start]
	if k, ok := punctuator(c); ok {
		l.off++
		return token{kind: k, pos: l.loc(start)}, nil
	}

	switch {
	case c == '.':
		if strings.HasPrefix(l.src[start:], "...") {
			l.off += 3
			return token{kind: tokSpread, pos: l.loc(start)}, nil
		}

		return token{}, l.errorf(start, "Unexpected %q.", ".")
	case isNameStart(c):
		for l.off < len(l.src) && isNameContinue(l.src[l.off]) {
			l.off++
		}

		return token{kind: tokName, value: l.src[start:l.off], pos: l.loc(start)}, nil
	case c == '-' || isDigit(c):
		return l.number()
	case c == '"':
		if strings.HasPrefix(l.src[start:], `"""`) {
			return l.blockString()
		}

		return l.string()
	}

	r, _ := utf8.DecodeRuneInString(l.src[start:])

	return token{}, l.errorf(start, "Unexpected character %q.", r)
}

func punctuator(c byte) (tokenKind, bool) {
	switch c {
	case '!':
		return tokBang, true
	case '$':
		return tokDollar, true
	case '&':
		return tokAmp, true
	case '(':
		return tokParenL, true
	case ')':
		return tokParenR, true
	case ':':
		return tokColon, true
	case '=':
		return tokEquals, true
	case '@':
		return tokAt, true
	case '[':
		return tokBracketL, true
	case ']':
		return tokBracketR, true
	case '{':
		return tokBraceL, true
	case '|':
		return tokPipe, true
	case '}':
		return tokBraceR, true
	}

	return 0, false
}

func isNameStart(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func isNameContinue(c byte) bool { return isNameStart(c) || isDigit(c) }

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

func (l *lexer) number() (token, error) {
	start := l.off
	kind := tokInt

	if l.src[l.off] == '-' {
		l.off++
	}

	if l.off < len(l.src) && l.src[l.off] == '0' {
		l.off++
		if l.off < len(l.src) && isDigit(l.src[l.off]) {
			return token{}, l.errorf(l.off, "Invalid number, unexpected digit after 0: %q.", l.src[l.off])
		}
	} else if err := l.digits(); err != nil {
		return token{}, err
	}

	if l.off < len(l.src) && l.src[l.off] == '.' {
		kind = tokFloat
		l.off++

		if err := l.digits(); err != nil {
			return token{}, err
		}
	}

	if l.off < len(l.src) && (l.src[l.off] == 'e' || l.src[l.off] == 'E') {
		kind = tokFloat
		l.off++

		if l.off < len(l.src) && (l.src[l.off] == '+' || l.src[l.off] == '-') {
			l.off++
		}

		if err := l.digits(); err != nil {
			return token{}, err
		}
	}

	// A number must not be directly followed by a name start or a dot.
	if l.off < len(l.src) && (l.src[l.off] == '.' || isNameStart(l.src[l.off])) {
		return token{}, l.errorf(l.off, "Invalid number, expected digit but got: %q.", l.src[l.off])
	}

	return token{kind: kind, value: l.src[start:l.off], pos: l.loc(start)}, nil
}

func (l *lexer) digits() error {
	if l.off >= len(l.src) || !isDigit(l.src[l.off]) {
		if l.off >= len(l.src) {
			return l.errorf(l.off, "Invalid number, expected digit but got: <EOF>.")
		}

		return l.errorf(l.off, "Invalid number, expected digit but got: %q.", l.src[l.off])
	}

	for l.off < len(l.src) && isDigit(l.src[l.off]) {
		l.off++
	}

	return nil
}

func (l *lexer) string() (token, error) {
	start := l.off
	l.off++

	var b strings.Builder

	for l.off < len(l.src) {
		c := l.src[l.off]

		switch {
		case c == '"':
			l.off++
			return token{kind: tokString, value: b.String(), pos: l.loc(start)}, nil
		case c == '\n' || c == '\r':
			return token{}, l.errorf(l.off, "Unterminated string.")
		case c == '\\':
			if err := l.escape(&b); err != nil {
				return token{}, err
			}
		case c < 0x20 && c != '\t':
			return token{}, l.errorf(l.off, "Invalid character within String: %q.", rune(c))
		default:
			r, size := utf8.DecodeRuneInString(l.src[l.off:])
			if r == utf8.RuneError && size == 1 {
				return token{}, l.errorf(l.off, "Invalid UTF-8 within String.")
			}

			b.WriteString(l.src[l.off : l.off+size])
			l.off += size
		}
	}

	return token{}, l.errorf(l.off, "Unterminated string.")
}

func (l *lexer) escape(b *strings.Builder) error {
	at := l.off
	l.off++

	if l.off >= len(l.src) {
		return l.errorf(at, "Unterminated string.")
	}

	c := l.src[l.off]
	l.off++

	switch c {
	case '"', '\\', '/':
		b.WriteByte(c)
	case 'b':
		b.WriteByte('\b')
	case 'f':
		b.WriteByte('\f')
	case 'n':
		b.WriteByte('\n')
	case 'r':
		b.WriteByte('\r')
	case 't':
		b.WriteByte('\t')
	case 'u':
		r, err := l.unicodeEscape(at)
		if err != nil {
			return err
		}

		b.WriteRune(r)
	default:
		return l.errorf(at, "Invalid character escape sequence: \"\\%c\".", c)
	}

	return nil
}

// unicodeEscape reads \uXXXX, \u{X…} and surrogate pairs \uD83D\uDE00.
func (l *lexer) unicodeEscape(at int) (rune, error) {
	if l.off < len(l.src) && l.src[l.off] == '{' {
		end := strings.IndexByte(l.src[l.off:], '}')
		if end < 2 {
			return 0, l.errorf(at, "Invalid Unicode escape sequence.")
		}

		n, ok := hexRune(l.src[l.off+1 : l.off+end])
		if !ok || n > utf8.MaxRune || (n >= 0xD800 && n <= 0xDFFF) {
			return 0, l.errorf(at, "Invalid Unicode escape sequence: %q.", l.src[at:l.off+end+1])
		}

		l.off += end + 1

		return n, nil
	}

	hi, ok := l.hex4()
	if !ok {
		return 0, l.errorf(at, "Invalid Unicode escape sequence.")
	}

	switch {
	case hi >= 0xD800 && hi <= 0xDBFF:
		if strings.HasPrefix(l.src[l.off:], `\u`) {
			save := l.off
			l.off += 2

			if lo, ok := l.hex4(); ok && lo >= 0xDC00 && lo <= 0xDFFF {
				return (hi-0xD800)<<10 + (lo - 0xDC00) + 0x10000, nil
			}

			l.off = save
		}

		return 0, l.errorf(at, "Invalid Unicode escape sequence: unpaired surrogate.")
	case hi >= 0xDC00 && hi <= 0xDFFF:
		return 0, l.errorf(at, "Invalid Unicode escape sequence: unpaired surrogate.")
	}

	return hi, nil
}

func (l *lexer) hex4() (rune, bool) {
	if l.off+4 > len(l.src) {
		return 0, false
	}

	n, ok := hexRune(l.src[l.off : l.off+4])
	if !ok {
		return 0, false
	}

	l.off += 4

	return n, true
}

// hexRune decodes 1–6 hex digits (more cannot be a code point).
func hexRune(s string) (rune, bool) {
	if s == "" || len(s) > 6 {
		return 0, false
	}

	var n rune

	for i := range len(s) {
		c := s[i]

		switch {
		case isDigit(c):
			n = n<<4 | rune(c-'0')
		case c >= 'a' && c <= 'f':
			n = n<<4 | rune(c-'a'+10)
		case c >= 'A' && c <= 'F':
			n = n<<4 | rune(c-'A'+10)
		default:
			return 0, false
		}
	}

	return n, true
}

func (l *lexer) blockString() (token, error) {
	start := l.off
	startLoc := l.loc(start)
	l.off += 3

	var raw strings.Builder

	for l.off < len(l.src) {
		switch {
		case strings.HasPrefix(l.src[l.off:], `"""`):
			l.off += 3
			return token{kind: tokBlockString, value: blockStringValue(raw.String()), pos: startLoc}, nil
		case strings.HasPrefix(l.src[l.off:], `\"""`):
			raw.WriteString(`"""`)
			l.off += 4
		case l.src[l.off] == '\n':
			raw.WriteByte('\n')
			l.off++
			l.newline()
		case l.src[l.off] == '\r':
			raw.WriteByte('\n')
			l.off++

			if l.off < len(l.src) && l.src[l.off] == '\n' {
				l.off++
			}

			l.newline()
		case l.src[l.off] < 0x20 && l.src[l.off] != '\t':
			return token{}, l.errorf(l.off, "Invalid character within String: %q.", rune(l.src[l.off]))
		default:
			r, size := utf8.DecodeRuneInString(l.src[l.off:])
			if r == utf8.RuneError && size == 1 {
				return token{}, l.errorf(l.off, "Invalid UTF-8 within String.")
			}

			raw.WriteString(l.src[l.off : l.off+size])
			l.off += size
		}
	}

	return token{}, l.errorf(l.off, "Unterminated string.")
}

// blockStringValue implements the spec's BlockStringValue(): common
// indentation and leading/trailing blank lines are removed.
func blockStringValue(raw string) string {
	lines := strings.Split(raw, "\n")

	common := -1

	for i, line := range lines {
		if i == 0 {
			continue
		}

		indent := len(line) - len(strings.TrimLeft(line, " \t"))
		if indent < len(line) && (common < 0 || indent < common) {
			common = indent
		}
	}

	if common > 0 {
		for i := 1; i < len(lines); i++ {
			if len(lines[i]) >= common {
				lines[i] = lines[i][common:]
			} else {
				lines[i] = ""
			}
		}
	}

	blank := func(s string) bool { return strings.Trim(s, " \t") == "" }
	for len(lines) > 0 && blank(lines[0]) {
		lines = lines[1:]
	}

	for len(lines) > 0 && blank(lines[len(lines)-1]) {
		lines = lines[:len(lines)-1]
	}

	return strings.Join(lines, "\n")
}
