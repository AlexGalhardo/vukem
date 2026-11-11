package z

import (
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
)

// longLabel is a hostname label one byte past the limit of 63.
var longLabel = strings.Repeat("a", 64)

func TestStringFormats(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		schema  StringSchema
		key     string
		valid   []string
		invalid []string
	}{
		{
			"ip", String().IP(), "string.ip",
			[]string{"1.2.3.4", "::1", "2001:db8::1", "255.255.255.255", "::ffff:1.2.3.4"},
			[]string{"", "fe80::1%eth0", "1.2.3", "01.2.3.4", "256.1.1.1", "1.2.3.4/8", "hello", "1.2.3.4 "},
		},
		{
			"ipv4", String().IPv4(), "string.ipv4",
			[]string{"1.2.3.4", "0.0.0.0", "192.168.0.1", "255.255.255.255"},
			[]string{"1.2.3", "01.2.3.4", "::1", "::ffff:1.2.3.4", "1.2.3.4.5", "256.0.0.1", "", "a.b.c.d"},
		},
		{
			"ipv6", String().IPv6(), "string.ipv6",
			[]string{"::1", "::", "2001:db8::1", "fe80:0:0:0:0:0:0:1"},
			[]string{"fe80::1%eth0", "1.2.3.4", "2001:db8:::1", "12345::", ":::", "", "::g"},
		},
		{
			"cidr",
			String().CIDR(),
			"string.cidr",
			[]string{"10.0.0.0/8", "192.168.1.0/24", "2001:db8::/32", "0.0.0.0/0"},
			[]string{
				"10.0.0.1",
				"10.0.0.0/33",
				"2001:db8::/129",
				"10.0.0.0/",
				"/8",
				"",
				"10.0.0.0/-1",
				"fe80::1%eth0/64",
			},
		},
		{
			"mac",
			String().MAC(),
			"string.mac",
			[]string{"00:1a:2b:3c:4d:5e", "00-1A-2B-3C-4D-5E", "001a.2b3c.4d5e", "FF:FF:FF:FF:FF:FF"},
			[]string{
				"00:1a:2b:3c:4d",
				"00:1a:2b:3c:4d:5e:6f:70",
				"zz:1a:2b:3c:4d:5e",
				"",
				"00:1a:2b",
				"00:1a:2b:3c:4d:5e:",
			},
		},
		{
			"hostname",
			String().Hostname(),
			"string.hostname",
			[]string{
				"example.com",
				"a",
				"my-host",
				"sub.example.co.uk",
				"EXAMPLE.com",
				strings.Repeat("a", 63) + ".com",
			},
			[]string{
				"",
				longLabel + ".com",
				"-a.com",
				"a-.com",
				"a..com",
				".a",
				"a.",
				"a_b.com",
				"ex ample.com",
				"é.com",
				strings.Repeat("a.", 127) + "a",
			},
		},
		{
			"e164",
			String().E164(),
			"string.e164",
			[]string{"+5511987654321", "+14155552671", "+1234567", "+123456789012345"},
			[]string{
				"+0123456789",
				"+1234567890123456",
				"5511987654321",
				"+123456",
				"+55 11 98765",
				"",
				"+",
				"+12a456789",
			},
		},
		{
			"slug", String().Slug(), "string.slug",
			[]string{"a", "my-first-post", "post-2", "2026"},
			[]string{"", "My-Post", "-a", "a-", "a--b", "a_b", "a b", "á"},
		},
		{
			"hex", String().Hex(), "string.hex",
			[]string{"00", "deadBEEF", "0123456789abcdef", "ff"},
			[]string{"", "abc", "0g", "0x00", " 00", "00 ", "zz", "dead beef"},
		},
		{
			"base64", String().Base64(), "string.base64",
			[]string{"aGVsbG8=", "aGk=", "YQ==", "", "aGVsbG8gd29ybGQ="},
			[]string{"aGVsbG8", "a-_=", "aGVsbG8\n", "aGVs bG8=", "====", "aGk", "aG!="},
		},
		{
			"base64url", String().Base64URL(), "string.base64url",
			[]string{"aGVsbG8", "a-_A", "YQ", "YQ==", ""},
			[]string{"a+/=", "aGVsbG8\n", "a b", "aGVsbG8=Z", "a", "YQ="},
		},
		{
			"jwt", String().JWT(), "string.jwt",
			[]string{
				"eyJhbGciOiJIUzI1NiJ9.e30.c2ln",
				"eyJhbGciOiJSUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxIn0.c2ln",
				"eyJhbGciOiJFUzI1NiIsImtpZCI6ImEifQ.e30.c2ln",
				"eyJhbGciOiJFZERTQSJ9.e30.YQ",
			},
			[]string{
				"", "a.b", "a.b.c.d", "eyJhbGciOiJIUzI1NiJ9.e30.", "bm90anNvbg.e30.c2ln", // not JSON
				"e30.e30.c2ln",                 // header without alg
				"eyJhbGciOjF9.e30.c2ln",        // alg is a number
				"eyJhbGciOiIifQ.e30.c2ln",      // alg is empty
				"eyJhbGciOiJIUzI1NiJ9=.e30.c2", // padding
				"W10.e30.c2ln",                 // header is an array
			},
		},
		{
			"iso_date",
			String().ISODate(),
			"string.iso_date",
			[]string{"2026-02-28", "2024-02-29", "1999-12-31", "0001-01-01"},
			[]string{
				"2026-02-30",
				"2026-13-01",
				"2026-1-1",
				"26-02-28",
				"2026-02-28T10:00:00Z",
				"",
				"2026/02/28",
				"2025-02-29",
			},
		},
		{
			"iso_time", String().ISOTime(), "string.iso_time",
			[]string{"10:30:00", "00:00:00", "23:59:59", "10:30:00.5", "10:30:00.123456789"},
			[]string{"24:00:00", "10:60:00", "10:30", "10:30:00Z", "1:30:00", "", "10:30:00.", "10:30:60"},
		},
		{
			"iso_datetime",
			String().ISODateTime(),
			"string.iso_datetime",
			[]string{
				"2026-02-28T10:30:00Z",
				"2026-02-28T10:30:00+02:00",
				"2026-02-28T10:30:00.123Z",
				"2026-02-28T10:30:00-03:00",
			},
			[]string{
				"2026-02-28",
				"2026-02-28 10:30:00",
				"2026-02-28T10:30:00",
				"2026-02-30T10:30:00Z",
				"",
				"2026-02-28T25:00:00Z", "2026-02-28T1:30:00Z",
				"10:30:00Z",
			},
		},
		{
			"iso_duration", String().ISODuration(), "string.iso_duration",
			[]string{"P1Y2M3DT4H5M6S", "PT0.5S", "P1W", "P1D", "PT1H", "P1Y", "PT30M", "P0D"},
			[]string{"P", "PT", "P1S", "1D", "P1DT", "PT1", "P1.5D", "p1d", "P1H", "PT1D", "P-1D", ""},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if len(tt.valid) < 4 || len(tt.invalid) < 6 {
				t.Fatalf(
					"the table needs at least 4 valid and 6 invalid values, has %d and %d",
					len(tt.valid),
					len(tt.invalid),
				)
			}

			for _, v := range tt.valid {
				if _, err := tt.schema.Parse(v); err != nil {
					t.Errorf("Parse(%q) = %v, want it accepted", v, err)
				}
			}

			for _, v := range tt.invalid {
				_, err := tt.schema.Parse(v)

				is, ok := AsIssues(err)
				if !ok || len(is) != 1 || is[0].Code != CodeInvalidString || is[0].Key != tt.key {
					t.Errorf("Parse(%q) = %v, want one %s issue with key %s", v, err, CodeInvalidString, tt.key)
				}
			}
		})
	}
}

