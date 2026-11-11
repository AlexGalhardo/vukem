package loadtest

import (
	"math"
	"math/bits"
	"time"
)

const (
	// linear is the number of exact one-microsecond buckets and of sub-buckets per power of two.
	linear = 32
	// histBuckets covers every int64 microsecond value: 32 exact buckets (index 0..31), then 32 sub-buckets for each
	// shift e = 0..57 (a value below 2^63 has at most 63 bits and e = bits-6). That is 59 groups of 32.
	histBuckets = 59 * linear
)

// Histogram counts durations in log-linear buckets of fixed size, with a worst-case relative error of 1/32 (about
// 3.1%) on a quantile. The zero value is ready; it is not safe for concurrent use.
type Histogram struct {
	counts   [histBuckets]uint64
	n        uint64
	sum      float64 // nanoseconds; a float so a huge sample cannot overflow it
	min, max time.Duration
}

// Bucket is one row of a latency histogram: the samples in [From, To].
type Bucket struct {
	From  time.Duration `json:"from_ns"`
	To    time.Duration `json:"to_ns"`
	Count uint64        `json:"count"`
}

// bucketIndex maps a value in microseconds to its bucket: exact below 32, then 32 sub-buckets per power of two.
func bucketIndex(v uint64) int {
	if v < linear {
		return int(v)
	}

	e := bits.Len64(v) - 6
	m := v >> e

	return linear + e*linear + int(m-linear) //nolint:gosec // G115: m is 32..63
}

// bucketUpper returns the largest value, in microseconds, that falls in bucket i.
func bucketUpper(i int) uint64 {
	if i < linear {
		return uint64(i) //nolint:gosec // G115: i is a bucket index, never negative
	}

	e := uint((i - linear) / linear)
	m := uint64(linear + (i-linear)%linear)

	return ((m + 1) << e) - 1
}

// Record adds one duration. A negative duration counts as zero.
func (h *Histogram) Record(d time.Duration) {
	d = max(d, 0)

	if h.n == 0 || d < h.min {
		h.min = d
	}

	if d > h.max {
		h.max = d
	}

	h.counts[bucketIndex(uint64(d.Microseconds()))]++ //nolint:gosec // G115: d was clamped to be non-negative
	h.n++
	h.sum += float64(d)
}

// Count returns the number of recorded durations.
func (h *Histogram) Count() uint64 { return h.n }

// Min returns the smallest recorded duration, exactly.
func (h *Histogram) Min() time.Duration { return h.min }

// Max returns the largest recorded duration, exactly.
func (h *Histogram) Max() time.Duration { return h.max }

// Mean returns the arithmetic mean of the recorded durations.
func (h *Histogram) Mean() time.Duration {
	if h.n == 0 {
		return 0
	}

	return time.Duration(h.sum / float64(h.n))
}

// Quantile returns the duration below which a fraction q of the samples fall, for q in [0,1]. It reports the upper
// edge of the bucket that holds the sample (in whole microseconds: the bucket holds values truncated to a
// microsecond, so a sample can also read up to 1us low), which errs high by at most 1/32; the top quantile is the
// exact Max.
func (h *Histogram) Quantile(q float64) time.Duration {
	if h.n == 0 {
		return 0
	}

	if q <= 0 {
		return h.min
	}

	target := uint64(math.Ceil(min(q, 1) * float64(h.n)))
	target = max(target, 1)

	var cum uint64

	for i, c := range &h.counts {
		if c == 0 {
			continue
		}

		cum += c
		if cum >= target {
			if cum == h.n {
				return h.max
			}

			return time.Duration(bucketUpper(i)) * time.Microsecond //nolint:gosec // G115: at most 2^53 microseconds
		}
	}

	return h.max
}

// Merge adds the samples of o to h.
func (h *Histogram) Merge(o *Histogram) {
	if o.n == 0 {
		return
	}

	if h.n == 0 || o.min < h.min {
		h.min = o.min
	}

	h.max = max(h.max, o.max)
	h.n += o.n
	h.sum += o.sum

	for i, c := range &o.counts {
		h.counts[i] += c
	}
}

// Buckets returns n rows that cover Min..Max on a logarithmic scale, for a text or SVG histogram. It is nil for an
// empty histogram or n < 1, and a single row when every sample is equal.
func (h *Histogram) Buckets(n int) []Bucket {
	if h.n == 0 || n < 1 {
		return nil
	}

	if h.min == h.max {
		return []Bucket{{From: h.min, To: h.max, Count: h.n}}
	}

	lo := float64(max(h.min, time.Microsecond))
	span := math.Log(float64(h.max) / lo)

	// Below a microsecond (max under the 1us floor) or equal after the floor, there is no range to split.
	if span <= 0 {
		return []Bucket{{From: h.min, To: h.max, Count: h.n}}
	}

	rows := make([]Bucket, n)
	edge := func(k int) time.Duration { return time.Duration(lo * math.Exp(span*float64(k)/float64(n))) }

	for k := range rows {
		rows[k].From, rows[k].To = edge(k), edge(k+1)
	}

	rows[0].From, rows[n-1].To = h.min, h.max

	for i, c := range &h.counts {
		if c == 0 {
			continue
		}

		edge := time.Duration(bucketUpper(i)) * time.Microsecond //nolint:gosec // G115: at most 2^53 microseconds
		upper := min(max(edge, h.min), h.max)
		k := int(math.Log(float64(max(upper, time.Duration(lo)))/lo) / span * float64(n))

		rows[min(k, n-1)].Count += c
	}

	return rows
}
