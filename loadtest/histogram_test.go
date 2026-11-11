package loadtest

import (
	"math"
	"slices"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
)

func TestHistogramExactBelow32us(t *testing.T) {
	t.Parallel()

	var h Histogram

	for v := range 32 {
		h.Record(time.Duration(v) * time.Microsecond)
	}

	for v := range 32 {
		// The (v+1)-th smallest value is v microseconds; its bucket is exact.
		q := float64(v+1) / 32

		if got, want := h.Quantile(q), time.Duration(v)*time.Microsecond; got != want {
			t.Errorf("Quantile(%v) = %v, want %v", q, got, want)
		}
	}
}

func TestHistogramQuantileError(t *testing.T) {
	t.Parallel()

	const n = 100_000

	// A deterministic spread over six decades, 1 microsecond to 10 seconds.
	values := make([]time.Duration, n)
	x := uint64(12345)

	for i := range values {
		x = x*6364136223846793005 + 1442695040888963407
		frac := float64(x>>11) / (1 << 53)
		values[i] = time.Duration(math.Pow(10, frac*7)) * time.Microsecond
	}

	var h Histogram
	for _, v := range values {
		h.Record(v)
	}

	slices.Sort(values)

	for _, q := range []float64{0.5, 0.9, 0.99, 0.999} {
		exact := values[int(math.Ceil(q*n))-1]
		got := h.Quantile(q)

		if rel := math.Abs(float64(got-exact)) / float64(exact); rel > 0.032 {
			t.Errorf("Quantile(%v) = %v, exact %v: relative error %.4f > 0.032", q, got, exact, rel)
		}
	}
}

func TestHistogramMinMaxMean(t *testing.T) {
	t.Parallel()

	var h Histogram
	for _, d := range []time.Duration{3 * time.Millisecond, 500 * time.Nanosecond, 9 * time.Millisecond} {
		h.Record(d)
	}

	if h.Count() != 3 || h.Min() != 500*time.Nanosecond || h.Max() != 9*time.Millisecond {
		t.Fatalf("count=%d min=%v max=%v", h.Count(), h.Min(), h.Max())
	}

	want := (3*time.Millisecond + 500*time.Nanosecond + 9*time.Millisecond) / 3
	if h.Mean() != want {
		t.Fatalf("mean = %v, want %v", h.Mean(), want)
	}
}

func TestHistogramMerge(t *testing.T) {
	t.Parallel()

	var a, b, all Histogram

	for i := range 2000 {
		d := time.Duration(i*i) * time.Microsecond
		all.Record(d)

		if i%2 == 0 {
			a.Record(d)
		} else {
			b.Record(d)
		}
	}

	a.Merge(&b)

	if diff := cmp.Diff(all.Buckets(10), a.Buckets(10)); diff != "" {
		t.Fatalf("merged buckets differ (-all +merged):\n%s", diff)
	}

	if a.Count() != all.Count() || a.Min() != all.Min() || a.Max() != all.Max() || a.Mean() != all.Mean() {
		t.Fatalf("merged stats differ: %d/%v/%v/%v vs %d/%v/%v/%v",
			a.Count(), a.Min(), a.Max(), a.Mean(), all.Count(), all.Min(), all.Max(), all.Mean())
	}
}

func TestHistogramEmpty(t *testing.T) {
	t.Parallel()

	var h Histogram

	if h.Count() != 0 || h.Min() != 0 || h.Max() != 0 || h.Mean() != 0 || h.Quantile(0.99) != 0 || h.Buckets(5) != nil {
		t.Fatal("an empty histogram must report zeros")
	}

	var empty Histogram

	h.Merge(&empty)
}

func TestHistogramHugeValue(t *testing.T) {
	t.Parallel()

	var h Histogram

	h.Record(time.Duration(math.MaxInt64))
	h.Record(-time.Second)

	if h.Count() != 2 || h.Max() != time.Duration(math.MaxInt64) || h.Min() != 0 {
		t.Fatalf("count=%d min=%v max=%v", h.Count(), h.Min(), h.Max())
	}

	if got := h.Quantile(1); got != time.Duration(math.MaxInt64) {
		t.Fatalf("Quantile(1) = %v, want the exact max", got)
	}
}

func TestHistogramBucketsCoverRange(t *testing.T) {
	t.Parallel()

	var h Histogram
	for _, ms := range []int{2, 3, 20, 25, 110} {
		h.Record(time.Duration(ms) * time.Millisecond)
	}

	rows := h.Buckets(10)
	if len(rows) != 10 || rows[0].From != 2*time.Millisecond || rows[len(rows)-1].To != 110*time.Millisecond {
		t.Fatalf("rows = %+v", rows)
	}

	var total uint64
	for _, r := range rows {
		total += r.Count
	}

	if total != 5 {
		t.Fatalf("rows hold %d samples, want 5", total)
	}
}
