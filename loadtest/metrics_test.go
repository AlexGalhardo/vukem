package loadtest

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
)

var update = flag.Bool("update", false, "rewrite golden files")

func within(got, want time.Duration, rel float64) bool {
	return math.Abs(float64(got-want)) <= rel*float64(want)
}

func TestMetricsSummary(t *testing.T) {
	t.Parallel()

	var m Metrics

	for i, d := range []time.Duration{time.Millisecond, 2 * time.Millisecond, 3 * time.Millisecond, 4 * time.Millisecond} {
		m.Add(Result{
			Seq: uint64(
				i,
			),
			Name:     "GET /",
			Status:   200,
			Duration: d,
			Service:  d,
			Waiting:  d / 2,
			BytesIn:  100,
			BytesOut: 10,
		})
	}

	m.Add(Result{Name: "GET /boom", Status: 500, Duration: 5 * time.Millisecond, BytesIn: 20})
	m.Add(Result{Name: "GET /", Err: "timeout", Duration: 30 * time.Second})
	m.Add(Result{Name: "GET /", Status: 302, Duration: time.Millisecond})

	s := m.Summary(2*time.Second, 3)

	if s.SchemaVersion != 1 || s.HTTPReqs != 7 || s.DroppedIterations != 3 || s.Elapsed != 2*time.Second {
		t.Fatalf("summary header = %+v", s)
	}

	if s.RPS != 3.5 {
		t.Errorf("RPS = %v, want 3.5", s.RPS)
	}

	if want := 2.0 / 7; math.Abs(s.HTTPReqFailed-want) > 1e-9 {
		t.Errorf("HTTPReqFailed = %v, want %v", s.HTTPReqFailed, want)
	}

	if s.DataReceived != 420 || s.DataSent != 40 {
		t.Errorf("bytes = %d in, %d out, want 420 and 40", s.DataReceived, s.DataSent)
	}

	if diff := cmp.Diff(map[string]uint64{"200": 4, "302": 1, "500": 1, "error": 1}, s.Status); diff != "" {
		t.Errorf("status (-want +got):\n%s", diff)
	}

	if diff := cmp.Diff(map[string]uint64{"timeout": 1}, s.Errors); diff != "" {
		t.Errorf("errors (-want +got):\n%s", diff)
	}

	if s.HTTPReqDuration.Min != time.Millisecond || s.HTTPReqDuration.Max != 30*time.Second {
		t.Errorf("duration trend = %+v", s.HTTPReqDuration)
	}

	if !within(s.HTTPReqDuration.P50, 3*time.Millisecond, 0.032) {
		t.Errorf("p50 = %v, want about 3ms", s.HTTPReqDuration.P50)
	}

	// Waiting counts only the requests that got a response byte; the three without one are not zeros in it.
	if s.HTTPReqWaiting.Max != 2*time.Millisecond {
		t.Errorf("waiting max = %v, want 2ms", s.HTTPReqWaiting.Max)
	}

	if got := s.ByName["GET /"]; got.HTTPReqs != 6 || math.Abs(got.HTTPReqFailed-1.0/6) > 1e-9 {
		t.Errorf("by name GET / = %+v", got)
	}

	if len(s.ByName) != 2 || len(s.Histogram) == 0 {
		t.Errorf("by_name=%d histogram=%d rows", len(s.ByName), len(s.Histogram))
	}
}

func TestMetricsEmptyAndSingleNameOmitted(t *testing.T) {
	t.Parallel()

	var m Metrics

	s := m.Summary(time.Second, 0)
	if s.HTTPReqs != 0 || s.RPS != 0 || s.HTTPReqFailed != 0 || s.Status == nil {
		t.Fatalf("empty summary = %+v", s)
	}

	m.Add(Result{Name: "GET /", Status: 200, Duration: time.Millisecond})

	if s := m.Summary(time.Second, 0); s.ByName != nil {
		t.Fatalf("a single name repeats the totals, by_name = %+v", s.ByName)
	}
}

func TestMetricsCapsDistinctNames(t *testing.T) {
	t.Parallel()

	var m Metrics

	for i := range 3 * maxNames {
		m.Add(Result{Name: "GET /" + strings.Repeat("x", i+1), Status: 200, Duration: time.Millisecond})
	}

	if s := m.Summary(time.Second, 0); len(s.ByName) != maxNames+1 || s.ByName[otherName].HTTPReqs != 2*maxNames {
		t.Fatalf("by_name has %d entries, other = %+v", len(s.ByName), s.ByName[otherName])
	}
}

func fixedSummary() Summary {
	tr := func(base time.Duration) Trend {
		return Trend{
			Min: base / 10, Mean: base, P50: base * 9 / 10, P90: base * 16 / 10, P95: base * 19 / 10,
			P99: base * 30 / 10, Max: base * 70 / 10,
		}
	}

	return Summary{
		SchemaVersion: 1, Target: "http://127.0.0.1:3000/", Method: "GET", Model: "closed", Concurrency: 10,
		Elapsed: 10 * time.Second, HTTPReqs: 6182, Arrivals: 6182, RPS: 618.2, HTTPReqFailed: 0.0005,
		HTTPReqDuration: tr(16 * time.Millisecond), HTTPReqWaiting: tr(15 * time.Millisecond),
		HTTPReqConnecting: Trend{P99: 400 * time.Microsecond, Max: 1900 * time.Microsecond},
		DataSent:          0, DataReceived: 4_100_000, DroppedIterations: 0,
		Status: map[string]uint64{"200": 6179, "500": 3},
		Errors: map[string]uint64{"timeout": 2},
		Histogram: []Bucket{
			{From: 1200 * time.Microsecond, To: 8 * time.Millisecond, Count: 812},
			{From: 8 * time.Millisecond, To: 16 * time.Millisecond, Count: 2904},
			{From: 16 * time.Millisecond, To: 32 * time.Millisecond, Count: 2211},
			{From: 32 * time.Millisecond, To: 112400 * time.Microsecond, Count: 255},
		},
	}
}

