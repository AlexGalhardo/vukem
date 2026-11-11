package loadtest

import (
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
	"time"
)

// metricKind says how the value of a threshold is written and shown.
type metricKind int

const (
	kindDuration metricKind = iota
	kindFraction
	kindNumber
	kindCount
)

// metricInfo describes a threshold metric: its kind, whether a per-name scope is meaningful, and how to read it.
type metricInfo struct {
	kind   metricKind
	scoped bool
	value  func(s Summary) float64
	named  func(n NameSummary) float64
}

func durationMetric(
	pick func(Trend) time.Duration,
	from func(Summary) Trend,
	named func(NameSummary) Trend,
) metricInfo {
	m := metricInfo{kind: kindDuration, value: func(s Summary) float64 { return float64(pick(from(s))) }}

	if named != nil {
		m.scoped = true
		m.named = func(n NameSummary) float64 { return float64(pick(named(n))) }
	}

	return m
}

func thresholdMetrics() map[string]metricInfo {
	dur := func(s Summary) Trend { return s.HTTPReqDuration }
	wait := func(s Summary) Trend { return s.HTTPReqWaiting }
	ndur := func(n NameSummary) Trend { return n.HTTPReqDuration }

	return map[string]metricInfo{
		"p50":  durationMetric(func(t Trend) time.Duration { return t.P50 }, dur, ndur),
		"p90":  durationMetric(func(t Trend) time.Duration { return t.P90 }, dur, ndur),
		"p95":  durationMetric(func(t Trend) time.Duration { return t.P95 }, dur, ndur),
		"p99":  durationMetric(func(t Trend) time.Duration { return t.P99 }, dur, ndur),
		"max":  durationMetric(func(t Trend) time.Duration { return t.Max }, dur, ndur),
		"mean": durationMetric(func(t Trend) time.Duration { return t.Mean }, dur, ndur),
		"min":  durationMetric(func(t Trend) time.Duration { return t.Min }, dur, ndur),

		"ttfb_p95": durationMetric(func(t Trend) time.Duration { return t.P95 }, wait, nil),
		"ttfb_p99": durationMetric(func(t Trend) time.Duration { return t.P99 }, wait, nil),

		"error_rate": {
			kind: kindFraction, scoped: true,
			value: func(s Summary) float64 { return s.HTTPReqFailed },
			named: func(n NameSummary) float64 { return n.HTTPReqFailed },
		},
		"rps":     {kind: kindNumber, value: func(s Summary) float64 { return s.RPS }},
		"dropped": {kind: kindCount, value: func(s Summary) float64 { return float64(s.DroppedIterations) }},
		"iteration_error_rate": {kind: kindFraction, value: func(s Summary) float64 {
			if s.Iterations == 0 {
				return 0
			}

			return float64(s.IterationsFailed) / float64(s.Iterations)
		}},
	}
}

// Threshold is a pass/fail condition on a Summary, such as p95<200ms.
type Threshold struct {
	scope  string
	metric string
	op     string
	value  float64
	raw    string
}

// ParseThreshold parses "<metric><op><value>" with an optional "<name>:" or "\"<name with spaces>\":" prefix that
// restricts it to one entry of Summary.ByName. Metrics: p50 p90 p95 p99 max mean min (durations), ttfb_p95 ttfb_p99,
// error_rate and iteration_error_rate (a fraction such as 0.01 or a percentage such as 1%), rps and dropped. The
// operators are <, <=, > and >=.
func ParseThreshold(s string) (Threshold, error) {
	raw := s
	scope := ""

	if strings.HasPrefix(s, `"`) {
		end := strings.Index(s[1:], `"`)
		if end < 0 || !strings.HasPrefix(s[end+2:], ":") {
			return Threshold{}, fmt.Errorf(
				"loadtest: invalid threshold %q: a quoted name must be followed by a colon",
				raw,
			)
		}

		scope, s = s[1:end+1], s[end+3:]
	} else if name, rest, ok := strings.Cut(s, ":"); ok {
		scope, s = name, rest
	}

	i := strings.IndexAny(s, "<>")
	if i <= 0 {
		return Threshold{}, fmt.Errorf(
			"loadtest: invalid threshold %q: want metric<value, metric<=value, metric>value or metric>=value",
			raw,
		)
	}

	op := s[i : i+1]
	rest := s[i+1:]

	if strings.HasPrefix(rest, "=") {
		op, rest = op+"=", rest[1:]
	}

	metric := strings.TrimSpace(s[:i])

	info, ok := thresholdMetrics()[metric]
	if !ok {
		return Threshold{}, fmt.Errorf("loadtest: invalid threshold %q: unknown metric %q", raw, metric)
	}

	if scope != "" && !info.scoped {
		return Threshold{}, fmt.Errorf("loadtest: invalid threshold %q: %s cannot be restricted to a name", raw, metric)
	}

	v, err := parseThresholdValue(info.kind, strings.TrimSpace(rest))
	if err != nil {
		return Threshold{}, fmt.Errorf("loadtest: invalid threshold %q: %w", raw, err)
	}

	return Threshold{scope: scope, metric: metric, op: op, value: v, raw: raw}, nil
}

