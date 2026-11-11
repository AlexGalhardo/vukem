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

func baselineSummary() Summary {
	return Summary{
		SchemaVersion:   1,
		Target:          "http://127.0.0.1:3000",
		Model:           "closed",
		Concurrency:     10,
		HTTPReqs:        6000,
		RPS:             600,
		HTTPReqFailed:   0.01,
		HTTPReqDuration: Trend{P95: 100 * time.Millisecond, P99: 200 * time.Millisecond},
	}
}

func TestCompare(t *testing.T) {
	t.Parallel()

	tol := DefaultTolerance()
	base := baselineSummary()

	tests := []struct {
		name   string
		edit   func(*Summary)
		metric string
		want   bool
	}{
		{"same run", func(*Summary) {}, "", false},
		{"p95 within tolerance", func(s *Summary) { s.HTTPReqDuration.P95 = 109 * time.Millisecond }, "p95", false},
		{"p95 over tolerance", func(s *Summary) { s.HTTPReqDuration.P95 = 111 * time.Millisecond }, "p95", true},
		{"p99 over tolerance", func(s *Summary) { s.HTTPReqDuration.P99 = 221 * time.Millisecond }, "p99", true},
		{"p95 faster", func(s *Summary) { s.HTTPReqDuration.P95 = 10 * time.Millisecond }, "p95", false},
		{"rps within tolerance", func(s *Summary) { s.RPS = 541 }, "rps", false},
		{"rps drop over tolerance", func(s *Summary) { s.RPS = 539 }, "rps", true},
		{"rps higher", func(s *Summary) { s.RPS = 900 }, "rps", false},
		{"errors within tolerance", func(s *Summary) { s.HTTPReqFailed = 0.014 }, "http_req_failed", false},
		{"errors over tolerance", func(s *Summary) { s.HTTPReqFailed = 0.016 }, "http_req_failed", true},
	}

	for _, tt := range tests {
		got := base
		tt.edit(&got)

		diffs, regressed := Compare(base, got, tol)
		if regressed != tt.want {
			t.Errorf("%s: regressed = %v, want %v (%+v)", tt.name, regressed, tt.want, diffs)
		}

		for _, d := range diffs {
			if d.Regressed != (tt.want && d.Metric == tt.metric) {
				t.Errorf("%s: metric %s Regressed = %v", tt.name, d.Metric, d.Regressed)
			}
		}
	}

	diffs, _ := Compare(base, base, tol)
	if len(diffs) != 4 || diffs[0].Metric != "p95" || diffs[1].Metric != "p99" || diffs[2].Metric != "rps" ||
		diffs[3].Metric != "http_req_failed" {
		t.Fatalf("diffs = %+v, want p95, p99, rps and http_req_failed", diffs)
	}
}

func TestCompareToleranceIsAdjustable(t *testing.T) {
	t.Parallel()

	base, got := baselineSummary(), baselineSummary()
	got.HTTPReqDuration.P95 = 130 * time.Millisecond

	if _, regressed := Compare(base, got, Tolerance{Latency: 0.5, RPS: 0.5, ErrorRate: 0.005}); regressed {
		t.Error("+30% p95 regressed under a 50% tolerance")
	}

	if _, regressed := Compare(base, got, Tolerance{}); !regressed {
		t.Error("+30% p95 passed under a zero tolerance")
	}
}

func TestReadSummaryChecksTheSchemaVersion(t *testing.T) {
	t.Parallel()

	var good bytes.Buffer
	if err := WriteJSON(&good, baselineSummary()); err != nil {
		t.Fatal(err)
	}

	if _, err := ReadSummary(&good); err != nil {
		t.Fatalf("a schema 1 baseline was refused: %v", err)
	}

	for _, bad := range []string{`{"schema_version":2}`, `{}`, `not json`} {
		if _, err := ReadSummary(strings.NewReader(bad)); err == nil {
			t.Errorf("ReadSummary(%q) was accepted", bad)
		}
	}
}

func TestCompareWarnsOnDifferentSetup(t *testing.T) {
	t.Parallel()

	base := baselineSummary()

	if w := SetupWarnings(base, base); len(w) != 0 {
		t.Fatalf("the same setup warned: %v", w)
	}

	got := base
	got.Model, got.Rate, got.Concurrency, got.Target = "open", "50/s", 0, "http://127.0.0.1:4000"

	w := SetupWarnings(base, got)
	if len(w) != 4 {
		t.Fatalf("warnings = %v, want one each for model, rate, concurrency and target", w)
	}

	var b bytes.Buffer

	diffs, _ := Compare(base, got, DefaultTolerance())
	if err := WriteCompare(&b, ".vukem/bench/baseline.json", diffs, w); err != nil {
		t.Fatal(err)
	}

	out := b.String()
	if !strings.Contains(out, "compare  .vukem/bench/baseline.json") || !strings.Contains(out, "warning") ||
		!strings.Contains(out, "p95") {
		t.Fatalf("output:\n%s", out)
	}
}

