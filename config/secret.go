package config

import "log/slog"

const redacted = "[redacted]"

// Secret holds a sensitive value. Every formatting path (fmt, encoding/json,
// log/slog) prints "[redacted]"; use Reveal to read the value.
type Secret struct {
	v string
}

// NewSecret wraps v.
func NewSecret(v string) Secret { return Secret{v: v} }

// Reveal returns the underlying value. Call it only where the value is used.
func (s Secret) Reveal() string { return s.v }

// IsZero reports whether the secret is empty.
func (s Secret) IsZero() bool { return s.v == "" }

// String implements fmt.Stringer.
func (Secret) String() string { return redacted }

// GoString implements fmt.GoStringer so %#v does not leak the value.
func (Secret) GoString() string { return redacted }

// LogValue implements slog.LogValuer.
func (Secret) LogValue() slog.Value { return slog.StringValue(redacted) }

// MarshalJSON implements json.Marshaler.
func (Secret) MarshalJSON() ([]byte, error) { return []byte(`"` + redacted + `"`), nil }

// MarshalText implements encoding.TextMarshaler.
func (Secret) MarshalText() ([]byte, error) { return []byte(redacted), nil }
