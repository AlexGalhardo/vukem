package ulid

import (
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

// canonical is the text shape of an id: 26 Crockford base32 characters,
// upper case, with I, L, O and U left out.
var canonical = regexp.MustCompile(`^[0-9ABCDEFGHJKMNPQRSTVWXYZ]{26}$`)

// The ULID specification's example, which other implementations encode the
// same way. It pins the bit layout: a wrong shift or a reversed alphabet
// would still pass every property test below, but not this one.
const (
	specID = "01ARYZ6S41TSV4RRFFQ69G5FAV"
	specMS = 1469918176385
)

func TestNewShape(t *testing.T) {
	t.Parallel()

	id := NewGenerator().New()

	if len(id) != Length {
		t.Fatalf("len(New()) = %d, want %d", len(id), Length)
	}

	if !canonical.MatchString(id) {
		t.Fatalf("New() = %q, want %d Crockford base32 characters", id, Length)
	}
}

func TestZeroGeneratorIsReady(t *testing.T) {
	t.Parallel()

	var g Generator

	if id := g.New(); !Valid(id) {
		t.Fatalf("(&Generator{}).New() = %q, want a valid id", id)
	}
}

func TestNewStampsTheCurrentTime(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		now := time.Now()

		got, ok := TimeOf(NewGenerator().New())
		if want := now.Truncate(time.Millisecond).UTC(); !ok || !got.Equal(want) {
			t.Fatalf("TimeOf(New()) = %s, %v, want %s", got, ok, want)
		}
	})
}

func TestEncodingMatchesTheSpecification(t *testing.T) {
	t.Parallel()

	at, ok := TimeOf(specID)
	if !ok {
		t.Fatalf("TimeOf(%q) rejected a specification id", specID)
	}

	if want := time.UnixMilli(specMS).UTC(); !at.Equal(want) {
		t.Fatalf("TimeOf(%q) = %s, want %s", specID, at, want)
	}

	got := NewGenerator().At(time.UnixMilli(specMS))
	if want := specID[:10]; got[:10] != want {
		t.Fatalf("At(%d) = %q, want the timestamp characters %q", specMS, got, want)
	}
}

func TestAtIsSortableByTime(t *testing.T) {
	t.Parallel()

	g := NewGenerator()
	base := time.Date(2026, time.October, 18, 12, 0, 0, 0, time.UTC)

	got := make([]string, 0, 5)
	for i := range 5 {
		got = append(got, g.At(base.Add(time.Duration(i)*time.Second)))
	}

	if want := slices.Sorted(slices.Values(got)); !slices.Equal(got, want) {
		t.Fatalf("ids from increasing times are not lexicographically sorted:\n%v", got)
	}
}

// Two ids made in the same millisecond must still differ and must still
// sort in creation order: a list ordered by id would otherwise shuffle rows
// created in the same tick.
func TestAtSameMillisecondIsMonotonic(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, time.October, 18, 12, 0, 0, 0, time.UTC)
	g := NewGenerator()

	got := make([]string, 0, 100)
	for range 100 {
		got = append(got, g.At(at))
	}

	for i, id := range got {
		if !Valid(id) {
			t.Fatalf("id %d (%q) is not valid", i, id)
		}

		if i > 0 && id <= got[i-1] {
			t.Fatalf("id %d (%q) does not sort after %q", i, id, got[i-1])
		}
	}
}

// A clock that steps backwards (an NTP correction, a container resuming)
// must not produce an id that sorts before one already handed out.
func TestAtKeepsOrderWhenTheClockGoesBackwards(t *testing.T) {
	t.Parallel()

	g := NewGenerator()
	at := time.Date(2026, time.October, 18, 12, 0, 0, 0, time.UTC)

	first := g.At(at)
	second := g.At(at.Add(-time.Hour))

	if second <= first {
		t.Fatalf("after the clock went back, %q does not sort after %q", second, first)
	}

	if third := g.At(at.Add(-24 * time.Hour)); third <= second {
		t.Fatalf("after the clock went back again, %q does not sort after %q", third, second)
	}
}

// A time the 48-bit timestamp cannot hold is clamped instead of silently
// encoding as some other time.
func TestAtClampsTimesTheFormatCannotHold(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		in     time.Time
		prefix string
	}{
		"before the epoch": {time.Date(1900, time.January, 1, 0, 0, 0, 0, time.UTC), "0000000000"},
		"past year 10889":  {time.Date(20000, time.January, 1, 0, 0, 0, 0, time.UTC), "7ZZZZZZZZZ"},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			id := NewGenerator().At(tc.in)
			if !Valid(id) {
				t.Fatalf("At(%s) = %q, want a valid id", tc.in, id)
			}

			if got := id[:len(tc.prefix)]; got != tc.prefix {
				t.Fatalf("At(%s) = %q, want it to start with %q", tc.in, id, tc.prefix)
			}
		})
	}
}

