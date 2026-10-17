package database

import (
	"database/sql"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
)

func TestScanTime(t *testing.T) {
	t.Parallel()

	utc := time.Date(2026, 1, 2, 3, 4, 5, 600000000, time.UTC)
	minus3 := time.Date(2026, 1, 2, 0, 4, 5, 600000000, time.FixedZone("", -3*3600))

	tests := []struct {
		name string
		src  any
		want time.Time
	}{
		{"time.Time", utc, utc},
		{"driver default", "2026-01-02 03:04:05.6 +0000 UTC", utc},
		{"monotonic suffix", "2026-01-02 00:04:05.6 -0300 -03 m=+0.012", minus3},
		{"sqlite format", "2026-01-02 03:04:05.6+00:00", utc},
		{"offset", "2026-01-02 00:04:05.6-03:00", minus3},
		{"T separator", "2026-01-02T03:04:05.6+00:00", utc},
		{"RFC 3339 Z", "2026-01-02T03:04:05.6Z", utc},
		{"no zone is UTC", "2026-01-02 03:04:05.6", utc},
		{"bytes", []byte("2026-01-02 03:04:05.6"), utc},
		{"minutes", "2026-01-02 03:04", time.Date(2026, 1, 2, 3, 4, 0, 0, time.UTC)},
		{"date", "2026-01-02", time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var (
				plain time.Time
				ptr   *time.Time
				null  sql.NullTime
				gen   sql.Null[time.Time]
			)

			for _, s := range []sql.Scanner{ScanTime(&plain), ScanTime(&ptr), ScanTime(&null), ScanTime(&gen)} {
				if err := s.Scan(tt.src); err != nil {
					t.Fatalf("Scan(%v) = %v", tt.src, err)
				}
			}

			if !plain.Equal(tt.want) || ptr == nil || !ptr.Equal(tt.want) ||
				!null.Valid || !null.Time.Equal(tt.want) || !gen.Valid || !gen.V.Equal(tt.want) {
				t.Fatalf("Scan(%v) = %v, %v, %+v, %+v, want %v", tt.src, plain, ptr, null, gen, tt.want)
			}

			_, offset := plain.Zone()
			if _, wantOffset := tt.want.Zone(); offset != wantOffset {
				t.Errorf("Scan(%v) offset = %d, want %d", tt.src, offset, wantOffset)
			}
		})
	}
}

func TestScanTimeNULL(t *testing.T) {
	t.Parallel()

	ptr := new(time.Now())
	null := sql.NullTime{Time: time.Now(), Valid: true}
	gen := sql.Null[time.Time]{V: time.Now(), Valid: true}

	for _, s := range []sql.Scanner{ScanTime(&ptr), ScanTime(&null), ScanTime(&gen)} {
		if err := s.Scan(nil); err != nil {
			t.Fatalf("Scan(nil) = %v", err)
		}
	}

	if diff := cmp.Diff([]bool{true, false, false}, []bool{ptr == nil, null.Valid, gen.Valid}); diff != "" {
		t.Errorf("Scan(nil) (-want +got):\n%s", diff)
	}

	if !null.Time.IsZero() || !gen.V.IsZero() {
		t.Errorf("Scan(nil) kept the old time: %v, %v", null.Time, gen.V)
	}

	var plain time.Time
	if err := ScanTime(&plain).Scan(nil); err == nil {
		t.Error("Scan(nil) into time.Time = nil error, want an error")
	}
}

func TestScanTimeRejects(t *testing.T) {
	t.Parallel()

	for _, src := range []any{"tomorrow", "2026-13-01", "", int64(1767323045), 1.5, true} {
		var got time.Time
		if err := ScanTime(&got).Scan(src); err == nil {
			t.Errorf("Scan(%#v) = %v, nil error, want an error", src, got)
		}
	}
}
