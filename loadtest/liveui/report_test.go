package liveui

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/AlexGalhardo/vukem/loadtest"
)

var update = flag.Bool("update", false, "rewrite golden files")

func fixedSeries() []Snapshot {
	out := make([]Snapshot, 0, 6)

	for i := range 6 {
		out = append(out, Snapshot{
			Second: i, Reqs: uint64(100 + 20*i), Failed: uint64(i % 2), P50Micros: int64(10_000 + 500*i),
			P95Micros: int64(25_000 + 1000*i), P99Micros: int64(40_000 + 2000*i), Active: 8 + i, TargetRate: 10,
			TotalReqs: uint64(100*(i+1) + 10*i*(i+1)), TotalFailed: uint64(i), Dropped: 0,
		})
	}

	return out
}

func fixedSummary() loadtest.Summary {
	return loadtest.Summary{
		SchemaVersion: 1,
		Target:        "http://127.0.0.1:3000/posts?token=hidden",
		Method:        "GET",
		Model:         "closed",
		Concurrency:   10,
		Elapsed:       6 * time.Second,
		HTTPReqs:      750,
		Arrivals:      750,
		RPS:           125,
		HTTPReqFailed: 0.004,
		HTTPReqDuration: loadtest.Trend{
			Min:  time.Millisecond,
			Mean: 12 * time.Millisecond,
			P50:  11 * time.Millisecond,
			P90:  20 * time.Millisecond,
			P95:  27 * time.Millisecond,
			P99:  44 * time.Millisecond,
			Max:  90 * time.Millisecond,
		},
		Status:       map[string]uint64{"200": 747, "500": 3},
		Errors:       map[string]uint64{"timeout": 1},
		DataReceived: 1_500_000,
		Histogram: []loadtest.Bucket{
			{From: time.Millisecond, To: 8 * time.Millisecond, Count: 100},
			{From: 8 * time.Millisecond, To: 16 * time.Millisecond, Count: 500},
			{From: 16 * time.Millisecond, To: 90 * time.Millisecond, Count: 150},
		},
		ByName: map[string]loadtest.NameSummary{
			"browse: GET /posts": {
				HTTPReqs:      600,
				HTTPReqFailed: 0.005,
				HTTPReqDuration: loadtest.Trend{
					P50: 10 * time.Millisecond,
					P95: 25 * time.Millisecond,
					P99: 40 * time.Millisecond,
				},
			},
			"browse: GET posts.show": {
				HTTPReqs: 150,
				HTTPReqDuration: loadtest.Trend{
					P50: 12 * time.Millisecond,
					P95: 30 * time.Millisecond,
					P99: 50 * time.Millisecond,
				},
			},
		},
	}
}

func fixedOptions() []ReportOption {
	return []ReportOption{
		WithGeneratedAt(time.Date(2026, 11, 11, 15, 4, 5, 0, time.UTC)),
		WithVersion("v0.41.0"),
		WithThresholds([]loadtest.ThresholdResult{
			{Threshold: "p95<200ms", Got: "27.0ms", OK: true},
			{Threshold: "error_rate<0.1%", Got: "0.40%", OK: false},
		}),
	}
}

func TestReportGolden(t *testing.T) {
	t.Parallel()

	var b bytes.Buffer
	if err := WriteHTML(&b, "browse, list_posts", fixedSummary(), fixedSeries(), fixedOptions()...); err != nil {
		t.Fatal(err)
	}

	p := filepath.Join("testdata", "report.golden.html")

	if *update {
		if err := os.WriteFile(p, b.Bytes(), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	want, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("%v (run with -update)", err)
	}

	if diff := cmp.Diff(strings.ReplaceAll(string(want), "\r\n", "\n"), b.String()); diff != "" {
		t.Errorf("report (-want +got):\n%s", diff)
	}
}

func TestSecurity_A05_BenchReportIsSelfContained(t *testing.T) {
	t.Parallel()

	var b bytes.Buffer
	if err := WriteHTML(&b, "report", fixedSummary(), fixedSeries(), fixedOptions()...); err != nil {
		t.Fatal(err)
	}

	html := b.String()

	for _, bad := range []string{"<script", "http://", "https://", "src=", "@import", "url(", "<link", "<iframe", "onerror", "javascript:"} {
		if strings.Contains(html, bad) {
			t.Errorf("the report contains %q: it must work from disk with nothing external", bad)
		}
	}

	if !strings.Contains(html, "<svg") || !strings.Contains(html, "<style>") {
		t.Error("the report must carry its charts and its styles inline")
	}

	if strings.Contains(html, "token=hidden") || strings.Contains(html, "?token") {
		t.Error("the report shows the query string of the target")
	}
}

func TestSecurity_A03_BenchReportEscapesNames(t *testing.T) {
	t.Parallel()

	evil := "<img src=x onerror=alert(1)>"

	sum := fixedSummary()
	sum.ByName = map[string]loadtest.NameSummary{evil: {HTTPReqs: 1}}
	sum.Scenarios = []string{evil}

	var b bytes.Buffer
	if err := WriteHTML(
		&b,
		evil,
		sum,
		fixedSeries(),
		WithThresholds([]loadtest.ThresholdResult{{Threshold: evil, Got: evil}}),
	); err != nil {
		t.Fatal(err)
	}

	if strings.Contains(b.String(), "<img") || !strings.Contains(b.String(), "&lt;img") {
		t.Fatalf("a user-supplied name was not escaped:\n%s", b.String())
	}
}

func TestReportEmptySeries(t *testing.T) {
	t.Parallel()

	var b bytes.Buffer
	if err := WriteHTML(&b, "empty", loadtest.Summary{SchemaVersion: 1, Status: map[string]uint64{}}, nil); err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(b.String(), "no data") {
		t.Fatalf("an empty run must say so:\n%s", b.String())
	}
}

func TestPolyline(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		values []float64
		w, h   int
		want   string
	}{
		{"empty", nil, 100, 50, ""},
		{"one point", []float64{5}, 100, 50, "0,25.0"},
		{"rising", []float64{0, 5, 10}, 100, 50, "0,50.0 50,25.0 100,0.0"},
		{"flat zero", []float64{0, 0, 0}, 100, 50, "0,50.0 50,50.0 100,50.0"},
		{"flat value", []float64{7, 7}, 10, 10, "0,3.0 10,3.0"},
	}

	for _, tt := range tests {
		if got := polyline(tt.values, tt.w, tt.h, 10); got != tt.want {
			t.Errorf("%s: polyline = %q, want %q", tt.name, got, tt.want)
		}
	}
}
