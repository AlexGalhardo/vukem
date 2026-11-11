package loadtest

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"
)

// FuzzParseRate checks that ParseRate never panics, and that a rate it accepts survives a trip through String.
func FuzzParseRate(f *testing.F) {
	for _, s := range []string{
		"50/s", "300/1m", "50", "5/m", "0/s", "-1/s", "x", "10/0s", "10/", "/", "", "1/1ns", "100000/s", "1/-1s",
		"9223372036854775807/s", "1/9223372036854775807ns", "1/2562047h47m16.854775807s", " 5/s ", "5/ s", "1e3/s", "+5/s",
		"5//s", "５/s", "5/1h1h", "5/.5s", "5/1.s",
	} {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, s string) {
		r, err := ParseRate(s)
		if err != nil {
			return
		}

		if r.Freq < 1 || r.Per <= 0 {
			t.Fatalf("ParseRate(%q) = %+v: an accepted rate must be positive", s, r)
		}

		back, err := ParseRate(r.String())
		if err != nil || back != r {
			t.Fatalf("ParseRate(%q) = %+v, but its String %q parses to %+v (%v)", s, r, r.String(), back, err)
		}
	})
}

// stagesText writes stages in the syntax ParseStages reads.
func stagesText(stages []Stage) string {
	parts := make([]string, len(stages))
	for i, s := range stages {
		parts[i] = fmt.Sprintf("%s:%d", s.Duration, s.Target)
	}

	return strings.Join(parts, ",")
}

// FuzzParseStages checks that ParseStages never panics and that the stages it accepts are valid, bounded and stable
// when written back and parsed again.
func FuzzParseStages(f *testing.F) {
	for _, s := range []string{
		"30s:50,1m:200,30s:0", "30s:50", "", ",", "30s", "x:10", "30s:-1", "0s:10", "-5s:10", "30s:10,", "61m:1",
		"30m:1,31m:1", "1s:100001", "1s:100000", "1h:0", "1ns:1", "9223372036854775807ns:1", "30s:9223372036854775807",
		"30s:+5", " 30s : 5 ", "30s:5,,30s:5", "1.5s:5", "1h0m0s:1", "30s:05",
	} {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, s string) {
		stages, err := ParseStages(s)
		if err != nil {
			return
		}

		if len(stages) == 0 || stagesDuration(stages) > maxDuration || stagesDuration(stages) <= 0 {
			t.Fatalf("ParseStages(%q) = %+v: empty or over the duration cap", s, stages)
		}

		for _, st := range stages {
			if st.Duration <= 0 || st.Target < 0 || st.Target > maxRate {
				t.Fatalf("ParseStages(%q) accepted the stage %+v", s, st)
			}
		}

		// The pure interpolation must not panic or leave the range of the targets, at any instant.
		top := 0
		for _, st := range stages {
			top = max(top, st.Target)
		}

		for _, at := range []time.Duration{0, time.Nanosecond, stagesDuration(stages) / 2, stagesDuration(stages), 2 * stagesDuration(stages)} {
			if v := targetAt(stages, at); v < 0 || v > float64(top) {
				t.Fatalf("targetAt(%q, %v) = %v, outside 0..%d", s, at, v, top)
			}
		}

		back, err := ParseStages(stagesText(stages))
		if err != nil || !slices.Equal(back, stages) {
			t.Fatalf(
				"ParseStages(%q) = %+v, but its text %q parses to %+v (%v)",
				s,
				stages,
				stagesText(stages),
				back,
				err,
			)
		}
	})
}

// FuzzParseThreshold checks that ParseThreshold never panics, and that a threshold it accepts can be evaluated and
// printed against any summary without a panic.
func FuzzParseThreshold(f *testing.F) {
	for _, s := range []string{
		"p95<200ms", "p50<1s", "p99>=1.5s", "max>0s", "error_rate<1%", "error_rate<0.01", "rps>100", "dropped<=0",
		"iteration_error_rate<5%", "ttfb_p95<1s", "checkout:p95<300ms", `"checkout: POST orders.store":p95<300ms`,
		`"a":b:p95<1s`, `"":p95<1s`, `"unterminated:p95<1s`, `"a"p95<1s`, ":p95<1s", "p95<", "<", "<=", ">=", "", " ",
		"p95<NaN", "rps>+Inf", "rps<-1", "error_rate<150%", "p95<1e9h", "p95<9223372036854775807ns", "p95 < 1s",
		"p95<<1s", "p95<=<1s", "a:b:c", "p95<1s\x00", "p95<1s%", "dropped<9223372036854775807", "\"\xff\":p95<1s",
	} {
		f.Add(s)
	}

	sum := Summary{
		SchemaVersion:    1,
		HTTPReqs:         10,
		RPS:              5,
		HTTPReqFailed:    0.1,
		Iterations:       4,
		IterationsFailed: 1,
		HTTPReqDuration: Trend{
			Min:  time.Millisecond,
			Mean: 2 * time.Millisecond,
			P50:  time.Millisecond,
			P90:  3 * time.Millisecond,
			P95:  4 * time.Millisecond,
			P99:  5 * time.Millisecond,
			Max:  6 * time.Millisecond,
		},
		ByName: map[string]NameSummary{"a": {HTTPReqs: 1}, "checkout: POST orders.store": {HTTPReqs: 2}},
	}

	f.Fuzz(func(t *testing.T, s string) {
		th, err := ParseThreshold(s)
		if err != nil {
			return
		}

		results := Evaluate(sum, []Threshold{th})
		if len(results) != 1 || results[0].Threshold != s {
			t.Fatalf("Evaluate(%q) = %+v: one result carrying the text it was parsed from", s, results)
		}

		if len(Check(sum, []Threshold{th})) > 1 {
			t.Fatalf("Check(%q) returned more failures than thresholds", s)
		}
	})
}
