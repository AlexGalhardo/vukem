package factory

import (
	"net/mail"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"

	"github.com/google/go-cmp/cmp"
)

// The zero Locale must keep the data seeded apps and golden tests were
// built on before locales existed.
func TestFakerZeroLocaleKeepsLegacyOutput(t *testing.T) {
	t.Parallel()

	f := NewFaker(42)
	f.seq = 1

	got := []string{f.Name(), f.Email(), f.Username(), f.Sentence(), f.Slug()}
	want := []string{
		"João Almeida", "olivia.lima.18383@example.net", "felipe1367",
		"Irure laboris commodo nisi nostrud in nostrud magna.", "commodo-nostrud-duis-1",
	}

	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("NewFaker(42) output changed (-want +got):\n%s", diff)
	}

	if NewFaker(1).Locale().Name() != "" {
		t.Error("NewFaker locale is not the zero Locale")
	}
}

func TestFakerLocalesAreDeterministic(t *testing.T) {
	t.Parallel()

	sample := func(f *Faker) []string {
		f.seq = 3

		return []string{
			f.Name(), f.Email(), f.Sentence(), f.Company(), f.ProductTitle(), f.ProductDescription(),
			f.Category(), f.StreetAddress(), f.City(), f.State(), f.PostalCode(), f.Address(), f.FakeCPF(),
			f.EmailFor("Ana Luíza"),
		}
	}

	for _, l := range []Locale{{}, LocaleEN(), LocalePTBR()} {
		t.Run(l.Name(), func(t *testing.T) {
			t.Parallel()

			a, b := sample(NewFakerWithLocale(7, l)), sample(NewFakerWithLocale(7, l))
			if diff := cmp.Diff(a, b); diff != "" {
				t.Errorf("same seed, different data (-first +second):\n%s", diff)
			}

			if c := sample(NewFakerWithLocale(8, l)); slices.Equal(a, c) {
				t.Error("different seeds, same data")
			}
		})
	}
}

func TestFakerLocaleData(t *testing.T) {
	t.Parallel()

	tests := []struct {
		locale Locale
		data   *localeData
	}{
		{LocaleEN(), &enData},
		{LocalePTBR(), &ptBRData},
	}

	for _, tt := range tests {
		t.Run(tt.locale.Name(), func(t *testing.T) {
			t.Parallel()

			f := NewFakerWithLocale(99, tt.locale)
			d := tt.data

			for i := range 50 {
				f.seq = i + 1

				first, last, _ := strings.Cut(f.Name(), " ")
				if !slices.Contains(d.firstNames, first) || !slices.Contains(d.lastNames, last) {
					t.Fatalf("Name() = %q, not from the %s data", first+" "+last, d.tag)
				}

				if w := f.Word(); !slices.Contains(d.words, w) {
					t.Fatalf("Word() = %q, not from the %s data", w, d.tag)
				}

				if c := f.Category(); !slices.Contains(d.categories, c) {
					t.Fatalf("Category() = %q, not from the %s data", c, d.tag)
				}

				city := f.City()
				if !slices.ContainsFunc(d.cities, func(c [2]string) bool { return c[0] == city }) {
					t.Fatalf("City() = %q, not from the %s data", city, d.tag)
				}

				if s := f.Sentence(); !strings.HasSuffix(s, ".") || !utf8.ValidString(s) ||
					!unicode.IsUpper(firstRune(s)) {
					t.Fatalf("Sentence() = %q", s)
				}

				for _, s := range []string{f.Company(), f.ProductTitle(), f.ProductDescription(), f.StreetAddress()} {
					if s == "" || strings.Contains(s, "%!") {
						t.Fatalf("formatted text = %q", s)
					}
				}
			}
		})
	}
}

