// Package ulid generates and validates ULIDs with the standard library
// only: 128-bit identifiers whose 26-character text form sorts by creation
// time.
//
//	g := ulid.NewGenerator()      // one per app, wired like any other service
//	id := g.New()                 // "01KAB2M4R1R4Q5WJ2B6PM9NZ3K"
//	if !ulid.Valid(r.PathValue("id")) { ... }
//
// A ULID is the key to reach for when a row's id is handed to clients: a
// sequential integer leaks how many rows a table holds, and a random
// version 4 UUID leaks nothing but destroys index locality and needs a
// separate column to order by. A ULID keeps the ordering in the key: the
// first 48 bits are a Unix timestamp in milliseconds, the remaining 80 come
// from crypto/rand. The timestamp is visible to anyone holding the id
// ([TimeOf]).
//
// An id is a plain string, so it needs no driver.Valuer or sql.Scanner and
// works as it is in a model field, a URL and a query. The matching column
// is CHAR(26), which schema.Table.ULID creates.
//
// There is deliberately no package-level generator: staying monotonic needs
// state, and package-level mutable state is state nobody owns and no test
// can isolate. Callers hold a [Generator], which also makes the clock
// injectable through [Generator.At].
//
// See https://github.com/AlexGalhardo/vukem/blob/main/docs/database.md#ulids.
package ulid

import (
	"crypto/rand"
	"strings"
	"sync"
	"time"
)

// Length is the number of characters in the text form of a ULID.
const Length = 26

// alphabet is Crockford base32: no I, L, O or U, so an id cannot be misread
// and cannot accidentally spell a word. Its characters ascend in ASCII
// order, which is what makes the text form sort like the number it holds.
const alphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

const (
	bitsPerChar = 5
	charMask    = 1<<bitsPerChar - 1

	// timeChars is how many characters hold the timestamp: ten characters
	// are 50 bits for a 48-bit field, so padBits of the first character are
	// always zero and its value can never exceed maxFirst.
	timeChars = 10
	padBits   = bitsPerChar*timeChars - timeBits
	maxFirst  = 1<<(bitsPerChar-padBits) - 1

	// timeBits is the width of the timestamp field and maxMS the largest
	// millisecond it holds (year 10889).
	timeBits = 48
	maxMS    = 1<<timeBits - 1

	// tailBytes is the entropy after the timestamp: 80 bits, encoded as two
	// 40-bit halves because 40 bits is exactly halfChars characters and
	// still fits a uint64.
	tailBytes = 10
	halfBytes = 5
	halfChars = 8
)

// Generator hands out ULIDs that are unique and strictly increasing, even
// for several ids made within the same millisecond. It is safe for
// concurrent use and its zero value is ready to use.
type Generator struct {
	mu     sync.Mutex
	lastMS uint64
	tail   [tailBytes]byte
	// seeded stays false until the first id, so that the zero millisecond
	// is not mistaken for "already generated at time 0".
	seeded bool
}

// NewGenerator returns a generator ready for use.
func NewGenerator() *Generator { return &Generator{} }

// New returns an id stamped with the current time.
func (g *Generator) New() string { return g.At(time.Now()) }

// At returns an id stamped with t, clamped to the range the 48-bit
// timestamp holds (the Unix epoch to year 10889) rather than silently
// encoded as some other time.
//
// When t lands in a millisecond already used, the random tail is
// incremented instead of redrawn, which keeps the ids strictly increasing.
// The same happens when t is before the last id: a clock that steps
// backwards must not produce an id that sorts before one already handed
// out.
func (g *Generator) At(t time.Time) string {
	ms := millis(t)

	g.mu.Lock()
	defer g.mu.Unlock()

	switch {
	case !g.seeded || ms > g.lastMS:
		g.lastMS, g.seeded = ms, true
		g.fill()
	case increment(&g.tail):
		ms = g.lastMS
	default:
		// The 80-bit counter wrapped inside one millisecond, which takes
		// 2^80 ids. Moving to the next millisecond keeps the ordering.
		g.lastMS++
		ms = g.lastMS

		g.fill()
	}

	return encode(ms, g.tail)
}

