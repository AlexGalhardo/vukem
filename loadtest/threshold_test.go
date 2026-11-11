package loadtest

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func thresholdSummary() Summary {
	return Summary{
		SchemaVersion:     1,
		Target:            "http://127.0.0.1:3000",
		Model:             "closed",
		Concurrency:       10,
		HTTPReqs:          1000,
		RPS:               250,
		HTTPReqFailed:     0.005,
		DroppedIterations: 2,
		Iterations:        100,
		IterationsFailed:  5,
		HTTPReqDuration: Trend{
			Min:  time.Millisecond,
			Mean: 20 * time.Millisecond,
			P50:  15 * time.Millisecond,
			P90:  40 * time.Millisecond,
			P95:  60 * time.Millisecond,
			P99:  90 * time.Millisecond,
			Max:  200 * time.Millisecond,
		},
		HTTPReqWaiting: Trend{P95: 50 * time.Millisecond, P99: 80 * time.Millisecond},
		ByName: map[string]NameSummary{
			"checkout: POST orders.store": {
				HTTPReqs:        100,
				HTTPReqFailed:   0.1,
				HTTPReqDuration: Trend{P95: 300 * time.Millisecond},
			},
			"browse: GET /": {HTTPReqs: 900, HTTPReqDuration: Trend{P95: 50 * time.Millisecond}},
		},
	}
}

func TestParseThreshold(t *testing.T) {
	t.Parallel()

	valid := []string{
		"p50<1s", "p90<=1s", "p95<200ms", "p99>1ms", "max>=1ms", "mean<1.5s", "min<1ms", "ttfb_p95<1s", "ttfb_p99<1s",
		"error_rate<0.01", "error_rate<1%", "rps>100", "rps >= 100", "dropped<=0", "iteration_error_rate<5%",
		"checkout:p95<300ms", `"checkout: POST orders.store":p95<300ms`, `"browse: GET /":error_rate<1%`,
	}

	for _, s := range valid {
		if _, err := ParseThreshold(s); err != nil {
			t.Errorf("ParseThreshold(%q) = %v", s, err)
		}
	}

	invalid := []string{
		"",
		"p95",
		"p95<",
		"<200ms",
		"bogus<1",
		"p95=200ms",
		"p95<200",
		"p95<fast",
		"error_rate<x",
		"error_rate<150%",
		"rps<-1",
		"error_rate<NaN",
		"error_rate<nan",
		"rps<Inf",
		"rps>+Inf",
		"rps<NaN",
		"rps>-Inf",
		"dropped<1.5",
		`"unterminated:p95<1s`,
		`"a"p95<1s`,
		"a:ttfb_p95<1s",
		"a:rps>1",
		"a:dropped<1",
		"a:iteration_error_rate<1%",
	}

	for _, s := range invalid {
		if _, err := ParseThreshold(s); err == nil {
			t.Errorf("ParseThreshold(%q) was accepted", s)
		}
	}
}

func TestCheck(t *testing.T) {
	t.Parallel()

	s := thresholdSummary()

	parse := func(exprs ...string) []Threshold {
		var ts []Threshold

		for _, e := range exprs {
			th, err := ParseThreshold(e)
			if err != nil {
				t.Fatal(err)
			}

			ts = append(ts, th)
		}

		return ts
	}

	holds := parse("p95<=60ms", "p99<100ms", "max<=200ms", "mean<21ms", "ttfb_p95<60ms", "error_rate<=0.5%", "rps>=250",
		"dropped<=2", "iteration_error_rate<=5%", `"browse: GET /":p95<60ms`, "p50>10ms")

	if failed := Check(s, holds); len(failed) != 0 {
		t.Fatalf("thresholds that hold failed: %+v", failed)
	}

	fails := parse(
		"p95<59ms",
		"error_rate<0.4%",
		"rps>251",
		"dropped<2",
		"iteration_error_rate<4%",
		`"checkout: POST orders.store":p95<299ms`,
	)

	failed := Check(s, fails)
	if len(failed) != len(fails) {
		t.Fatalf("%d of %d failing thresholds were reported: %+v", len(failed), len(fails), failed)
	}

	if failed[0].Threshold != "p95<59ms" || failed[0].Got != "60.0ms" {
		t.Fatalf("first failure = %+v", failed[0])
	}

	all := Evaluate(s, parse("p95<200ms", "p95<1ms"))
	if len(all) != 2 || !all[0].OK || all[1].OK {
		t.Fatalf("Evaluate = %+v", all)
	}
}

func TestSecurity_A04_BenchThresholdUnknownScopeFails(t *testing.T) {
	t.Parallel()

	th, err := ParseThreshold("typo:p95<10s")
	if err != nil {
		t.Fatal(err)
	}

	failed := Check(thresholdSummary(), []Threshold{th})
	if len(failed) != 1 || !strings.Contains(failed[0].Got, "no such") {
		t.Fatalf("a threshold on a name that does not exist must fail, got %+v", failed)
	}

	if failed := Check(Summary{SchemaVersion: 1}, []Threshold{th}); len(failed) != 1 {
		t.Fatal("a summary with no per-name data must fail a scoped threshold too")
	}
}

func TestWriteThresholds(t *testing.T) {
	t.Parallel()

	var ts []Threshold

	for _, e := range []string{"error_rate<1%", "p95<10ms"} {
		th, err := ParseThreshold(e)
		if err != nil {
			t.Fatal(err)
		}

		ts = append(ts, th)
	}

	var b bytes.Buffer

	if err := WriteThresholds(&b, Evaluate(thresholdSummary(), ts)); err != nil {
		t.Fatal(err)
	}

	out := b.String()
	for _, want := range []string{"thresholds\n", "  ok    error_rate<1%", "got 0.50%", "  FAIL  p95<10ms", "got 60.0ms", "1 of 2 thresholds failed"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
}

func TestGateApply(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	save := filepath.Join(dir, "nested", "baseline.json")
	s := thresholdSummary()

	var out bytes.Buffer

	if err := (Gate{Save: save}).Apply(&out, s); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(save); err != nil {
		t.Fatalf("baseline not saved: %v", err)
	}

	// Comparing a run with itself holds.
	if err := (Gate{Compare: save, Tolerance: DefaultTolerance()}).Apply(&out, s); err != nil {
		t.Fatalf("a run regressed against itself: %v", err)
	}

	slower := s
	slower.HTTPReqDuration.P95 *= 2

	if err := (Gate{Compare: save, Tolerance: DefaultTolerance()}).Apply(&out, slower); !errors.Is(err, ErrRegression) {
		t.Fatalf("err = %v, want ErrRegression", err)
	}

	th, err := ParseThreshold("p95<1ms")
	if err != nil {
		t.Fatal(err)
	}

	// Both fail: the threshold wins.
	if err := (Gate{Thresholds: []Threshold{th}, Compare: save, Tolerance: DefaultTolerance()}).Apply(
		&out,
		slower,
	); !errors.Is(
		err,
		ErrThresholds,
	) {
		t.Fatalf("err = %v, want ErrThresholds to take precedence", err)
	}

	if err := (Gate{Compare: filepath.Join(dir, "missing.json")}).Apply(
		&out,
		s,
	); err == nil ||
		errors.Is(err, ErrRegression) {
		t.Fatalf("a missing baseline = %v, want a plain error", err)
	}
}