func golden(t *testing.T, name string, got []byte) {
	t.Helper()

	p := filepath.Join("testdata", name)

	if *update {
		if err := os.WriteFile(p, got, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	want, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("%v (run with -update)", err)
	}

	if diff := cmp.Diff(strings.ReplaceAll(string(want), "\r\n", "\n"), string(got)); diff != "" {
		t.Errorf("%s (-want +got):\n%s", name, diff)
	}
}

func TestWriteTextGolden(t *testing.T) {
	t.Parallel()

	var b bytes.Buffer
	if err := WriteText(&b, fixedSummary()); err != nil {
		t.Fatal(err)
	}

	golden(t, "report.golden.txt", b.Bytes())
}

func TestWriteJSONGolden(t *testing.T) {
	t.Parallel()

	var b bytes.Buffer
	if err := WriteJSON(&b, fixedSummary()); err != nil {
		t.Fatal(err)
	}

	golden(t, "report.golden.json", b.Bytes())
}

func TestSummaryJSONRoundTrip(t *testing.T) {
	t.Parallel()

	s := fixedSummary()
	s.Interrupted = true
	s.ByName = map[string]NameSummary{
		"GET /": {HTTPReqs: 5, HTTPReqFailed: 0.2, HTTPReqDuration: Trend{P50: time.Millisecond}},
	}

	var b bytes.Buffer
	if err := WriteJSON(&b, s); err != nil {
		t.Fatal(err)
	}

	var got Summary
	if err := json.Unmarshal(b.Bytes(), &got); err != nil {
		t.Fatal(err)
	}

	if diff := cmp.Diff(s, got); diff != "" {
		t.Fatalf("round trip (-want +got):\n%s", diff)
	}
}

func TestWriteTextInterruptedNote(t *testing.T) {
	t.Parallel()

	s := fixedSummary()

	var plain, stopped, asJSON bytes.Buffer

	if err := WriteText(&plain, s); err != nil {
		t.Fatal(err)
	}

	if strings.Contains(plain.String(), "interrupted") {
		t.Fatal("a finished run must not say interrupted")
	}

	s.Interrupted = true

	if err := WriteText(&stopped, s); err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(stopped.String(), "  note       interrupted (stopped before the planned end)\n") {
		t.Fatalf("missing note:\n%s", stopped.String())
	}

	s.Interrupted = false

	if err := WriteJSON(&asJSON, s); err != nil {
		t.Fatal(err)
	}

	if strings.Contains(asJSON.String(), "interrupted") {
		t.Fatal("interrupted must be omitted when false")
	}
}

func TestRunInterrupted(t *testing.T) {
	if testing.Short() {
		t.Skip("takes a few hundred milliseconds of wall time")
	}

	srv := okServer(t, nil)
	ctx, cancel := context.WithCancel(t.Context())

	time.AfterFunc(200*time.Millisecond, cancel)

	s, err := Run(ctx, Config{Concurrency: 2, Duration: 5 * time.Second}, get(srv))
	if err != nil {
		t.Fatal(err)
	}

	if !s.Interrupted || s.Elapsed >= 3*time.Second || s.HTTPReqs == 0 || s.HTTPReqFailed != 0 {
		t.Fatalf("interrupted=%v elapsed=%v reqs=%d failed=%v", s.Interrupted, s.Elapsed, s.HTTPReqs, s.HTTPReqFailed)
	}
}

func TestRunFillsRunFields(t *testing.T) {
	t.Parallel()

	srv := okServer(t, nil)

	s, err := Run(
		t.Context(),
		Config{Concurrency: 3, Requests: 30},
		Static(Request{Method: http.MethodPost, URL: srv.URL + "/a", Body: []byte("hi")}),
	)
	if err != nil {
		t.Fatal(err)
	}

	if s.Model != "closed" || s.Concurrency != 3 || s.Method != http.MethodPost || s.Target != srv.URL+"/a" ||
		s.HTTPReqs != 30 ||
		s.Interrupted ||
		s.DataSent != 60 {
		t.Fatalf("summary = %+v", s)
	}

	open, err := Run(t.Context(), Config{Rate: Rate{100, time.Second}, Requests: 5}, get(srv))
	if err != nil {
		t.Fatal(err)
	}

	if open.Model != "open" || open.Rate != "100/s" || open.MaxWorkers != 1000 || open.Concurrency != 0 {
		t.Fatalf("open summary = %+v", open)
	}
}

func TestSecurity_A09_BenchReportHasNoRawErrors(t *testing.T) {
	t.Parallel()

	gone := okServer(t, nil)
	url := gone.URL + "/secret-token-path"
	gone.Close()

	s, err := Run(t.Context(), Config{Concurrency: 1, Requests: 3, Timeout: time.Second}, Static(Request{URL: url}))
	if err != nil {
		t.Fatal(err)
	}

	if s.Errors["connection refused"] != 3 {
		t.Fatalf("errors = %v, want 3 refused", s.Errors)
	}

	var text, js bytes.Buffer
	if err := WriteText(&text, s); err != nil {
		t.Fatal(err)
	}

	if err := WriteJSON(&js, s); err != nil {
		t.Fatal(err)
	}

	for _, out := range []string{text.String(), js.String()} {
		for _, raw := range []string{"dial tcp", "connectex", "refused by", "Get \"", "connect:"} {
			if strings.Contains(out, raw) {
				t.Errorf("the report holds raw transport text %q:\n%s", raw, out)
			}
		}
	}
}