func TestNewIsUnique(t *testing.T) {
	t.Parallel()

	const n = 20_000

	g := NewGenerator()
	seen := make(map[string]struct{}, n)

	for range n {
		id := g.New()
		if _, dup := seen[id]; dup {
			t.Fatalf("duplicate id %q", id)
		}

		seen[id] = struct{}{}
	}
}

func TestNewIsSafeForConcurrentUse(t *testing.T) {
	t.Parallel()

	const workers, each = 8, 500

	var (
		mu  sync.Mutex
		all = make(map[string]struct{}, workers*each)
		wg  sync.WaitGroup
	)

	g := NewGenerator()

	for range workers {
		wg.Go(func() {
			local := make([]string, 0, each)
			for range each {
				local = append(local, g.New())
			}

			mu.Lock()
			defer mu.Unlock()

			for _, id := range local {
				all[id] = struct{}{}
			}
		})
	}

	wg.Wait()

	if len(all) != workers*each {
		t.Fatalf("got %d unique ids, want %d", len(all), workers*each)
	}
}

func TestValid(t *testing.T) {
	t.Parallel()

	generated := NewGenerator().New()
	zeros := strings.Repeat("0", Length)

	tests := map[string]struct {
		in   string
		want bool
	}{
		"generated":             {generated, true},
		"specification id":      {specID, true},
		"all zeros":             {zeros, true},
		"largest":               {"7" + strings.Repeat("Z", Length-1), true},
		"empty":                 {"", false},
		"too short":             {zeros[:Length-1], false},
		"too long":              {zeros + "0", false},
		"letter I":              {zeros[:Length-1] + "I", false},
		"letter L":              {zeros[:Length-1] + "L", false},
		"letter O":              {zeros[:Length-1] + "O", false},
		"letter U":              {zeros[:Length-1] + "U", false},
		"excluded letter first": {"I" + zeros[:Length-1], false},
		"lower case":            {strings.ToLower(generated), false},
		"hyphen":                {zeros[:Length-1] + "-", false},
		"space":                 {zeros[:Length-1] + " ", false},
		"null byte":             {zeros[:Length-1] + "\x00", false},
		"non-ascii":             {zeros[:Length-1] + "é", false},
		"overflows 128 bits":    {"8" + zeros[:Length-1], false},
		"overflows at Z":        {"Z" + zeros[:Length-1], false},
		"uuid":                  {"0199f2a0-1d3b-7c6e-9a1f-0c2b4d6e8f10", false},
		"sql injection shape":   {"' OR 1=1 --              ", false},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if got := Valid(tc.in); got != tc.want {
				t.Fatalf("Valid(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestTimeOfRoundTrips(t *testing.T) {
	t.Parallel()

	g := NewGenerator()

	for _, want := range []time.Time{
		time.UnixMilli(0).UTC(),
		time.Date(2026, time.October, 18, 12, 34, 56, 789_000_000, time.UTC),
		time.UnixMilli(maxMS).UTC(),
	} {
		id := g.At(want)

		got, ok := TimeOf(id)
		if !ok {
			t.Fatalf("TimeOf(%q) reported an invalid id", id)
		}

		if !got.Equal(want) {
			t.Fatalf("TimeOf(At(%s)) = %s, want %s", want, got, want)
		}
	}
}

// Sub-millisecond precision is not part of the format: it is truncated, not
// rounded, so an id never claims a time that has not happened yet.
func TestTimeOfTruncatesToMilliseconds(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, time.October, 18, 12, 0, 0, 999_999, time.UTC)

	got, ok := TimeOf(NewGenerator().At(at))
	if want := at.Truncate(time.Millisecond); !ok || !got.Equal(want) {
		t.Fatalf("TimeOf(At(%s)) = %s, %v, want %s", at, got, ok, want)
	}
}

func TestTimeOfRejectsAnInvalidID(t *testing.T) {
	t.Parallel()

	for _, bad := range []string{"", "not-an-id", strings.Repeat("I", Length)} {
		if got, ok := TimeOf(bad); ok || !got.IsZero() {
			t.Fatalf("TimeOf(%q) = %s, %v, want the zero time and false", bad, got, ok)
		}
	}
}
