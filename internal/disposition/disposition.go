// Package disposition builds safe Content-Disposition attachment headers
// for client-facing download names (export and web.Ctx.Download).
package disposition

import (
	"path"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	maxNameLen = 200
	fallback   = "download"
)

// Attachment returns an RFC 6266 attachment header for filename, sanitized
// by Sanitize. Non-ASCII names get an ASCII filename plus an RFC 5987
// filename*.
func Attachment(filename string) string {
	name := Sanitize(filename)

	ascii := strings.Map(func(r rune) rune {
		// Some browsers percent-decode filename, so % goes too.
		if r >= utf8.RuneSelf || r == '%' {
			return '_'
		}

		return r
	}, name)

	v := `attachment; filename="` + ascii + `"`
	if ascii != name {
		v += "; filename*=UTF-8''" + encode5987(name)
	}

	return v
}

// Sanitize returns a download-safe name: the last path element (either
// separator), without control (C0, C1, U+2028/2029) and format (bidi,
// zero-width) characters, with Windows-forbidden characters as "_", no
// leading dots, no trailing dots or spaces, a "_" before Windows device
// names, at most 200 characters keeping the extension; "download" when
// nothing is left.
func Sanitize(name string) string {
	name = path.Base(strings.ReplaceAll(name, `\`, "/"))

	name = strings.Map(func(r rune) rune {
		switch {
		case unicode.IsControl(r), r == '\u2028', r == '\u2029', r == utf8.RuneError, unicode.Is(unicode.Cf, r):
			return -1
		case strings.ContainsRune(`<>:"/|?*`, r):
			return '_'
		}

		return r
	}, name)

	name = strings.TrimRight(strings.TrimLeft(strings.TrimSpace(name), "."), ". ")

	if utf8.RuneCountInString(name) > maxNameLen {
		ext := path.Ext(name)
		if utf8.RuneCountInString(ext) > 16 {
			ext = ""
		}

		runes := []rune(strings.TrimSuffix(name, ext))
		name = string(runes[:maxNameLen-utf8.RuneCountInString(ext)]) + ext
	}

	if name == "" {
		return fallback
	}

	if reserved(name) {
		name = "_" + name
	}

	return name
}

// reserved reports whether name is a Windows device name, which Windows
// opens as the device whatever the extension ("NUL.csv", "com1.txt").
func reserved(name string) bool {
	stem, _, _ := strings.Cut(name, ".")
	stem = strings.ToUpper(strings.TrimRight(stem, " "))

	switch stem {
	case "CON", "PRN", "AUX", "NUL":
		return true
	}

	if len(stem) == 4 && (strings.HasPrefix(stem, "COM") || strings.HasPrefix(stem, "LPT")) {
		return stem[3] >= '1' && stem[3] <= '9'
	}

	return false
}

// encode5987 percent-encodes every byte outside attr-char (RFC 5987 §3.2.1).
func encode5987(s string) string {
	const hex = "0123456789ABCDEF"

	var b strings.Builder

	for i := range len(s) {
		c := s[i]
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' ||
			strings.IndexByte("!#$&+-.^_`|~", c) >= 0 {
			b.WriteByte(c)

			continue
		}

		b.WriteByte('%')
		b.WriteByte(hex[c>>4])
		b.WriteByte(hex[c&0x0f])
	}

	return b.String()
}