func TestCompareZeroBaselineCannotHideARegression(t *testing.T) {
	t.Parallel()

	base := baselineSummary()
	base.HTTPReqDuration.P95, base.HTTPReqDuration.P99, base.RPS = 0, 0, 0

	if diffs, regressed := Compare(base, baselineSummary(), DefaultTolerance()); !regressed {
		t.Fatalf("a baseline with zero values judged %+v as fine", diffs)
	}

	// Nothing to be worse than: two zero latencies do not regress.
	empty := baselineSummary()
	empty.HTTPReqDuration.P95, empty.HTTPReqDuration.P99 = 0, 0

	diffs, _ := Compare(empty, empty, DefaultTolerance())
	for _, d := range diffs {
		if (d.Metric == "p95" || d.Metric == "p99") && d.Regressed {
			t.Errorf("%s regressed between two zero latencies", d.Metric)
		}
	}
}

func TestReadSummaryIsBounded(t *testing.T) {
	t.Parallel()

	big := `{"schema_version":1,"http_reqs":1,"target":"` + strings.Repeat("a", 5<<20) + `"}`
	if _, err := ReadSummary(strings.NewReader(big)); err == nil {
		t.Fatal("a 5 MiB baseline was read")
	}

	if _, err := ReadSummary(strings.NewReader(`{"schema_version":1,"http_reqs":0}`)); err == nil {
		t.Fatal("a baseline with no request was accepted: there is nothing to compare with")
	}
}

func TestParseTolerance(t *testing.T) {
	t.Parallel()

	got, err := ParseTolerance("25%")
	if err != nil || got.Latency != 0.25 || got.RPS != 0.25 || got.ErrorRate != DefaultTolerance().ErrorRate {
		t.Fatalf("ParseTolerance(25%%) = %+v, %v", got, err)
	}

	if got, err := ParseTolerance("100"); err != nil || got.Latency != 1 {
		t.Fatalf("a bare number is a percentage too: %+v, %v", got, err)
	}

	for _, bad := range []string{"", "lots", "-1%", "100.1%", "1000%", "NaN", "Inf", "%"} {
		if _, err := ParseTolerance(bad); err == nil {
			t.Errorf("ParseTolerance(%q) was accepted", bad)
		}
	}
}

func TestGateReadsTheBaselineBeforeSavingOverIt(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "baseline.json")
	fast := baselineSummary()

	var out bytes.Buffer
	if err := (Gate{Save: path}).Apply(&out, fast); err != nil {
		t.Fatal(err)
	}

	slow := fast
	slow.HTTPReqDuration.P95 *= 3

	// --save X --compare X: the baseline is the file as it was, not the run that is about to replace it.
	err := (Gate{Save: path, Compare: path, Tolerance: DefaultTolerance()}).Apply(&out, slow)
	if !errors.Is(err, ErrRegression) {
		t.Fatalf("err = %v, want ErrRegression: saving first made the run its own baseline", err)
	}

	f, openErr := os.Open(path)
	if openErr != nil {
		t.Fatal(openErr)
	}

	defer f.Close()

	if saved, err := ReadSummary(f); err != nil || saved.HTTPReqDuration.P95 != slow.HTTPReqDuration.P95 {
		t.Fatalf("the new run was not saved: %+v, %v", saved.HTTPReqDuration, err)
	}
}

func TestGateFailsWhenTheRunSentNothing(t *testing.T) {
	t.Parallel()

	th, err := ParseThreshold("error_rate<1%")
	if err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer

	// A run that sent no request has a failure rate of 0, which would pass: it must fail instead.
	err = (Gate{Thresholds: []Threshold{th}}).Apply(&out, Summary{SchemaVersion: 1})
	if !errors.Is(err, ErrThresholds) || !strings.Contains(out.String(), "no request") {
		t.Fatalf("err = %v, out = %q, want ErrThresholds with a clear message", err, out.String())
	}

	base := filepath.Join(t.TempDir(), "baseline.json")
	if err := (Gate{Save: base}).Apply(&out, baselineSummary()); err != nil {
		t.Fatal(err)
	}

	err = (Gate{Compare: base, Tolerance: DefaultTolerance()}).Apply(&out, Summary{SchemaVersion: 1})
	if !errors.Is(err, ErrThresholds) {
		t.Fatalf("a comparison on an empty run = %v, want ErrThresholds", err)
	}

	// Without a gate there is nothing to judge.
	if err := (Gate{}).Apply(&out, Summary{SchemaVersion: 1}); err != nil {
		t.Fatalf("no gate, no failure: %v", err)
	}
}
