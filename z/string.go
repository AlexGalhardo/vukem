package z

import (
	"net/mail"
	"net/url"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

type strCheck func(o *opts[string], c *pctx, path, v string)

// StringSchema parses strings. It rejects invalid UTF-8 and control
// characters other than tab, newline and carriage return (A05).
type StringSchema struct {
	o      opts[string]
	pre    []func(string) string
	checks []strCheck
	// omitEmpty skips the checks when the value is empty after the
	// pre-steps, so an optional enum or a bounded optional string can be
	// described in one schema.
	omitEmpty bool
}

// String returns a schema for string values. The empty string is a valid
// string; use Min(1) (or the "required" tag) to reject it.
func String() StringSchema { return StringSchema{} }

func (s StringSchema) with(pre func(string) string, check strCheck) StringSchema {
	s.o = s.o.clone()
	s.pre = slices.Clip(s.pre)
	s.checks = slices.Clip(s.checks)

	if pre != nil {
		s.pre = append(s.pre, pre)
	}

	if check != nil {
		s.checks = append(s.checks, check)
	}

	return s
}

// GoType implements AnySchema.
func (StringSchema) GoType() reflect.Type { return reflect.TypeFor[string]() }

func (s StringSchema) optional() bool { return s.o.optional || s.o.hasDef }

func (s StringSchema) parseAny(c *pctx, path string, in any, present bool) (any, bool, bool) {
	v, set, ok := parse(&s.o, c, path, in, present, false, s.core)
	return v, set, ok
}

func (s StringSchema) core(c *pctx, path string, in any) (string, bool) {
	v, isStr := in.(string)
	if !isStr {
		s.o.issue(
			c,
			path,
			CodeInvalidType,
			CodeInvalidType,
			map[string]any{"expected": "a string", "received": typeName(in)},
		)
		return "", false
	}

	if !safeText(v) {
		s.o.issue(c, path, CodeInvalidString, "string.utf8", nil)
		return "", false
	}

	for _, p := range s.pre {
		v = p(v)
	}

	// Fail closed: a Preprocess func must not reintroduce what the first
	// check rejected.
	if len(s.pre) > 0 && !safeText(v) {
		s.o.issue(c, path, CodeInvalidString, "string.utf8", nil)
		return "", false
	}

	// After the pre-steps, so a form field holding only spaces is empty
	// once Trim ran, and before the checks, which is the whole point. A
	// default replaces the empty value, which is the line of Go this
	// removes: if o.Format == "" { o.Format = "png" }.
	if s.omitEmpty && v == "" {
		if s.o.hasDef {
			return s.o.def, true
		}

		return v, true
	}

	before := len(c.issues)

	for _, chk := range s.checks {
		chk(&s.o, c, path, v)
	}

	return v, len(c.issues) == before
}

// safeText reports whether v is valid UTF-8 without control characters
// other than tab, newline and carriage return.
func safeText(v string) bool {
	return utf8.ValidString(v) && !strings.ContainsFunc(v, func(r rune) bool {
		return unicode.IsControl(r) && r != '\n' && r != '\r' && r != '\t'
	})
}

// Parse implements Schema.
func (s StringSchema) Parse(in any) (string, error) { return parseRoot[string](s, in) }

// Check implements Schema.
func (s StringSchema) Check(v string) error { return checkValue(s, v) }

// Trim removes leading and trailing white space before checks.
func (s StringSchema) Trim() StringSchema { return s.with(strings.TrimSpace, nil) }

// ToLower lower-cases the value before checks.
func (s StringSchema) ToLower() StringSchema { return s.with(strings.ToLower, nil) }

// ToUpper upper-cases the value before checks.
func (s StringSchema) ToUpper() StringSchema { return s.with(strings.ToUpper, nil) }

// Preprocess normalizes the value with fn before checks, e.g. stripping a
// phone number down to digits so Regex sees the normalized form.
// Pre-steps (Trim, ToLower, ToUpper, Preprocess) run in declaration order
// after the UTF-8 check and before every check (Min, Regex, Email...);
// Transform runs after the checks, and Refine last. The result is checked
// again for invalid UTF-8 and control characters (fail closed). The
// normalized value is what JSON Schema keywords (pattern, minLength...)
// describe. Preprocess panics if fn is nil.
func (s StringSchema) Preprocess(fn func(string) string) StringSchema {
	if fn == nil {
		panic("z: StringSchema.Preprocess: nil func")
	}

	return s.with(fn, nil)
}

// Min requires at least n characters (runes).
func (s StringSchema) Min(n int) StringSchema {
	return s.kw("minLength", n).with(nil, func(o *opts[string], c *pctx, path, v string) {
		if utf8.RuneCountInString(v) < n {
			o.issue(c, path, CodeTooSmall, "too_small.string", map[string]any{"min": n})
		}
	})
}

// Max allows at most n characters (runes).
func (s StringSchema) Max(n int) StringSchema {
	return s.kw("maxLength", n).with(nil, func(o *opts[string], c *pctx, path, v string) {
		if utf8.RuneCountInString(v) > n {
			o.issue(c, path, CodeTooBig, "too_big.string", map[string]any{"max": n})
		}
	})
}

// Len requires exactly n characters (runes).
func (s StringSchema) Len(n int) StringSchema {
	return s.kw("minLength", n).kw("maxLength", n).with(nil, func(o *opts[string], c *pctx, path, v string) {
		if l := utf8.RuneCountInString(v); l != n {
			code := CodeTooSmall
			if l > n {
				code = CodeTooBig
			}

			o.issue(c, path, code, "exact.string", map[string]any{"len": n})
		}
	})
}

// required is Min(1) reported as CodeTooSmall with the "required" message,
// the meaning of the "required" tag on a string.
func (s StringSchema) required() StringSchema {
	return s.kw("minLength", 1).with(nil, func(o *opts[string], c *pctx, path, v string) {
		if v == "" {
			o.issue(c, path, CodeTooSmall, CodeRequired, map[string]any{"min": 1})
		}
	})
}

// NonEmpty is Min(1).
func (s StringSchema) NonEmpty() StringSchema { return s.Min(1) }

func (s StringSchema) format(key string, valid func(string) bool, params map[string]any) StringSchema {
	return s.with(nil, func(o *opts[string], c *pctx, path, v string) {
		if !valid(v) {
			o.issue(c, path, CodeInvalidString, key, params)
		}
	})
}

// Email requires an email address (addr-spec, no display name).
func (s StringSchema) Email() StringSchema {
	return s.kw("format", "email").format("string.email", func(v string) bool {
		a, err := mail.ParseAddress(v)
		return err == nil && a.Address == v && a.Name == "" && strings.Contains(v[strings.LastIndexByte(v, '@'):], ".")
	}, nil)
}

// URL requires an absolute http or https URL.
func (s StringSchema) URL() StringSchema {
	return s.kw("format", "uri").format("string.url", func(v string) bool {
		u, err := url.Parse(v)
		return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
	}, nil)
}

var (
	uuidRe     = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
	ulidRe     = regexp.MustCompile(`^[0-7][0-9A-HJKMNP-TV-Za-hjkmnp-tv-z]{25}$`)
	numericRe  = regexp.MustCompile(`^-?\d+(\.\d+)?$`)
	alphaRe    = regexp.MustCompile(`^\p{L}+$`)
	alphanumRe = regexp.MustCompile(`^[\p{L}\p{N}]+$`)
)

// UUID requires a UUID in canonical form.
func (s StringSchema) UUID() StringSchema {
	return s.kw("format", "uuid").format("string.uuid", uuidRe.MatchString, nil)
}

// ULID requires a ULID.
func (s StringSchema) ULID() StringSchema {
	return s.kw("pattern", ulidRe.String()).format("string.ulid", ulidRe.MatchString, nil)
}

// Alpha requires letters only.
func (s StringSchema) Alpha() StringSchema {
	return s.kw("pattern", alphaRe.String()).format("string.alpha", alphaRe.MatchString, nil)
}

// Alphanum requires letters and digits only.
func (s StringSchema) Alphanum() StringSchema {
	return s.kw("pattern", alphanumRe.String()).format("string.alphanum", alphanumRe.MatchString, nil)
}

// Numeric requires a decimal number written as a string.
func (s StringSchema) Numeric() StringSchema {
	return s.kw("pattern", numericRe.String()).format("string.numeric", numericRe.MatchString, nil)
}

// The Has* rules carry no JSON Schema keyword: "contains a character of a
// class" chained across classes needs lookaheads, and one pattern would
// replace the others.

// HasLower requires at least one lowercase letter (unicode.IsLower).
func (s StringSchema) HasLower() StringSchema {
	return s.hasRune("string.has_lower", unicode.IsLower)
}

// HasUpper requires at least one uppercase letter (unicode.IsUpper).
func (s StringSchema) HasUpper() StringSchema {
	return s.hasRune("string.has_upper", unicode.IsUpper)
}

// HasDigit requires at least one decimal digit (unicode.IsDigit, so digits
// of other scripts count).
func (s StringSchema) HasDigit() StringSchema {
	return s.hasRune("string.has_digit", unicode.IsDigit)
}

// HasSymbol requires at least one punctuation or symbol character
// (unicode.IsPunct or unicode.IsSymbol); spaces do not count.
func (s StringSchema) HasSymbol() StringSchema {
	return s.hasRune("string.has_symbol", func(r rune) bool { return unicode.IsPunct(r) || unicode.IsSymbol(r) })
}

func (s StringSchema) hasRune(key string, class func(rune) bool) StringSchema {
	return s.format(key, func(v string) bool { return strings.IndexFunc(v, class) >= 0 }, nil)
}

// Regex requires a match of re.
func (s StringSchema) Regex(re *regexp.Regexp) StringSchema {
	return s.kw("pattern", re.String()).format("string.regex", re.MatchString, nil)
}

// StartsWith requires prefix.
func (s StringSchema) StartsWith(prefix string) StringSchema {
	return s.kw("pattern", "^"+regexp.QuoteMeta(prefix)).
		format("string.starts_with", func(v string) bool { return strings.HasPrefix(v, prefix) },
			map[string]any{"prefix": prefix})
}

// EndsWith requires suffix.
func (s StringSchema) EndsWith(suffix string) StringSchema {
	return s.kw("pattern", regexp.QuoteMeta(suffix)+"$").
		format("string.ends_with", func(v string) bool { return strings.HasSuffix(v, suffix) },
			map[string]any{"suffix": suffix})
}

// Includes requires substring.
func (s StringSchema) Includes(substring string) StringSchema {
	return s.kw("pattern", regexp.QuoteMeta(substring)).
		format("string.includes", func(v string) bool { return strings.Contains(v, substring) },
			map[string]any{"substring": substring})
}

// OneOf requires one of values (see also Enum).
func (s StringSchema) OneOf(values ...string) StringSchema {
	return s.kw("enum", values).with(nil, func(o *opts[string], c *pctx, path, v string) {
		if !slices.Contains(values, v) {
			o.issue(c, path, CodeInvalidEnum, CodeInvalidEnum, map[string]any{"options": values})
		}
	})
}

// OmitEmpty accepts the empty string, skipping every check after the
// pre-steps (Trim, ToLower, Preprocess). It is how an optional enum is
// written — OneOf alone rejects "" — and it is what the "omitempty"
// validate tag maps to.
//
// It is not Optional: Optional governs an **absent** key, while this
// governs a **present but empty** value, and a browser form sends the
// second, not the first. A field that may be either takes both.
//
// Transform and Refine still run, so a normalisation step sees the empty
// value; return nil from Refine for it when that is intended.
func (s StringSchema) OmitEmpty() StringSchema {
	s.o = s.o.clone()
	s.omitEmpty = true

	return s
}

// Optional allows the key to be absent (the zero value is used).
func (s StringSchema) Optional() StringSchema { s.o = s.o.clone(); s.o.optional = true; return s }

// Nullable allows null.
func (s StringSchema) Nullable() StringSchema { s.o = s.o.clone(); s.o.nullable = true; return s }

// Default uses v when the key is absent.
func (s StringSchema) Default(v string) StringSchema {
	s.o = s.o.clone()
	s.o.hasDef, s.o.def = true, v

	return s
}

// Refine adds a custom check; return FieldError for nested paths or
// FieldErrorKey for a translatable message.
func (s StringSchema) Refine(fn func(string) error) StringSchema {
	s.o = s.o.clone()
	s.o.refines = append(s.o.refines, fn)

	return s
}

// Transform maps the value after the checks; Refine sees the result. Use
// Preprocess to normalize before the checks.
func (s StringSchema) Transform(fn func(string) string) StringSchema {
	s.o = s.o.clone()
	s.o.transforms = append(s.o.transforms, fn)

	return s
}

// Message overrides the message for an issue code (or a detailed key such
// as "too_small.string"). Placeholders: {field}, {min}, {max}, ...
func (s StringSchema) Message(code, message string) StringSchema {
	s.o = s.o.clone()
	if s.o.messages == nil {
		s.o.messages = map[string]string{}
	}

	s.o.messages[code] = message

	return s
}

// Describe sets a description used by schema exports.
func (s StringSchema) Describe(text string) StringSchema {
	s.o = s.o.clone()
	s.o.desc = text
	return s
}

func (s StringSchema) kw(k string, v any) StringSchema {
	s.o = s.o.clone()
	s.o.setKW(k, v)

	return s
}
