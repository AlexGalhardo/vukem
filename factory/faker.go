package factory

import (
	"encoding/binary"
	"fmt"
	"math/rand/v2"
	"strings"
	"time"
)

// Faker generates fake data. It is deterministic for a given seed; it is
// not cryptographically secure and must never produce secrets.
type Faker struct {
	r   *rand.Rand
	seq int
}

// NewFaker returns a Faker seeded with seed.
func NewFaker(seed uint64) *Faker {
	src := rand.NewPCG(seed, seed^0x9e3779b97f4a7c15)

	return &Faker{r: rand.New(src)} //nolint:gosec // G404: fake data, never secrets
}

var (
	firstNames = []string{
		"Ana", "Bruno", "Carla", "Diego", "Elena", "Felipe", "Gabriela", "Hugo", "Isabel", "João",
		"Karina", "Lucas", "Marina", "Nicolas", "Olivia", "Pedro", "Rafaela", "Samuel", "Tatiana", "Vitor",
	}
	lastNames = []string{
		"Almeida", "Barbosa", "Costa", "Dias", "Ferreira", "Gomes", "Lima", "Martins", "Nunes", "Oliveira",
		"Pereira", "Ribeiro", "Santos", "Silva", "Souza", "Teixeira",
	}
	words = strings.Fields(`lorem ipsum dolor sit amet consectetur adipiscing elit sed do eiusmod tempor
		incididunt ut labore et dolore magna aliqua enim ad minim veniam quis nostrud exercitation ullamco
		laboris nisi aliquip ex ea commodo consequat duis aute irure in reprehenderit voluptate velit esse`)
	domains = []string{"example.com", "example.org", "example.net"}
)

// Seq is the 1-based index of the model being built (unique per Make).
func (f *Faker) Seq() int { return f.seq }

// Int returns a number in [lo, hi].
func (f *Faker) Int(lo, hi int) int {
	if hi <= lo {
		return lo
	}

	return lo + f.r.IntN(hi-lo+1)
}

// Float returns a number in [lo, hi).
func (f *Faker) Float(lo, hi float64) float64 { return lo + f.r.Float64()*(hi-lo) }

// Bool returns true or false.
func (f *Faker) Bool() bool { return f.r.IntN(2) == 1 }

// Pick returns one of items.
func Pick[T any](f *Faker, items ...T) T { return items[f.r.IntN(len(items))] }

// FirstName returns a first name.
func (f *Faker) FirstName() string { return Pick(f, firstNames...) }

// LastName returns a last name.
func (f *Faker) LastName() string { return Pick(f, lastNames...) }

// Name returns a full name.
func (f *Faker) Name() string { return f.FirstName() + " " + f.LastName() }

// Username returns a lowercase handle, unique within one Make.
func (f *Faker) Username() string {
	return fmt.Sprintf("%s%d", ascii(strings.ToLower(f.FirstName())), f.seq*1000+f.r.IntN(1000))
}

// Email returns an address on a reserved example domain, unique within
// one Make and unlikely to collide across Makes.
func (f *Faker) Email() string {
	return fmt.Sprintf("%s.%s.%d%04d@%s", ascii(strings.ToLower(f.FirstName())), ascii(strings.ToLower(f.LastName())),
		f.seq, f.r.IntN(10000), Pick(f, domains...))
}

// Word returns a lorem ipsum word.
func (f *Faker) Word() string { return Pick(f, words...) }

// Words returns n words separated by spaces.
func (f *Faker) Words(n int) string {
	out := make([]string, n)
	for i := range out {
		out[i] = f.Word()
	}

	return strings.Join(out, " ")
}

// Sentence returns a capitalized sentence of 6–12 words.
func (f *Faker) Sentence() string {
	s := f.Words(f.Int(6, 12))
	return strings.ToUpper(s[:1]) + s[1:] + "."
}

// Paragraph returns 3–6 sentences.
func (f *Faker) Paragraph() string {
	out := make([]string, f.Int(3, 6))
	for i := range out {
		out[i] = f.Sentence()
	}

	return strings.Join(out, " ")
}

// Slug returns a lowercase, dash-separated slug of 3 words.
func (f *Faker) Slug() string {
	return strings.ReplaceAll(f.Words(3), " ", "-") + fmt.Sprintf("-%d", f.seq)
}

// URL returns an https URL on an example domain.
func (f *Faker) URL() string { return "https://" + Pick(f, domains...) + "/" + f.Slug() }

// UUID returns a random version 4 UUID (not for secrets).
func (f *Faker) UUID() string {
	var b [16]byte
	binary.LittleEndian.PutUint64(b[:8], f.r.Uint64())
	binary.LittleEndian.PutUint64(b[8:], f.r.Uint64())

	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80

	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// Time returns a time between from and to.
func (f *Faker) Time(from, to time.Time) time.Time {
	d := to.Sub(from)
	if d <= 0 {
		return from
	}

	return from.Add(time.Duration(f.r.Int64N(int64(d)))).UTC().Truncate(time.Second)
}

// ascii keeps letters a–z so generated emails and handles stay valid.
func ascii(s string) string {
	repl := strings.NewReplacer(
		"ã",
		"a",
		"á",
		"a",
		"â",
		"a",
		"é",
		"e",
		"ê",
		"e",
		"í",
		"i",
		"ó",
		"o",
		"ô",
		"o",
		"õ",
		"o",
		"ú",
		"u",
		"ç",
		"c",
	)
	return repl.Replace(s)
}
