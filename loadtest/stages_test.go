package loadtest

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
)

func TestParseStages(t *testing.T) {
	t.Parallel()

	got, err := ParseStages("30s:50,1m:200, 30s:0")
	if err != nil {
		t.Fatal(err)
	}

	want := []Stage{{30 * time.Second, 50}, {time.Minute, 200}, {30 * time.Second, 0}}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Fatalf("(-want +got):\n%s", diff)
	}

	for _, bad := range []string{"", "30s", "x:10", "30s:-1", "30s:x", "0s:10", "-5s:10", "30s:10,", "61m:1", "30m:1,31m:1", "1s:100001"} {
		if _, err := ParseStages(bad); err == nil {
			t.Errorf("ParseStages(%q) was accepted", bad)
		}
	}
}

func TestStagesInterpolate(t *testing.T) {
	t.Parallel()

	stages := []Stage{{10 * time.Second, 100}, {10 * time.Second, 100}, {10 * time.Second, 0}}

	tests := []struct {
		at   time.Duration
		want float64
	}{
		{0, 0},
		{5 * time.Second, 50},
		{10 * time.Second, 100},
		{15 * time.Second, 100},
		{25 * time.Second, 50},
		{30 * time.Second, 0},
		{time.Hour, 0},
	}

	for _, tt := range tests {
		if got := targetAt(stages, tt.at); got != tt.want {
			t.Errorf("targetAt(%v) = %v, want %v", tt.at, got, tt.want)
		}
	}

	if got := stagesDuration(stages); got != 30*time.Second {
		t.Errorf("stagesDuration = %v", got)
	}
}

func TestStagesModelCaps(t *testing.T) {
	t.Parallel()

	if err := checkStages([]Stage{{time.Second, maxConcurrency + 1}}, false); err == nil {
		t.Error("a closed-model stage over the virtual user cap was accepted")
	}

	if err := checkStages([]Stage{{time.Second, maxConcurrency}}, false); err != nil {
		t.Errorf("a closed-model stage at the cap was refused: %v", err)
	}

	if err := checkStages([]Stage{{time.Second, maxRate + 1}}, true); err == nil {
		t.Error("an open-model stage over the rate cap was accepted")
	}
}

func TestStagesRefuseRequestCount(t *testing.T) {
	t.Parallel()

	srv := okServer(t, nil)

	_, err := StartScenarios(t.Context(), ScenarioConfig{
		Requests: 10, BaseURL: srv.URL, Stages: []Stage{{time.Second, 1}},
	}, []Scenario{{Name: "a", Do: func(context.Context, *Client) error { return nil }}})
	if err == nil {
		t.Fatal("stages with a request count were accepted")
	}
}

func TestClosedModelRampsUsers(t *testing.T) {
	if testing.Short() {
		t.Skip("takes two seconds of wall time")
	}

	var (
		inflight atomic.Int64
		mu       sync.Mutex
		peaks    = map[int]int64{} // 250ms window -> highest concurrency seen
	)

	begin := time.Now()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n := inflight.Add(1)
		defer inflight.Add(-1)

		mu.Lock()
		w2 := int(time.Since(begin) / (250 * time.Millisecond))
		peaks[w2] = max(peaks[w2], n)
		mu.Unlock()

		time.Sleep(40 * time.Millisecond)
		_, _ = w.Write([]byte("ok"))
	}))
	t.Cleanup(srv.Close)

	sum, err := RunScenarios(t.Context(), ScenarioConfig{
		BaseURL: srv.URL, Stages: []Stage{{500 * time.Millisecond, 8}, {time.Second, 8}, {500 * time.Millisecond, 0}},
	}, []Scenario{{Name: "get", Do: func(ctx context.Context, c *Client) error {
		_, err := c.Get(ctx, "/")

		return err
	}}})
	if err != nil {
		t.Fatal(err)
	}

	if sum.Elapsed < 1800*time.Millisecond || sum.HTTPReqs == 0 {
		t.Fatalf("elapsed=%v reqs=%d, want about the 2s of stages", sum.Elapsed, sum.HTTPReqs)
	}

	mu.Lock()
	defer mu.Unlock()

	// Windows 0-1 are the ramp up, 2-5 the plateau, 6-7 the ramp down.
	early, plateau := max(peaks[0], peaks[1]), max(max(peaks[3], peaks[4]), peaks[5])
	if plateau > 8 || plateau < 5 || early >= plateau {
		t.Fatalf("peaks per window = %v: want the ramp below a plateau of about 8 users", peaks)
	}
}

func TestOpenModelRampsRate(t *testing.T) {
	if testing.Short() {
		t.Skip("takes over a second of wall time")
	}

	srv := okServer(t, nil)

	r, err := StartScenarios(t.Context(), ScenarioConfig{
		BaseURL: srv.URL, Open: true, Stages: []Stage{{300 * time.Millisecond, 0}, {time.Second, 200}},
	}, []Scenario{{Name: "get", Do: func(ctx context.Context, c *Client) error {
		_, err := c.Get(ctx, "/")

		return err
	}}})
	if err != nil {
		t.Fatal(err)
	}

	begin := time.Now()

	var early, late int

	for _, res := range collect(t, r) {
		if res.Scheduled.Sub(begin) < 250*time.Millisecond {
			early++
		} else {
			late++
		}
	}

	// A ramp from 0 to 200/s over a second sends about 100.
	if early != 0 || late < 30 || late > 170 {
		t.Fatalf("arrivals before the ramp=%d during it=%d, want 0 and about 100", early, late)
	}
}