func TestFakerPTBR(t *testing.T) {
	t.Parallel()

	f := NewFakerWithLocale(5, LocalePTBR())
	cep := regexp.MustCompile(`^\d{5}-\d{3}$`)
	address := regexp.MustCompile(`^(Rua|Avenida|Travessa|Alameda|Praça) .+, \d+ - .+/[A-Z]{2}, \d{5}-\d{3}$`)

	for range 50 {
		if p := f.PostalCode(); !cep.MatchString(p) {
			t.Fatalf("PostalCode() = %q, want a CEP", p)
		}

		if a := f.Address(); !address.MatchString(a) {
			t.Fatalf("Address() = %q", a)
		}

		// Adjectives agree with the noun's gender ("Cadeira Ergonômica",
		// never "Cadeira Ergonômico").
		title := f.ProductTitle()
		for _, n := range ptBRData.nouns {
			if !strings.HasPrefix(title, n.word+" ") {
				continue
			}

			adj := strings.Fields(strings.TrimPrefix(title, n.word+" "))[0]
			if n.feminine && strings.HasSuffix(adj, "o") || !n.feminine && strings.HasSuffix(adj, "a") {
				t.Fatalf("ProductTitle() = %q: adjective does not agree", title)
			}
		}
	}
}

func TestFakerEN(t *testing.T) {
	t.Parallel()

	f := NewFakerWithLocale(5, LocaleEN())
	address := regexp.MustCompile(`^\d+ \w+ \w+, [A-Za-z ]+, [A-Z]{2} \d{5}$`)

	for range 50 {
		if a := f.Address(); !address.MatchString(a) {
			t.Fatalf("Address() = %q", a)
		}
	}
}

// FakeCPF must never be a valid CPF (it could be a real person's), while
// keeping the format apps display and store.
func TestFakerFakeCPFIsNeverValid(t *testing.T) {
	t.Parallel()

	format := regexp.MustCompile(`^\d{3}\.\d{3}\.\d{3}-\d{2}$`)
	f := NewFaker(11)

	for range 2000 {
		cpf := f.FakeCPF()
		if !format.MatchString(cpf) {
			t.Fatalf("FakeCPF() = %q, want ###.###.###-##", cpf)
		}

		if validCPF(cpf) {
			t.Fatalf("FakeCPF() = %q has valid check digits", cpf)
		}
	}
}

func validCPF(s string) bool {
	digits := strings.NewReplacer(".", "", "-", "").Replace(s)
	d := make([]int, len(digits))

	for i, c := range digits {
		d[i], _ = strconv.Atoi(string(c))
	}

	return cpfDigit(d[:9]) == d[9] && cpfDigit(d[:10]) == d[10]
}

func TestFakerEmailFor(t *testing.T) {
	t.Parallel()

	f := NewFaker(3)
	f.seq = 2

	for _, name := range []string{"Ana Luíza", "José da Conceição", "Zoë O'Brien-Smith", "   ", "李雷"} {
		email := f.EmailFor(name)

		addr, err := mail.ParseAddress(email)
		if err != nil || addr.Address != email {
			t.Fatalf("EmailFor(%q) = %q, not a plain address: %v", name, email, err)
		}

		local, domain, _ := strings.Cut(email, "@")
		if !slices.Contains(domains, domain) || !regexp.MustCompile(`^[a-z0-9.]+$`).MatchString(local) {
			t.Fatalf("EmailFor(%q) = %q", name, email)
		}
	}

	if got := f.EmailFor("Ana Luíza"); !strings.HasPrefix(got, "ana.luiza.") {
		t.Errorf("EmailFor(Ana Luíza) = %q, want prefix ana.luiza.", got)
	}
}

func TestFactoryLocale(t *testing.T) {
	t.Parallel()

	base := New(func(f *Faker) string { return f.Locale().Name() + ":" + f.Category() })

	got := base.Locale(LocalePTBR()).Seed(1).Count(20).Make()
	for _, s := range got {
		cat, ok := strings.CutPrefix(s, "pt-BR:")
		if !ok || !slices.Contains(ptBRData.categories, cat) {
			t.Fatalf("Make() = %q, want pt-BR data", s)
		}
	}

	if diff := cmp.Diff(got, base.Locale(LocalePTBR()).Seed(1).Count(20).Make()); diff != "" {
		t.Errorf("seeded Make differs (-first +second):\n%s", diff)
	}

	if s := base.MakeOne(); !strings.HasPrefix(s, ":") {
		t.Errorf("base factory locale = %q, want the zero Locale (Locale returns a copy)", s)
	}
}

func firstRune(s string) rune {
	r, _ := utf8.DecodeRuneInString(s)
	return r
}
