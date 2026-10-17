package uuid

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/google/go-cmp/cmp"
)

// RFC 9562 appendix A.6 test vector (version 7, 2022-02-22 19:22:22 UTC).
const rfcV7 = "017f22e2-79b0-7cc3-98c4-dc0c0c07398f"

func TestNewVersionAndVariant(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		new     func() UUID
		version int
	}{
		{"v4", NewV4, 4},
		{"v7", NewV7, 7},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			seen := map[UUID]bool{}

			for range 1000 {
				u := tt.new()
				if got := u.Version(); got != tt.version {
					t.Fatalf("%s.Version() = %d, want %d", u, got, tt.version)
				}

				if u[8]&0xc0 != 0x80 {
					t.Fatalf("%s variant bits = %02x, want 10xxxxxx", u, u[8])
				}

				if seen[u] {
					t.Fatalf("duplicate %s", u)
				}

				seen[u] = true
			}
		})
	}
}

func TestNewV7Time(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		now := time.Now()

		got, ok := NewV7().Time()
		if want := now.Truncate(time.Millisecond).UTC(); !ok || !got.Equal(want) {
			t.Fatalf("NewV7().Time() = %v, %v, want %v", got, ok, want)
		}

		if _, ok := NewV4().Time(); ok {
			t.Fatal("NewV4().Time() ok = true, want false")
		}
	})
}

func TestNewV7SortsByTime(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		for range 100 {
			a := NewV7()

			time.Sleep(time.Millisecond) // fake clock inside the bubble

			if b := NewV7(); a.String() >= b.String() || slices.Compare(a[:], b[:]) >= 0 {
				t.Fatalf("NewV7() %s made before %s sorts after it", a, b)
			}
		}
	})
}

func TestParse(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in      string
		want    string
		version int
		err     bool
	}{
		{in: rfcV7, want: rfcV7, version: 7},
		{in: "919108F7-52D1-4320-9BAC-F847DB4148A8", want: "919108f7-52d1-4320-9bac-f847db4148a8", version: 4},
		{in: "00000000-0000-0000-0000-000000000000", want: "00000000-0000-0000-0000-000000000000"},
		{in: "", err: true},
		{in: "017f22e2-79b0-7cc3-98c4-dc0c0c07398", err: true},
		{in: "017f22e2-79b0-7cc3-98c4-dc0c0c07398ff", err: true},
		{in: "017f22e279b07cc398c4dc0c0c07398f", err: true},
		{in: "{017f22e2-79b0-7cc3-98c4-dc0c0c07398f}", err: true},
		{in: "017f22e2_79b0-7cc3-98c4-dc0c0c07398f", err: true},
		{in: "017f22e2-79b0-7cc3-98c4-dc0c0c07398g", err: true},
		{in: "017f22e2-79b0-7cc3-98c4-dc0c0c07398é", err: true},
	}

	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			t.Parallel()

			u, err := Parse(tt.in)
			if tt.err {
				if !errors.Is(err, ErrInvalid) {
					t.Fatalf("Parse(%q) = %v, %v, want ErrInvalid", tt.in, u, err)
				}

				return
			}

			if err != nil || u.String() != tt.want || u.Version() != tt.version {
				t.Fatalf("Parse(%q) = %s (v%d), %v, want %s (v%d)", tt.in, u, u.Version(), err, tt.want, tt.version)
			}
		})
	}
}

func TestRFCVectorTime(t *testing.T) {
	t.Parallel()

	got, ok := MustParse(rfcV7).Time()
	want := time.Date(2022, 2, 22, 19, 22, 22, 0, time.UTC)

	if !ok || !got.Equal(want) {
		t.Fatalf("Time() = %v, %v, want %v", got, ok, want)
	}
}

func TestMustParsePanics(t *testing.T) {
	t.Parallel()

	defer func() {
		if recover() == nil {
			t.Fatal("MustParse(bad) did not panic")
		}
	}()

	MustParse("bad")
}

func TestIsZero(t *testing.T) {
	t.Parallel()

	if !(UUID{}).IsZero() || NewV4().IsZero() {
		t.Fatal("IsZero mismatch")
	}
}

type order struct {
	ID     UUID  `json:"id"`
	Parent *UUID `json:"parent"`
}

func TestJSON(t *testing.T) {
	t.Parallel()

	in := order{ID: MustParse(rfcV7)}

	b, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}

	if want := `{"id":"` + rfcV7 + `","parent":null}`; string(b) != want {
		t.Fatalf("json.Marshal = %s, want %s", b, want)
	}

	var out order
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}

	if diff := cmp.Diff(in, out); diff != "" {
		t.Fatalf("round trip (-want +got):\n%s", diff)
	}

	for _, bad := range []string{`{"id":""}`, `{"id":"nope"}`, `{"id":42}`} {
		if err := json.Unmarshal([]byte(bad), &out); err == nil {
			t.Errorf("json.Unmarshal(%s) = nil error, want one", bad)
		}
	}
}

func TestSQL(t *testing.T) {
	t.Parallel()

	u := MustParse(rfcV7)

	v, err := u.Value()
	if err != nil || v != rfcV7 {
		t.Fatalf("Value() = %v, %v, want %q", v, err, rfcV7)
	}

	tests := []struct {
		name string
		src  any
		err  bool
	}{
		{"string", rfcV7, false},
		{"text bytes", []byte(strings.ToUpper(rfcV7)), false},
		{"binary(16)", slices.Clone(u[:]), false},
		{"array", [16]byte(u), false},
		{"null", nil, true},
		{"bad text", "nope", true},
		{"bad bytes", []byte{1, 2, 3}, true},
		{"int", int64(1), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var got UUID

			err := got.Scan(tt.src)
			if tt.err {
				if err == nil {
					t.Fatalf("Scan(%v) = nil error, want one", tt.src)
				}

				return
			}

			if err != nil || got != u {
				t.Fatalf("Scan(%v) = %s, %v, want %s", tt.src, got, err, u)
			}
		})
	}
}
