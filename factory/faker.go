package factory

import (
	"encoding/binary"
	"fmt"
	"math/rand/v2"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// Faker generates fake data. It is deterministic for a given seed; it is
// not cryptographically secure and must never produce secrets.
type Faker struct {
	r   *rand.Rand
	loc *localeData // nil: the original data set
	seq int
}

// NewFaker returns a Faker seeded with seed, with the zero Locale.
func NewFaker(seed uint64) *Faker { return NewFakerWithLocale(seed, Locale{}) }

// NewFakerWithLocale returns a Faker seeded with seed that generates
// names, words, companies, products and addresses in locale l.
func NewFakerWithLocale(seed uint64, l Locale) *Faker {
	src := rand.NewPCG(seed, seed^0x9e3779b97f4a7c15)

	return &Faker{r: rand.New(src), loc: l.d} //nolint:gosec // G404: fake data, never secrets
}

// Locale returns the Faker's locale.
func (f *Faker) Locale() Locale { return Locale{f.loc} }

// data is the locale's data set; English for the zero Locale, which has no
// commerce or address data of its own.
func (f *Faker) data() *localeData {
	if f.loc == nil {
		return &enData
	}

	return f.loc
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
func (f *Faker) FirstName() string {
	if f.loc != nil {
		return Pick(f, f.loc.firstNames...)
	}

	return Pick(f, firstNames...)
}

// LastName returns a last name.
func (f *Faker) LastName() string {
	if f.loc != nil {
		return Pick(f, f.loc.lastNames...)
	}

	return Pick(f, lastNames...)
}

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

// EmailFor returns an address on a reserved example domain whose local
// part is derived from name ("Ana Luíza" gives ana.luiza.…), unique within
// one Make like Email.
func (f *Faker) EmailFor(name string) string {
	var b strings.Builder

	dot := false

	for _, c := range ascii(strings.ToLower(name)) {
		if c >= 'a' && c <= 'z' || c >= '0' && c <= '9' {
			if dot && b.Len() > 0 {
				b.WriteByte('.')
			}

			b.WriteRune(c)

			dot = false
		} else {
			dot = true
		}
	}

	local := b.String()
	if local == "" {
		local = "user"
	}

	return fmt.Sprintf("%s.%d%04d@%s", local, f.seq, f.r.IntN(10000), Pick(f, domains...))
}

// Word returns a word in the Faker's locale (lorem ipsum for the zero
// Locale).
func (f *Faker) Word() string {
	if f.loc != nil {
		return Pick(f, f.loc.words...)
	}

	return Pick(f, words...)
}

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
	r, n := utf8.DecodeRuneInString(s)

	return string(unicode.ToUpper(r)) + s[n:] + "."
}

// Paragraph returns 3–6 sentences.
func (f *Faker) Paragraph() string {
	out := make([]string, f.Int(3, 6))
	for i := range out {
		out[i] = f.Sentence()
	}

	return strings.Join(out, " ")
}

// Slug returns a lowercase, dash-separated ASCII slug of 3 words.
func (f *Faker) Slug() string {
	return ascii(strings.ReplaceAll(f.Words(3), " ", "-")) + fmt.Sprintf("-%d", f.seq)
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

// Company returns a company name ("Baker Logistics LLC", "Silva & Costa
// Comércio Ltda").
func (f *Faker) Company() string {
	d := f.data()

	return fmt.Sprintf(Pick(f, d.companyFmt...), f.lastName(d), f.lastName(d), Pick(f, d.companyNoun...))
}

// ProductTitle returns a product title for a listing ("Rustic Oak Desk",
// "Cadeira Ergonômica de Aço").
func (f *Faker) ProductTitle() string {
	d := f.data()
	n := Pick(f, d.nouns...)

	return fmt.Sprintf(d.productFmt, n.word, adjective(Pick(f, d.adjectives...), n), Pick(f, d.materials...))
}

// ProductDescription returns a one-line product description built on
// ProductTitle.
func (f *Faker) ProductDescription() string {
	d := f.data()

	return fmt.Sprintf(d.describeFmt, f.ProductTitle(), Pick(f, d.uses...), Pick(f, d.benefits...))
}

// Category returns a store category ("Home & Kitchen", "Casa e Cozinha").
func (f *Faker) Category() string { return Pick(f, f.data().categories...) }

// StreetAddress returns a street and number ("742 Maple Ave", "Rua das
// Flores, 120").
func (f *Faker) StreetAddress() string {
	d := f.data()

	return fmt.Sprintf(d.streetFmt, Pick(f, d.streetTypes...), Pick(f, d.streets...), f.Int(1, 2999))
}

// City returns a city name.
func (f *Faker) City() string { return Pick(f, f.data().cities...)[0] }

// State returns a state code ("IL", "SP"), independent of City; Address
// keeps the two consistent.
func (f *Faker) State() string { return Pick(f, f.data().cities...)[1] }

// PostalCode returns a ZIP code (en) or a CEP (pt-BR, "01310-100").
func (f *Faker) PostalCode() string {
	if f.data() == &ptBRData {
		return fmt.Sprintf("%05d-%03d", f.Int(1000, 99999), f.Int(0, 999))
	}

	return fmt.Sprintf("%05d", f.Int(1000, 99999))
}

// Address returns a one-line postal address whose city and state match.
func (f *Faker) Address() string {
	d := f.data()
	street := f.StreetAddress()
	city := Pick(f, d.cities...)

	return fmt.Sprintf(d.addressFmt, street, city[0], city[1], f.PostalCode())
}

// FakeCPF returns a string in the Brazilian CPF format ("123.456.789-09")
// whose first check digit is wrong on purpose: it can never be a real
// person's CPF, and a CPF validator rejects it. Seed CPF columns directly;
// do not send it through validation meant for real documents.
func (f *Faker) FakeCPF() string {
	var d [11]int
	for i := range 9 {
		d[i] = f.r.IntN(10)
	}

	d[9] = (cpfDigit(d[:9]) + 1 + f.r.IntN(9)) % 10
	d[10] = cpfDigit(d[:10])

	return fmt.Sprintf("%d%d%d.%d%d%d.%d%d%d-%d%d", d[0], d[1], d[2], d[3], d[4], d[5], d[6], d[7], d[8], d[9], d[10])
}

// cpfDigit is the CPF check digit of digits (9 for the first, 10 for the
// second).
func cpfDigit(digits []int) int {
	sum := 0
	for i, n := range digits {
		sum += n * (len(digits) + 1 - i)
	}

	if r := sum * 10 % 11; r < 10 {
		return r
	}

	return 0
}

func (f *Faker) lastName(d *localeData) string { return Pick(f, d.lastNames...) }

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
		"à", "a", "á", "a", "â", "a", "ã", "a", "ä", "a",
		"è", "e", "é", "e", "ê", "e", "ë", "e",
		"ì", "i", "í", "i", "î", "i", "ï", "i",
		"ò", "o", "ó", "o", "ô", "o", "õ", "o", "ö", "o",
		"ù", "u", "ú", "u", "û", "u", "ü", "u",
		"ç", "c", "ñ", "n", "ý", "y", "ÿ", "y",
	)

	return repl.Replace(s)
}
