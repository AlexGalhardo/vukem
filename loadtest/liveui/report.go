package liveui

import (
	"fmt"
	"html/template"
	"io"
	"maps"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/AlexGalhardo/vukem/loadtest"
)

// ReportOption adds to a report.
type ReportOption func(*reportOptions)

type reportOptions struct {
	generated  time.Time
	version    string
	thresholds []loadtest.ThresholdResult
}

// WithGeneratedAt sets the date shown in the header (UTC); the default is now.
func WithGeneratedAt(t time.Time) ReportOption { return func(o *reportOptions) { o.generated = t } }

// WithVersion sets the vukem version shown in the header.
func WithVersion(v string) ReportOption { return func(o *reportOptions) { o.version = v } }

// WithThresholds lists the outcome of the thresholds of the run.
func WithThresholds(rs []loadtest.ThresholdResult) ReportOption {
	return func(o *reportOptions) { o.thresholds = rs }
}

type reportCard struct{ Value, Label string }

type reportLine struct{ Class, Points string }

type reportChart struct {
	ID, Title, Caption string
	W, H               int
	Lines              []reportLine
}

type reportName struct{ Name, Reqs, Failed, P50, P95, P99 string }

type reportBar struct {
	X, Y, W, H, LX string
	Label          string
}

type reportData struct {
	Title, Generated, Version, Model, Duration, Target string
	CSS                                                template.CSS
	Cards                                              []reportCard
	Warnings                                           []string
	Charts                                             []reportChart
	Thresholds                                         []loadtest.ThresholdResult
	Names                                              []reportName
	Bars                                               []reportBar
	Statuses                                           string
}

func parseReportTemplate() (*template.Template, error) {
	return template.ParseFS(assetFS, "templates/report.html")
}

// WriteHTML writes a report that stands alone: styles and charts are inline, there is no script and nothing is
// fetched, so it works opened from disk and is safe to attach to a pull request. Every text that came from the user
// goes through html/template's escaping.
func WriteHTML(w io.Writer, title string, sum loadtest.Summary, snaps []Snapshot, opts ...ReportOption) error {
	o := reportOptions{generated: time.Now(), version: "dev"}
	for _, opt := range opts {
		opt(&o)
	}

	tpl, err := parseReportTemplate()
	if err != nil {
		return err
	}

	css, err := assetFS.ReadFile("assets/live.css")
	if err != nil {
		return err
	}

	// The page names no scheme anywhere, so it holds no URL a reader or a scanner could take for a resource.
	title = strings.NewReplacer("http://", "", "https://", "").Replace(cleanTitle(title))

	data := reportData{
		Title:     title,
		Generated: o.generated.UTC().Format("2006-01-02 15:04:05"),
		Version:   o.version,
		Model:     modelText(sum),
		Duration: fmtSeconds(
			sum.Elapsed,
		),
		Target:     displayTarget(sum.Target),
		CSS:        template.CSS(css), //nolint:gosec // G203: the embedded stylesheet
		Cards:      reportCards(sum),
		Warnings:   sum.Warnings,
		Charts:     reportCharts(snaps),
		Thresholds: o.thresholds,
		Names:      reportNames(sum),
		Bars:       reportBars(sum.Histogram),
		Statuses:   statusText(sum),
	}

	return tpl.Execute(w, data)
}

func modelText(s loadtest.Summary) string {
	switch {
	case s.Model == "open" && s.Rate != "":
		return "open model, " + s.Rate
	case s.Model == "open":
		return "open model"
	case s.Concurrency > 0:
		return "closed model, " + strconv.Itoa(s.Concurrency) + " workers"
	case s.Model != "":
		return s.Model + " model"
	}

	return "unknown model"
}

func fmtSeconds(d time.Duration) string { return strconv.FormatFloat(d.Seconds(), 'f', 1, 64) + " s" }

// displayTarget is the target without its scheme, query and fragment: the query can hold a token, and the report is
// shared.
func displayTarget(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return strings.SplitN(raw, "?", 2)[0]
	}

	return u.Host + u.Path
}

func fmtMs(d time.Duration) string {
	return strconv.FormatFloat(float64(d)/float64(time.Millisecond), 'f', 1, 64) + " ms"
}

func reportCards(s loadtest.Summary) []reportCard {
	return []reportCard{
		{strconv.FormatFloat(s.RPS, 'f', 1, 64), "requests per second"},
		{strconv.FormatUint(s.HTTPReqs, 10), "requests"},
		{
			fmtMs(s.HTTPReqDuration.P50) + " / " + fmtMs(s.HTTPReqDuration.P95) + " / " + fmtMs(s.HTTPReqDuration.P99),
			"p50 / p95 / p99",
		},
		{strconv.FormatFloat(s.HTTPReqFailed*100, 'f', 2, 64) + " %", "failed"},
		{strconv.FormatUint(s.DroppedIterations, 10), "dropped iterations"},
	}
}

