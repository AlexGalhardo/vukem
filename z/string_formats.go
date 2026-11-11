package z

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"html"
	"net"
	"net/netip"
	"regexp"
	"strings"
	"time"
)

// None of the formats below does I/O: they only read the string.

var (
	e164Re     = regexp.MustCompile(`^\+[1-9]\d{6,14}$`)
	slugRe     = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)
	hexRe      = regexp.MustCompile(`^([0-9a-fA-F]{2})+$`)
	durationRe = regexp.MustCompile(`^P(\d+Y)?(\d+M)?(\d+W)?(\d+D)?(T(\d+H)?(\d+M)?(\d+(\.\d+)?S)?)?$`)
)

// IP requires an IPv4 or IPv6 address. An IPv6 zone ("fe80::1%eth0") is
// refused: it names a local interface, not something a client may send.
func (s StringSchema) IP() StringSchema {
	return s.kw("anyOf", []any{map[string]any{"format": "ipv4"}, map[string]any{"format": "ipv6"}}).
		format("string.ip", func(v string) bool {
			a, err := netip.ParseAddr(v)
			return err == nil && a.Zone() == ""
		}, nil)
}

// IPv4 requires an IPv4 address in dotted decimal form: four parts, no
// leading zeros, and not an IPv4-mapped IPv6 address.
func (s StringSchema) IPv4() StringSchema {
	return s.kw("format", "ipv4").format("string.ipv4", func(v string) bool {
		a, err := netip.ParseAddr(v)
		return err == nil && a.Is4()
	}, nil)
}

// IPv6 requires an IPv6 address, without a zone.
func (s StringSchema) IPv6() StringSchema {
	return s.kw("format", "ipv6").format("string.ipv6", func(v string) bool {
		a, err := netip.ParseAddr(v)
		return err == nil && a.Is6() && a.Zone() == ""
	}, nil)
}

// CIDR requires an address with a prefix length ("10.0.0.0/8",
// "2001:db8::/32"); a bare address is refused.
func (s StringSchema) CIDR() StringSchema {
	return s.format("string.cidr", func(v string) bool {
		_, err := netip.ParsePrefix(v)
		return err == nil
	}, nil)
}

// MAC requires a 48-bit hardware address ("00:1a:2b:3c:4d:5e", with dashes
// or in dotted groups); the 64-bit and 160-bit forms are refused.
func (s StringSchema) MAC() StringSchema {
	return s.format("string.mac", func(v string) bool {
		m, err := net.ParseMAC(v)
		return err == nil && len(m) == 6
	}, nil)
}

// Hostname requires a DNS hostname: at most 253 bytes, labels of 1 to 63
// letters, digits or hyphens that neither start nor end with a hyphen. It
// does not look the name up.
func (s StringSchema) Hostname() StringSchema {
	return s.kw("format", "hostname").format("string.hostname", isHostname, nil)
}

func isHostname(v string) bool {
	if v == "" || len(v) > 253 {
		return false
	}

	for label := range strings.SplitSeq(v, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}

		for i := range len(label) {
			c := label[i]
			if c != '-' && (c < '0' || c > '9') && (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') {
				return false
			}
		}
	}

	return true
}

// E164 requires an international phone number: "+", a country code that does
// not start with 0, and 7 to 15 digits in all.
func (s StringSchema) E164() StringSchema {
	return s.kw("pattern", e164Re.String()).format("string.e164", e164Re.MatchString, nil)
}

// Slug requires lowercase letters and digits separated by single dashes
// ("my-first-post").
func (s StringSchema) Slug() StringSchema {
	return s.kw("pattern", slugRe.String()).format("string.slug", slugRe.MatchString, nil)
}

// Hex requires a non-empty hexadecimal string of even length. It reads the
// whole value before it decodes it: put a Max(n) on the schema (it runs
// first) to bound what an untrusted client can make it read.
func (s StringSchema) Hex() StringSchema {
	return s.kw("pattern", hexRe.String()).format("string.hex", func(v string) bool {
		if v == "" || len(v)%2 != 0 {
			return false
		}

		_, err := hex.DecodeString(v)

		return err == nil
	}, nil)
}

// Base64 requires standard base64 with padding. Line breaks are refused (the
// decoder would skip them). It decodes the whole value to check it: combine it
// with Max(n) to bound the size of what an untrusted client sends.
func (s StringSchema) Base64() StringSchema {
	return s.kw("contentEncoding", "base64").format("string.base64", func(v string) bool {
		return !strings.ContainsAny(v, "\r\n") && decodes(base64.StdEncoding.Strict(), v)
	}, nil)
}