// finite rejects NaN and the infinities, which ParseFloat accepts and every comparison would mishandle.
func finite(f float64) bool { return !math.IsNaN(f) && !math.IsInf(f, 0) }

func parseThresholdValue(kind metricKind, v string) (float64, error) {
	switch kind {
	case kindDuration:
		d, err := time.ParseDuration(v)
		if err != nil {
			return 0, fmt.Errorf("%q is not a duration with a unit, such as 200ms", v)
		}

		return float64(d), nil
	case kindFraction:
		if p, ok := strings.CutSuffix(v, "%"); ok {
			f, err := strconv.ParseFloat(p, 64)
			if err != nil || !finite(f) || f < 0 || f > 100 {
				return 0, fmt.Errorf("%q is not a percentage between 0%% and 100%%", v)
			}

			return f / 100, nil
		}

		f, err := strconv.ParseFloat(v, 64)
		if err != nil || !finite(f) || f < 0 || f > 1 {
			return 0, fmt.Errorf("%q is not a fraction between 0 and 1", v)
		}

		return f, nil
	case kindNumber:
		// Handled below, with the other plain numbers.
	case kindCount:
		n, err := strconv.ParseUint(v, 10, 63)
		if err != nil {
			return 0, fmt.Errorf("%q is not a whole number", v)
		}

		return float64(n), nil
	}

	f, err := strconv.ParseFloat(v, 64)
	if err != nil || !finite(f) || f < 0 {
		return 0, fmt.Errorf("%q is not a number", v)
	}

	return f, nil
}

// ThresholdResult is the outcome of one threshold.
type ThresholdResult struct {
	// Threshold is the text it was parsed from.
	Threshold string
	// Got is the measured value, formatted, or why it could not be measured.
	Got string
	OK  bool
}

// Evaluate checks every threshold against s. A threshold restricted to a name that is not in s.ByName fails: a typo
// must not make a run pass.
func Evaluate(s Summary, ts []Threshold) []ThresholdResult {
	metrics := thresholdMetrics()
	out := make([]ThresholdResult, 0, len(ts))

	for _, t := range ts {
		info := metrics[t.metric]
		res := ThresholdResult{Threshold: t.raw}

		var got float64

		if t.scope == "" {
			got = info.value(s)
		} else if n, ok := s.ByName[t.scope]; ok {
			got = info.named(n)
		} else {
			res.Got = fmt.Sprintf("no such name %q in the run", t.scope)
			out = append(out, res)

			continue
		}

		res.Got, res.OK = formatMetric(info.kind, got), holds(got, t.op, t.value)
		out = append(out, res)
	}

	return out
}

// Check returns the thresholds that do not hold.
func Check(s Summary, ts []Threshold) (failed []ThresholdResult) {
	for _, r := range Evaluate(s, ts) {
		if !r.OK {
			failed = append(failed, r)
		}
	}

	return failed
}

func holds(got float64, op string, want float64) bool {
	switch op {
	case "<":
		return got < want
	case "<=":
		return got <= want
	case ">":
		return got > want
	}

	return got >= want
}

func formatMetric(kind metricKind, v float64) string {
	switch kind {
	case kindDuration:
		return fmtDur(time.Duration(v))
	case kindFraction:
		return fmt.Sprintf("%.2f%%", v*100)
	case kindCount:
		return strconv.FormatInt(int64(v), 10)
	case kindNumber:
		// Shown with one decimal, below.
	}

	return strconv.FormatFloat(v, 'f', 1, 64)
}

// WriteThresholds prints the outcome of every threshold and how many failed.
func WriteThresholds(w io.Writer, results []ThresholdResult) error {
	var b strings.Builder

	b.WriteString("thresholds\n")

	width := 0
	for _, r := range results {
		width = max(width, len(r.Threshold))
	}

	failed := 0

	for _, r := range results {
		mark := "ok   "
		if !r.OK {
			mark, failed = "FAIL ", failed+1
		}

		fmt.Fprintf(&b, "  %s %-*s  got %s\n", mark, width, r.Threshold, r.Got)
	}

	if failed > 0 {
		fmt.Fprintf(&b, "%d of %d thresholds failed\n", failed, len(results))
	}

	_, err := io.WriteString(w, b.String())

	return err
}
