package loadtest

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Tolerance is how much worse than a baseline a run may be.
type Tolerance struct {
	// Latency is the allowed relative increase of p95 and p99.
	Latency float64
	// RPS is the allowed relative drop of the throughput.
	RPS float64
	// ErrorRate is the allowed absolute increase of http_req_failed.
	ErrorRate float64
}

// DefaultTolerance allows 10% on latency and throughput and 0.5 percentage points on the failure rate.
func DefaultTolerance() Tolerance { return Tolerance{Latency: 0.10, RPS: 0.10, ErrorRate: 0.005} }

// Diff is one compared metric.
type Diff struct {
	Metric    string
	Base, Got string
	// Change is the relative change, or the difference in percentage points for the failure rate.
	Change    string
	Limit     string
	Regressed bool
}

// Compare reports how a run differs from a baseline on p95, p99, rps and http_req_failed, and whether any of them is
// worse than tol allows.
func Compare(base, got Summary, tol Tolerance) (diffs []Diff, regressed bool) {
	add := func(d Diff) {
		diffs = append(diffs, d)
		regressed = regressed || d.Regressed
	}

	add(latencyDiff("p95", base.HTTPReqDuration.P95, got.HTTPReqDuration.P95, tol.Latency))
	add(latencyDiff("p99", base.HTTPReqDuration.P99, got.HTTPReqDuration.P99, tol.Latency))

	change := relative(base.RPS, got.RPS)
	add(Diff{
		Metric: "rps", Base: fmt.Sprintf("%.1f", base.RPS), Got: fmt.Sprintf("%.1f", got.RPS),
		Change: fmt.Sprintf("%+.1f%%", change*100), Limit: fmt.Sprintf("-%.1f%%", tol.RPS*100),
		// A baseline without throughput cannot say the run is fine.
		Regressed: base.RPS <= 0 || got.RPS < base.RPS*(1-tol.RPS),
	})

	pp := (got.HTTPReqFailed - base.HTTPReqFailed) * 100
	add(Diff{
		Metric:    "http_req_failed",
		Base:      fmt.Sprintf("%.2f%%", base.HTTPReqFailed*100),
		Got:       fmt.Sprintf("%.2f%%", got.HTTPReqFailed*100),
		Change:    fmt.Sprintf("%+.2fpp", pp),
		Limit:     fmt.Sprintf("+%.2fpp", tol.ErrorRate*100),
		Regressed: got.HTTPReqFailed-base.HTTPReqFailed > tol.ErrorRate,
	})

	return diffs, regressed
}

func latencyDiff(metric string, base, got time.Duration, tol float64) Diff {
	return Diff{
		Metric: metric,
		Base:   fmtDur(base),
		Got:    fmtDur(got),
		Change: fmt.Sprintf(
			"%+.1f%%",
			relative(float64(base), float64(got))*100,
		),
		Limit: fmt.Sprintf("+%.1f%%", tol*100),
		// A zero baseline has nothing to be relative to, and a run slower than nothing is a regression.
		Regressed: float64(got) > float64(base)*(1+tol),
	}
}

func relative(base, got float64) float64 {
	if base == 0 {
		return 0
	}

	return (got - base) / base
}

// maxBaselineBytes bounds a baseline file: a Summary is a few kilobytes, and a larger file is not one.
const maxBaselineBytes = 4 << 20

// ParseTolerance reads a percentage such as "10%" (the % is optional) between 0 and 100 and applies it to latency and
// throughput; the failure rate keeps the default allowance.
func ParseTolerance(s string) (Tolerance, error) {
	tol := DefaultTolerance()

	v, err := strconv.ParseFloat(strings.TrimSuffix(strings.TrimSpace(s), "%"), 64)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) || v < 0 || v > 100 {
		return tol, fmt.Errorf("loadtest: tolerance %q is not a percentage between 0%% and 100%%", s)
	}

	tol.Latency, tol.RPS = v/100, v/100

	return tol, nil
}

// ReadSummary reads a Summary saved with WriteJSON. Only schema_version 1 is understood.
func ReadSummary(r io.Reader) (Summary, error) {
	var s Summary
	if err := json.NewDecoder(io.LimitReader(r, maxBaselineBytes)).Decode(&s); err != nil {
		return Summary{}, fmt.Errorf("loadtest: read baseline: %w", err)
	}

	if s.HTTPReqs == 0 {
		return Summary{}, errors.New("loadtest: the baseline holds no request, so there is nothing to compare with")
	}

	if s.SchemaVersion != 1 {
		return Summary{}, fmt.Errorf(
			"loadtest: the baseline has schema_version %d, this version reads 1",
			s.SchemaVersion,
		)
	}

	return s, nil
}