// Base64URL requires URL-safe base64, with or without padding. Combine it with
// Max(n), like [StringSchema.Base64], to bound the size it decodes.
func (s StringSchema) Base64URL() StringSchema {
	return s.kw("contentEncoding", "base64url").format("string.base64url", isBase64URL, nil)
}

func isBase64URL(v string) bool {
	if strings.ContainsAny(v, "\r\n") {
		return false
	}

	return decodes(base64.RawURLEncoding.Strict(), v) || decodes(base64.URLEncoding.Strict(), v)
}

func decodes(enc *base64.Encoding, v string) bool {
	_, err := enc.DecodeString(v)
	return err == nil
}

// JWT requires the shape of a JSON Web Token: three base64url segments whose
// first one decodes to a JSON object with a non-empty "alg" string.
//
// It does NOT verify the signature, the claims or the algorithm: a string
// that passes is only well formed, and anyone can build one, including one
// whose alg is "none". Use auth/jwt to accept a token. Combine it with Max(n)
// to bound the size of what is decoded.
func (s StringSchema) JWT() StringSchema {
	return s.format("string.jwt", isJWTShape, nil)
}

func isJWTShape(v string) bool {
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return false
	}

	for _, p := range parts {
		if p == "" || strings.ContainsAny(p, "=\r\n") || !isBase64URL(p) {
			return false
		}
	}

	raw, err := base64.RawURLEncoding.Strict().DecodeString(parts[0])
	if err != nil {
		return false
	}

	var header struct {
		Alg any `json:"alg"`
	}

	if json.Unmarshal(raw, &header) != nil {
		return false
	}

	alg, ok := header.Alg.(string)

	return ok && alg != ""
}

// ISODate requires a calendar date, "2026-02-28" (YYYY-MM-DD).
func (s StringSchema) ISODate() StringSchema {
	return s.kw("format", "date").format("string.iso_date", func(v string) bool {
		_, err := time.Parse(time.DateOnly, v)
		return err == nil
	}, nil)
}

// ISOTime requires a time of day, "15:04:05", with optional fractional
// seconds, and no zone.
func (s StringSchema) ISOTime() StringSchema {
	return s.kw("format", "time").format("string.iso_time", func(v string) bool {
		// The layout's "15" also takes one digit; ISO 8601 wants two.
		if len(v) < 8 || v[2] != ':' {
			return false
		}

		_, err := time.Parse("15:04:05", v)

		return err == nil
	}, nil)
}

// ISODateTime requires an RFC 3339 date and time with a zone
// ("2026-02-28T10:30:00Z", "...+02:00"), with optional fractional seconds.
func (s StringSchema) ISODateTime() StringSchema {
	return s.kw("format", "date-time").format("string.iso_datetime", func(v string) bool {
		// The layout's "15" also takes one digit; ISO 8601 wants two.
		if len(v) < 20 || v[13] != ':' {
			return false
		}

		_, err := time.Parse(time.RFC3339Nano, v)

		return err == nil
	}, nil)
}

// ISODuration requires an ISO 8601 duration ("P1Y2M3DT4H5M6S", "PT0.5S",
// "P1W"): a "P", then years, months, weeks and days, then after a "T" hours,
// minutes and seconds, in that order, with at least one part.
func (s StringSchema) ISODuration() StringSchema {
	return s.kw("format", "duration").format("string.iso_duration", func(v string) bool {
		return durationRe.MatchString(v) && v != "P" && !strings.HasSuffix(v, "T")
	}, nil)
}

// NormalizeEmail trims the value and lower-cases it before the checks. The
// RFC lets a mailbox be case sensitive, but no mail provider is, and one
// person registered twice as "Ada@x.io" and "ada@x.io" is the problem this
// avoids. Dots and "+tags" are left alone. Put it before Email().
func (s StringSchema) NormalizeEmail() StringSchema {
	return s.with(func(v string) string { return strings.ToLower(strings.TrimSpace(v)) }, nil)
}

// Escape replaces <, >, &, ' and " with HTML entities before the checks.
// Think twice: html/template already escapes on output, and escaping on input
// stores entities in the database, so "Tom & Jerry" is saved as
// "Tom &amp; Jerry" and shown wrong anywhere that is not HTML. It is here for
// the data that is only ever printed as HTML. It is not idempotent: a value
// that goes through it twice (an edit form that posts back what was stored)
// is escaped twice ("&amp;amp;"). The checks after it, Min and Max among them,
// count the escaped text, in which a single & is five characters.
func (s StringSchema) Escape() StringSchema { return s.with(html.EscapeString, nil) }
