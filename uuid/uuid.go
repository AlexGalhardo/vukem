// Package uuid generates and parses RFC 9562 UUIDs with the standard
// library only: random version 4 and time-ordered version 7, both from
// crypto/rand.
//
//	id := uuid.NewV7() // public order ids: sortable, unguessable
//	u, err := uuid.Parse(r.PathValue("id"))
//
// [UUID] is a [16]byte that marshals as its canonical text form in JSON
// (encoding.TextMarshaler) and in SQL (driver.Valuer, sql.Scanner), so it
// works as a struct field for orm models, request bodies and responses.
//
// See https://github.com/AlexGalhardo/vukem/blob/main/docs/database.md#uuids.
package uuid

import (
	"crypto/rand"
	"database/sql/driver"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"time"
)

// UUID is a 128-bit universally unique identifier. The zero value is the
// nil UUID (all zeros).
type UUID [16]byte

// ErrInvalid reports text that is not a UUID in canonical form.
var ErrInvalid = errors.New("uuid: invalid UUID")

// NewV4 returns a random (version 4) UUID: 122 bits from crypto/rand.
func NewV4() UUID {
	var u UUID

	_, _ = rand.Read(u[:]) // crypto/rand.Read never fails (Go 1.24+)

	return u.with(4)
}

// NewV7 returns a time-ordered (version 7) UUID: a 48-bit Unix timestamp
// in milliseconds followed by 74 bits from crypto/rand. UUIDs made in
// different milliseconds sort by creation time (as bytes and as strings),
// which keeps B-tree indexes compact; inside one millisecond the order is
// random. The timestamp is visible to anyone holding the UUID.
func NewV7() UUID {
	var u UUID

	_, _ = rand.Read(u[6:])

	var ts [8]byte
	binary.BigEndian.PutUint64(ts[:], uint64(time.Now().UnixMilli()))
	copy(u[0:6], ts[2:8]) // the low 48 bits, big-endian

	return u.with(7)
}

// with sets the version nibble and the RFC 9562 variant bits (10xx).
func (u UUID) with(version byte) UUID {
	u[6] = u[6]&0x0f | version<<4
	u[8] = u[8]&0x3f | 0x80

	return u
}

// Parse parses the canonical 36-character form
// (xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx), in either case. Other forms
// (braces, urn:uuid:, no hyphens) are rejected. It does not check the
// version: use Version for that.
func Parse(s string) (UUID, error) {
	var u UUID

	if len(s) != 36 || s[8] != '-' || s[13] != '-' || s[18] != '-' || s[23] != '-' {
		return u, ErrInvalid
	}

	src := []byte(s[0:8] + s[9:13] + s[14:18] + s[19:23] + s[24:36])
	if _, err := hex.Decode(u[:], src); err != nil {
		return UUID{}, ErrInvalid
	}

	return u, nil
}

// MustParse is Parse for constants; it panics on invalid input.
func MustParse(s string) UUID {
	u, err := Parse(s)
	if err != nil {
		panic(fmt.Sprintf("uuid: MustParse(%q): invalid UUID", s))
	}

	return u
}

// String returns the canonical lowercase form.
func (u UUID) String() string {
	var b [36]byte

	hex.Encode(b[0:8], u[0:4])
	b[8] = '-'
	hex.Encode(b[9:13], u[4:6])
	b[13] = '-'
	hex.Encode(b[14:18], u[6:8])
	b[18] = '-'
	hex.Encode(b[19:23], u[8:10])
	b[23] = '-'
	hex.Encode(b[24:36], u[10:16])

	return string(b[:])
}

// Version returns the version number (4 for NewV4, 7 for NewV7, 0 for the
// nil UUID).
func (u UUID) Version() int { return int(u[6] >> 4) }

// IsZero reports whether u is the nil UUID.
func (u UUID) IsZero() bool { return u == UUID{} }

// Time returns the creation time of a version 7 UUID (millisecond
// precision, UTC); ok is false for other versions.
func (u UUID) Time() (t time.Time, ok bool) {
	if u.Version() != 7 {
		return time.Time{}, false
	}

	ms := int64(binary.BigEndian.Uint16(u[0:2]))<<32 | int64(binary.BigEndian.Uint32(u[2:6]))

	return time.UnixMilli(ms).UTC(), true
}

// MarshalText implements encoding.TextMarshaler (JSON strings, query
// strings).
func (u UUID) MarshalText() ([]byte, error) { return []byte(u.String()), nil }

// UnmarshalText implements encoding.TextUnmarshaler with Parse's rules; an
// empty string is an error, not the nil UUID.
func (u *UUID) UnmarshalText(b []byte) error {
	p, err := Parse(string(b))
	if err != nil {
		return err
	}

	*u = p

	return nil
}

// Value implements driver.Valuer: the canonical text, which PostgreSQL's
// uuid type and SQLite/MySQL text columns accept.
func (u UUID) Value() (driver.Value, error) { return u.String(), nil }

// Scan implements sql.Scanner for text (string or []byte) and 16-byte
// binary columns. NULL is an error: scan nullable columns into a *UUID.
func (u *UUID) Scan(src any) error {
	switch v := src.(type) {
	case string:
		return u.UnmarshalText([]byte(v))
	case []byte:
		if len(v) == len(u) {
			copy(u[:], v)
			return nil
		}

		return u.UnmarshalText(v)
	case [16]byte:
		*u = v
		return nil
	case nil:
		return errors.New("uuid: cannot scan NULL into UUID (use *uuid.UUID)")
	default:
		return fmt.Errorf("uuid: cannot scan %T into UUID", src)
	}
}