func reportNames(s loadtest.Summary) []reportName {
	names := s.ByName
	if len(names) == 0 && s.HTTPReqs > 0 {
		names = map[string]loadtest.NameSummary{"all requests": {
			HTTPReqs: s.HTTPReqs, HTTPReqFailed: s.HTTPReqFailed, HTTPReqDuration: s.HTTPReqDuration,
		}}
	}

	out := make([]reportName, 0, len(names))

	for _, k := range slices.Sorted(maps.Keys(names)) {
		n := names[k]
		out = append(out, reportName{
			Name:   k,
			Reqs:   strconv.FormatUint(n.HTTPReqs, 10),
			Failed: strconv.FormatFloat(n.HTTPReqFailed*100, 'f', 2, 64) + " %",
			P50:    fmtMs(n.HTTPReqDuration.P50),
			P95:    fmtMs(n.HTTPReqDuration.P95),
			P99:    fmtMs(n.HTTPReqDuration.P99),
		})
	}

	return out
}

func statusText(s loadtest.Summary) string {
	parts := []string{}

	for _, k := range slices.Sorted(maps.Keys(s.Status)) {
		parts = append(parts, k+"="+strconv.FormatUint(s.Status[k], 10))
	}

	for _, k := range slices.Sorted(maps.Keys(s.Errors)) {
		parts = append(parts, "error "+k+"="+strconv.FormatUint(s.Errors[k], 10))
	}

	if len(parts) == 0 {
		return "none"
	}

	return strings.Join(parts, ", ")
}

const (
	chartW = 600
	chartH = 160
)

// reportCharts draws the four charts of the dashboard as SVG polylines computed here; none for an empty series.
func reportCharts(snaps []Snapshot) []reportChart {
	if len(snaps) == 0 {
		return nil
	}

	col := func(f func(Snapshot) float64) []float64 {
		v := make([]float64, len(snaps))
		for i, s := range snaps {
			v[i] = f(s)
		}

		return v
	}

	reqs := col(func(s Snapshot) float64 { return float64(s.Reqs) })
	failed := col(func(s Snapshot) float64 { return float64(s.Failed) })
	p50 := col(func(s Snapshot) float64 { return float64(s.P50Micros) / 1000 })
	p95 := col(func(s Snapshot) float64 { return float64(s.P95Micros) / 1000 })
	p99 := col(func(s Snapshot) float64 { return float64(s.P99Micros) / 1000 })
	active := col(func(s Snapshot) float64 { return float64(s.Active) })
	target := col(func(s Snapshot) float64 { return float64(s.TargetRate) })

	line := func(class string, v []float64, top float64) reportLine {
		return reportLine{Class: class, Points: polyline(v, chartW, chartH, top)}
	}

	latTop := peak(p50, p95, p99)
	loadTop := peak(active, target)

	return []reportChart{
		{
			ID: "c-throughput", Title: "Throughput, requests per second", W: chartW, H: chartH,
			Caption: fmt.Sprintf("Throughput (requests per second), peak %.0f", peak(reqs)),
			Lines:   []reportLine{line("reqs", reqs, peak(reqs))},
		},
		{
			ID: "c-latency", Title: "Latency percentiles per second, in milliseconds", W: chartW, H: chartH,
			Caption: fmt.Sprintf("Latency (ms): p50 solid, p95 dashed, p99 dotted, peak %.1f", latTop),
			Lines:   []reportLine{line("p50", p50, latTop), line("p95", p95, latTop), line("p99", p99, latTop)},
		},
		{
			ID: "c-failures", Title: "Failed requests per second", W: chartW, H: chartH,
			Caption: fmt.Sprintf("Failures (requests per second), peak %.0f", peak(failed)),
			Lines:   []reportLine{line("failed", failed, peak(failed))},
		},
		{
			ID: "c-load", Title: "Requests in flight and target load", W: chartW, H: chartH,
			Caption: fmt.Sprintf("In flight (solid) and target (dashed), peak %.0f", loadTop),
			Lines:   []reportLine{line("active", active, loadTop), line("target", target, loadTop)},
		},
	}
}

func peak(series ...[]float64) float64 {
	top := 0.0

	for _, s := range series {
		for _, v := range s {
			top = max(top, v)
		}
	}

	return top
}

// polyline returns the SVG points of values drawn in a w by h box against a scale that tops out at top: x spreads the
// values evenly, y grows upward from the bottom edge.
func polyline(values []float64, w, h int, top float64) string {
	if len(values) == 0 {
		return ""
	}

	parts := make([]string, len(values))
	steps := max(len(values)-1, 1)

	for i, v := range values {
		y := float64(h)
		if top > 0 {
			y = float64(h) - v/top*float64(h)
		}

		parts[i] = strconv.Itoa(i*w/steps) + "," + strconv.FormatFloat(y, 'f', 1, 64)
	}

	return strings.Join(parts, " ")
}

// reportBars lays the latency histogram out as SVG bars.
func reportBars(rows []loadtest.Bucket) []reportBar {
	top := uint64(0)
	for _, r := range rows {
		top = max(top, r.Count)
	}

	if top == 0 {
		return nil
	}

	slot := 592.0 / float64(len(rows))
	bars := make([]reportBar, len(rows))

	for i, r := range rows {
		h := float64(r.Count) / float64(top) * 80
		x := 4 + float64(i)*slot

		bars[i] = reportBar{
			X: f1(x + 2), Y: f1(90 - h), W: f1(max(slot-4, 1)), H: f1(h), LX: f1(x + slot/2), Label: fmtMs(r.To),
		}
	}

	return bars
}

func f1(v float64) string { return strconv.FormatFloat(v, 'f', 1, 64) }