func TestStringFormatsJSONSchema(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		schema StringSchema
		want   map[string]any
	}{
		{"ipv4", String().IPv4(), map[string]any{"type": "string", "format": "ipv4"}},
		{"ipv6", String().IPv6(), map[string]any{"type": "string", "format": "ipv6"}},
		{"ip", String().IP(), map[string]any{"type": "string", "anyOf": []any{
			map[string]any{"format": "ipv4"}, map[string]any{"format": "ipv6"},
		}}},
		{"hostname", String().Hostname(), map[string]any{"type": "string", "format": "hostname"}},
		{"e164", String().E164(), map[string]any{"type": "string", "pattern": `^\+[1-9]\d{6,14}$`}},
		{"slug", String().Slug(), map[string]any{"type": "string", "pattern": `^[a-z0-9]+(-[a-z0-9]+)*$`}},
		{"hex", String().Hex(), map[string]any{"type": "string", "pattern": `^([0-9a-fA-F]{2})+$`}},
		{"base64", String().Base64(), map[string]any{"type": "string", "contentEncoding": "base64"}},
		{"base64url", String().Base64URL(), map[string]any{"type": "string", "contentEncoding": "base64url"}},
		{"iso_date", String().ISODate(), map[string]any{"type": "string", "format": "date"}},
		{"iso_time", String().ISOTime(), map[string]any{"type": "string", "format": "time"}},
		{"iso_datetime", String().ISODateTime(), map[string]any{"type": "string", "format": "date-time"}},
		{"iso_duration", String().ISODuration(), map[string]any{"type": "string", "format": "duration"}},
		// No keyword can say these.
		{"cidr", String().CIDR(), map[string]any{"type": "string"}},
		{"mac", String().MAC(), map[string]any{"type": "string"}},
		{"jwt", String().JWT(), map[string]any{"type": "string"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if diff := cmp.Diff(tt.want, JSONSchema(tt.schema)); diff != "" {
				t.Errorf("schema (-want +got):\n%s", diff)
			}
		})
	}
}

