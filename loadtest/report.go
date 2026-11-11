package loadtest

import (
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"
)

// WriteJSON writes s as indented JSON, durations in integer nanoseconds.
func WriteJSON(w io.Writer, s Summary) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")

	return enc.Encode(s)
}

// WriteText writes the human report of s.
func WriteText(w io.Writer, s Summary) error {
	var b strings.Builder

	fmt.Fprintf(&b, "vukem bench  %s %s\n", s.Method, s.Target)
	fmt.Fprintf(&b, "  model      %s\n", modelLine(s))
	fmt.Fprintf(&b, "  duration   %.2fs\n", s.Elapsed.Seconds())

	if s.Interrupted {
		b.WriteString("  note       interrupted (stopped before the planned end)\n")
	}

	b.WriteString("\n")
	fmt.Fprintf(&b, "  %s %s    %.1f/s\n", dots("http_reqs"), thousands(s.HTTPReqs), s.RPS)
	fmt.Fprintf(
		&b,
		"  %s %.2f%%    %s of %s%s\n",
		dots("http_req_failed"),
		s.HTTPReqFailed*100,
		thousands(uint64(s.HTTPReqFailed*float64(s.HTTPReqs)+0.5)),
		thousands(s.HTTPReqs),
		droppedNote(s.DroppedIterations),
	)

	for _, row := range []struct {
		name string
		t    Trend
	}{
		{"http_req_duration", s.HTTPReqDuration},
		{"http_req_waiting", s.HTTPReqWaiting},
		{"http_req_connecting", s.HTTPReqConnecting},
	} {
		fmt.Fprintf(&b, "  %s %s\n", dots(row.name), trendLine(row.t))
	}

	if s.HTTPReqTLS.Max > 0 {
		fmt.Fprintf(&b, "  %s %s\n", dots("http_req_tls_handshaking"), trendLine(s.HTTPReqTLS))
	}

	fmt.Fprintf(
		&b,
		"  %s %s   %s/s\n",
		dots("data_received"),
		bytesText(s.DataReceived),
		bytesText(perSecond(s.DataReceived, s.Elapsed)),
	)
	fmt.Fprintf(
		&b,
		"  %s %s   %s/s\n",
		dots("data_sent"),
		bytesText(s.DataSent),
		bytesText(perSecond(s.DataSent, s.Elapsed)),
	)
	fmt.Fprintf(&b, "  %s %d\n", dots("dropped_iterations"), s.DroppedIterations)

	for _, w := range s.Warnings {
		b.WriteString("  warning    " + w + "\n")
	}

	b.WriteString("\n  status  " + countsLine(s.Status) + "\n")

	if len(s.Errors) > 0 {
		b.WriteString("  errors  " + countsLine(s.Errors) + "\n")
	}

	histogramText(&b, s.Histogram)

	_, err := io.WriteString(w, b.String())

	return err
}

// droppedNote shows the drops next to the failure rate: a dropped arrival is in no figure above it.
func droppedNote(dropped uint64) string {
	if dropped == 0 {
		return ""
	}

	return "  + " + thousands(dropped) + " dropped"
}

func modelLine(s Summary) string {
	if s.Model == "open" {
		return fmt.Sprintf("open, %s (max %d workers)", s.Rate, s.MaxWorkers)
	}

	return fmt.Sprintf("closed, %d workers", s.Concurrency)
}

// dots pads a metric name with dots to a common column.
func dots(name string) string {
	return name + " " + strings.Repeat(".", max(1, 25-len(name)))
}

func trendLine(t Trend) string {
	return fmt.Sprintf("min=%s mean=%s p50=%s p90=%s p95=%s p99=%s max=%s",
		fmtDur(t.Min), fmtDur(t.Mean), fmtDur(t.P50), fmtDur(t.P90), fmtDur(t.P95), fmtDur(t.P99), fmtDur(t.Max))
}

func countsLine(m map[string]uint64) string {
	parts := make([]string, 0, len(m))
	for _, k := range slices.Sorted(maps.Keys(m)) {
		parts = append(parts, k+"="+strconv.FormatUint(m[k], 10))
	}

	return strings.Join(parts, " ")
}

func histogramText(b *strings.Builder, rows []Bucket) {
	if len(rows) == 0 {
		return
	}

	var peak uint64
	for _, r := range rows {
		peak = max(peak, r.Count)
	}

	b.WriteString("\n  latency histogram\n")

	for _, r := range rows {
		bar := 0
		if r.Count > 0 {
			bar = max(1, int(r.Count*30/peak)) //nolint:gosec // G115: at most 30
		}

		fmt.Fprintf(
			b,
			"    %8s - %8s  %-30s %7s\n",
			fmtDur(r.From),
			fmtDur(r.To),
			strings.Repeat("#", bar),
			thousands(r.Count),
		)
	}
}

// fmtDur formats a duration with one decimal in a fitting unit.
func fmtDur(d time.Duration) string {
	switch {
	case d <= 0:
		return "0s"
	case d < time.Microsecond:
		return strconv.FormatInt(int64(d), 10) + "ns"
	case d < time.Millisecond:
		return fmt.Sprintf("%.1fµs", float64(d)/float64(time.Microsecond))
	case d < time.Second:
		return fmt.Sprintf("%.1fms", float64(d)/float64(time.Millisecond))
	}

	return fmt.Sprintf("%.2fs", d.Seconds())
}

func bytesText(n int64) string {
	f := float64(n)

	for _, unit := range []string{"B", "kB", "MB", "GB"} {
		if f < 1000 || unit == "GB" {
			if unit == "B" {
				return fmt.Sprintf("%d B", n)
			}

			return fmt.Sprintf("%.1f %s", f, unit)
		}

		f /= 1000
	}

	return ""
}

func perSecond(n int64, d time.Duration) int64 {
	if d <= 0 {
		return 0
	}

	return int64(float64(n) / d.Seconds())
}

// thousands groups digits with a space: 6182 becomes "6 182".
func thousands(n uint64) string {
	s := strconv.FormatUint(n, 10)

	var b strings.Builder

	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(' ')
		}

		b.WriteRune(c)
	}

	return b.String()
}
