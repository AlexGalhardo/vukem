// Package config loads typed application configuration from environment
// variables and .env files.
//
// Values are resolved with this precedence, highest first: the process
// environment, .env.<APP_ENV>.local, .env.local (skipped when APP_ENV=test),
// .env.<APP_ENV>, .env. Structs are filled with [Fill] using `env` and
// `default` struct tags.
package config

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strings"
)

// Entry is one KEY=VALUE pair parsed from a .env file.
type Entry struct {
	Key   string
	Value string
	// Expand reports whether ${VAR} references in Value must be expanded.
	// Single-quoted values are literal.
	Expand bool
}

// ParseEnv parses .env syntax: KEY=VALUE lines, optional "export " prefix,
// # comments, single-quoted literals and double-quoted values with escapes
// (which may span lines).
func ParseEnv(r io.Reader) ([]Entry, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)

	var (
		entries []Entry
		lineNo  int
	)

	for sc.Scan() {
		lineNo++
		start := lineNo
		line := strings.TrimSpace(strings.TrimSuffix(sc.Text(), "\r"))

		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		line = strings.TrimPrefix(line, "export ")

		key, rest, ok := strings.Cut(line, "=")
		if !ok {
			return nil, fmt.Errorf("line %d: expected KEY=VALUE", start)
		}

		key = strings.TrimSpace(key)
		if !validKey(key) {
			return nil, fmt.Errorf("line %d: invalid key %q", start, key)
		}

		rest = strings.TrimLeft(rest, " \t")

		var (
			e   = Entry{Key: key, Expand: true}
			err error
		)

		switch {
		case strings.HasPrefix(rest, "'"):
			e.Expand = false
			e.Value, err = singleQuoted(rest)
		case strings.HasPrefix(rest, `"`):
			// Double-quoted values may continue on the following lines.
			for !closedDoubleQuote(rest) && sc.Scan() {
				lineNo++
				rest += "\n" + strings.TrimSuffix(sc.Text(), "\r")
			}
			e.Value, err = doubleQuoted(rest)
		default:
			e.Value = unquoted(rest)
		}

		if err != nil {
			return nil, fmt.Errorf("line %d: %w", start, err)
		}

		entries = append(entries, e)
	}

	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("read env: %w", err)
	}

	return entries, nil
}

func validKey(k string) bool {
	if k == "" {
		return false
	}

	for i, r := range k {
		switch {
		case r == '_', r >= 'A' && r <= 'Z', r >= 'a' && r <= 'z':
		case i > 0 && (r >= '0' && r <= '9' || r == '.'):
		default:
			return false
		}
	}

	return true
}

func unquoted(s string) string {
	if i := strings.Index(s, " #"); i >= 0 {
		s = s[:i]
	}

	if i := strings.Index(s, "\t#"); i >= 0 {
		s = s[:i]
	}

	return strings.TrimSpace(s)
}

func singleQuoted(s string) (string, error) {
	end := strings.Index(s[1:], "'")
	if end < 0 {
		return "", errors.New("unterminated single-quoted value")
	}

	if err := checkTrailing(s[end+2:]); err != nil {
		return "", err
	}

	return s[1 : end+1], nil
}

// closedDoubleQuote reports whether s (starting with a quote) contains an
// unescaped closing quote.
func closedDoubleQuote(s string) bool {
	_, ok := doubleQuoteEnd(s)
	return ok
}

func doubleQuoteEnd(s string) (int, bool) {
	for i := 1; i < len(s); i++ {
		switch s[i] {
		case '\\':
			i++
		case '"':
			return i, true
		}
	}

	return 0, false
}

func doubleQuoted(s string) (string, error) {
	end, ok := doubleQuoteEnd(s)
	if !ok {
		return "", errors.New("unterminated double-quoted value")
	}

	if err := checkTrailing(s[end+1:]); err != nil {
		return "", err
	}

	var b strings.Builder

	body := s[1:end]
	for i := 0; i < len(body); i++ {
		c := body[i]
		if c != '\\' || i == len(body)-1 {
			b.WriteByte(c)
			continue
		}

		i++
		switch body[i] {
		case 'n':
			b.WriteByte('\n')
		case 'r':
			b.WriteByte('\r')
		case 't':
			b.WriteByte('\t')
		case '"', '\\':
			b.WriteByte(body[i])
		case '$':
			// \$ is kept escaped so expansion leaves it as a literal "$".
			b.WriteString(`\$`)
		default:
			b.WriteByte('\\')
			b.WriteByte(body[i])
		}
	}

	return b.String(), nil
}

func checkTrailing(s string) error {
	s = strings.TrimSpace(s)
	if s == "" || strings.HasPrefix(s, "#") {
		return nil
	}

	return errors.New("unexpected characters after quoted value")
}