func TestStringNormalization(t *testing.T) {
	t.Parallel()

	got, err := String().NormalizeEmail().Email().Parse(" Ada@Example.COM ")
	if err != nil || got != "ada@example.com" {
		t.Errorf("NormalizeEmail = %q, %v", got, err)
	}

	if got, err := String().Escape().Parse(`<b>"Tom" & 'Jerry'</b>`); err != nil ||
		got != "&lt;b&gt;&#34;Tom&#34; &amp; &#39;Jerry&#39;&lt;/b&gt;" {
		t.Errorf("Escape = %q, %v", got, err)
	}
}

func TestDateRulesRelativeToValues(t *testing.T) {
	t.Parallel()

	ref := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)

	if _, err := Time().AfterOrEqual(ref).Parse(ref); err != nil {
		t.Errorf("AfterOrEqual rejects the instant itself: %v", err)
	}

	if _, err := Time().AfterOrEqual(ref).Parse(ref.Add(-time.Second)); err == nil {
		t.Error("AfterOrEqual accepts an earlier instant")
	}

	if _, err := Time().After(ref).Parse(ref); err == nil {
		t.Error("After accepts the instant itself")
	}

	if _, err := Time().BeforeOrEqual(ref).Parse(ref); err != nil {
		t.Errorf("BeforeOrEqual rejects the instant itself: %v", err)
	}

	_, err := Time().BeforeOrEqual(ref).Parse(ref.Add(time.Second))

	is, _ := AsIssues(err)
	if len(is) != 1 || is[0].Key != "too_big.date_inclusive" || is[0].Code != CodeTooBig {
		t.Errorf("BeforeOrEqual issues = %+v", is)
	}
}

// FuzzStringFormats feeds arbitrary text to every format: none may panic,
// and an ISODateTime must also be a valid RFC 3339 time, so the extra check
// of the digits never accepts what the standard library refuses.
func FuzzStringFormats(f *testing.F) {
	for _, s := range []string{
		"", "1.2.3.4", "fe80::1%eth0", "10.0.0.0/8", "00:1a:2b:3c:4d:5e", "example.com", "+5511987654321",
		"my-post", "deadBEEF", "aGVsbG8=", "eyJhbGciOiJIUzI1NiJ9.e30.c2ln", "2026-02-28", "10:30:00",
		"2026-02-28T10:30:00Z", "P1Y2M3DT4H5M6S", "PT", "\x00", "a.b.c",
	} {
		f.Add(s)
	}

	schemas := []StringSchema{
		String().IP(), String().IPv4(), String().IPv6(), String().CIDR(), String().MAC(), String().Hostname(),
		String().E164(), String().Slug(), String().Hex(), String().Base64(), String().Base64URL(), String().JWT(),
		String().ISODate(), String().ISOTime(), String().ISODuration(), String().NormalizeEmail().Email(),
		String().Escape(),
	}
	dateTime := String().ISODateTime()

	f.Fuzz(func(t *testing.T, data string) {
		for i := range schemas {
			_, _ = schemas[i].Parse(data)
		}

		if _, err := dateTime.Parse(data); err == nil {
			if _, perr := time.Parse(time.RFC3339Nano, data); perr != nil {
				t.Fatalf("ISODateTime accepted %q, which time.Parse refuses: %v", data, perr)
			}
		}
	})
}