// fill draws a fresh random tail. crypto/rand.Read is documented never to
// fail as of Go 1.24: it terminates the program rather than return an
// error, so there is no error path to handle here.
func (g *Generator) fill() { _, _ = rand.Read(g.tail[:]) }

// millis is t as Unix milliseconds, clamped to what the timestamp field
// holds. A time before the epoch would be negative and one past year 10889
// would not fit 48 bits; either would encode as a different time.
func millis(t time.Time) uint64 {
	ms := t.UnixMilli()
	if ms <= 0 {
		return 0
	}

	return min(uint64(ms), maxMS)
}

// increment adds one to the big-endian counter in tail and reports whether
// it did so without wrapping.
func increment(tail *[tailBytes]byte) bool {
	for i := len(tail) - 1; i >= 0; i-- {
		tail[i]++
		if tail[i] != 0 {
			return true
		}
	}

	return false
}

// encode renders ms and tail as Length characters, five bits each, most
// significant first. The halves line up with the format: the timestamp is
// exactly the first timeChars characters and the entropy the last 16, so
// no group needs a carry across a character boundary.
func encode(ms uint64, tail [tailBytes]byte) string {
	var out [Length]byte

	group(out[:], ms, timeChars)

	for i := range tailBytes / halfBytes {
		var v uint64
		for _, b := range tail[i*halfBytes : (i+1)*halfBytes] {
			v = v<<8 | uint64(b)
		}

		group(out[timeChars+i*halfChars:], v, halfChars)
	}

	return string(out[:])
}

// group writes the low bitsPerChar*n bits of v into the first n bytes of
// out, most significant group first.
func group(out []byte, v uint64, n int) {
	for i := range n {
		out[i] = alphabet[v>>(bitsPerChar*(n-1-i))&charMask]
	}
}

// Valid reports whether s is an id in the canonical form [Generator.New]
// returns: Length Crockford base32 characters, upper case, whose value fits
// 128 bits. Use it before putting a client-supplied id in a query — a value
// that cannot be an id does not need a database round trip to be refused.
//
// Lower case is rejected on purpose, although the ULID specification lets a
// parser accept it: a CHAR(26) key must have exactly one text form, or two
// spellings of one id become two rows. The validation rule
// z.String().ULID() is the case-insensitive one, for ids this package did
// not make.
func Valid(s string) bool {
	_, ok := decode(s)

	return ok
}

// TimeOf returns the millisecond an id was stamped with, in UTC, and
// whether s was a valid id. The time is advisory: it is the clock of
// whichever process made the id, not a verified fact.
func TimeOf(s string) (time.Time, bool) {
	ms, ok := decode(s)
	if !ok {
		return time.Time{}, false
	}

	return time.UnixMilli(ms).UTC(), true
}

// decode parses s and returns its timestamp in milliseconds. Every
// character is checked, including the 16 that carry only entropy, so Valid
// and TimeOf agree on what an id is. The timestamp is at most timeBits
// wide, so it needs no bounds check to be an int64 for time.UnixMilli.
func decode(s string) (ms int64, ok bool) {
	if len(s) != Length {
		return 0, false
	}

	for i := range Length {
		// IndexByte over a 32-byte constant keeps the alphabet the single
		// source of truth for encoding and decoding, at a cost that does
		// not matter next to the database round trip it guards.
		v := strings.IndexByte(alphabet, s[i])
		if v < 0 {
			return 0, false
		}

		// The first character carries bitsPerChar-padBits real bits: a
		// larger value would need more than the 128 bits of a ULID, so
		// encode cannot have produced it.
		if i == 0 && v > maxFirst {
			return 0, false
		}

		if i < timeChars {
			ms = ms<<bitsPerChar | int64(v)
		}
	}

	return ms, true
}