// SetupWarnings lists what differs between the setup of a baseline and of a run: comparing different setups misleads.
func SetupWarnings(base, got Summary) []string {
	var w []string

	for _, c := range []struct{ name, b, g string }{
		{"model", base.Model, got.Model},
		{"rate", base.Rate, got.Rate},
		{"concurrency", strconv.Itoa(base.Concurrency), strconv.Itoa(got.Concurrency)},
		{"target", base.Target, got.Target},
	} {
		if c.b != c.g {
			w = append(
				w,
				fmt.Sprintf("the baseline's %s is %q, this run's is %q: the comparison may mislead", c.name, c.b, c.g),
			)
		}
	}

	return w
}

// WriteCompare prints the comparison table.
func WriteCompare(w io.Writer, path string, diffs []Diff, warnings []string) error {
	var b strings.Builder

	fmt.Fprintf(&b, "compare  %s\n", path)

	for _, wn := range warnings {
		b.WriteString("  warning  " + wn + "\n")
	}

	for _, d := range diffs {
		verdict := "ok"
		if d.Regressed {
			verdict = "REGRESSED (limit " + d.Limit + ")"
		}

		fmt.Fprintf(&b, "  %-16s %9s -> %-9s %9s   %s\n", d.Metric, d.Base, d.Got, d.Change, verdict)
	}

	_, err := io.WriteString(w, b.String())

	return err
}

var (
	// ErrThresholds reports that a threshold did not hold.
	ErrThresholds = errors.New("loadtest: thresholds failed")
	// ErrRegression reports a run worse than its baseline.
	ErrRegression = errors.New("loadtest: the run regressed against the baseline")
)

// Gate is what a run must satisfy and where it is saved.
type Gate struct {
	Thresholds []Threshold
	// Save writes the summary as JSON, creating the directory.
	Save string
	// Compare is a baseline file; the run must not be worse than Tolerance allows.
	Compare   string
	Tolerance Tolerance
}

// Apply saves the summary, evaluates the thresholds and compares with the baseline, writing the tables to w. It
// returns ErrThresholds when a threshold fails, which wins over ErrRegression, and a plain error when a file cannot
// be written or read.
func (g Gate) Apply(w io.Writer, s Summary) error {
	judging := len(g.Thresholds) > 0 || g.Compare != ""

	// A run that sent nothing has no failures and no latency: every threshold on them would pass, so it fails.
	if judging && s.HTTPReqs == 0 {
		fmt.Fprintln(w, "thresholds\n  FAIL  the run sent no request, so nothing can be judged")

		return fmt.Errorf("%w: the run sent no request", ErrThresholds)
	}

	// The baseline is read before --save can replace it: with the same file for both, the run would be its own baseline.
	var base *Summary

	if g.Compare != "" {
		b, err := readBaseline(g.Compare)
		if err != nil {
			return err
		}

		base = &b
	}

	if g.Save != "" {
		if s.HTTPReqs == 0 {
			return errors.New("loadtest: the run sent no request, so there is nothing to save")
		}

		if err := saveSummary(g.Save, s); err != nil {
			return err
		}
	}

	thresholdsFailed, regressed := false, false

	if len(g.Thresholds) > 0 {
		results := Evaluate(s, g.Thresholds)
		thresholdsFailed = len(Check(s, g.Thresholds)) > 0

		if err := WriteThresholds(w, results); err != nil {
			return err
		}
	}

	if base != nil {
		var err error
		if regressed, err = g.compare(w, *base, s); err != nil {
			return err
		}
	}

	switch {
	case thresholdsFailed:
		return ErrThresholds
	case regressed:
		return ErrRegression
	}

	return nil
}

func readBaseline(path string) (Summary, error) {
	f, err := os.Open(path)
	if err != nil {
		return Summary{}, fmt.Errorf("loadtest: read baseline: %w", err)
	}

	defer f.Close()

	return ReadSummary(f)
}

func (g Gate) compare(w io.Writer, base, s Summary) (bool, error) {
	diffs, regressed := Compare(base, s, g.Tolerance)

	return regressed, WriteCompare(w, g.Compare, diffs, SetupWarnings(base, s))
}

func saveSummary(path string, s Summary) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}

	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}

	if err := WriteJSON(f, s); err != nil {
		_ = f.Close()

		return err
	}

	return f.Close()
}
