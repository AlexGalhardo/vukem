package loadtest

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// Stage ramps linearly from the previous target to Target over Duration. The first stage starts from 0.
type Stage struct {
	Duration time.Duration
	// Target is arrivals per second in the open model, virtual users in the closed model.
	Target int
}

// ParseStages parses "30s:50,1m:200,30s:0": a duration and a target per stage.
func ParseStages(s string) ([]Stage, error) {
	var stages []Stage

	for part := range strings.SplitSeq(s, ",") {
		dur, target, ok := strings.Cut(strings.TrimSpace(part), ":")
		if !ok {
			return nil, fmt.Errorf("loadtest: invalid stage %q: want duration:target, such as 30s:50", part)
		}

		d, err := time.ParseDuration(dur)
		if err != nil || d <= 0 {
			return nil, fmt.Errorf("loadtest: invalid stage %q: the duration must be positive, such as 30s", part)
		}

		n, err := strconv.Atoi(target)
		if err != nil || n < 0 || n > maxRate {
			return nil, fmt.Errorf(
				"loadtest: invalid stage %q: the target must be a number between 0 and %d",
				part,
				maxRate,
			)
		}

		stages = append(stages, Stage{Duration: d, Target: n})
	}

	if total := stagesDuration(stages); total > maxDuration {
		return nil, fmt.Errorf("loadtest: the stages last %s, at most %s", total, maxDuration)
	}

	return stages, nil
}

func stagesDuration(stages []Stage) time.Duration {
	var total time.Duration
	for _, s := range stages {
		total += s.Duration
	}

	return total
}

// checkStages applies the caps of the model to every target.
func checkStages(stages []Stage, open bool) error {
	if len(stages) == 0 {
		return errors.New("loadtest: no stages")
	}

	limit, what := maxConcurrency, "virtual users"
	if open {
		limit, what = maxRate, "arrivals per second"
	}

	for _, s := range stages {
		if s.Duration <= 0 || s.Target < 0 || s.Target > limit {
			return fmt.Errorf("loadtest: a stage target must be between 0 and %d %s", limit, what)
		}
	}

	if total := stagesDuration(stages); total > maxDuration {
		return fmt.Errorf("loadtest: the stages last %s, at most %s", total, maxDuration)
	}

	return nil
}

// targetAt returns the interpolated target at elapsed time: linear inside a stage, from the previous target (0 at
// the start), and the last target after the end.
func targetAt(stages []Stage, elapsed time.Duration) float64 {
	prev, start := 0.0, time.Duration(0)

	for _, s := range stages {
		if elapsed < start+s.Duration {
			return prev + (float64(s.Target)-prev)*float64(elapsed-start)/float64(s.Duration)
		}

		prev, start = float64(s.Target), start+s.Duration
	}

	return prev
}

// rateFor turns a target of arrivals per second into a Rate. A thousandth of a request per second is the resolution,
// and anything below it pauses the arrivals.
func rateFor(perSecond float64) Rate {
	if perSecond < 0.001 {
		return Rate{Freq: 0, Per: time.Second}
	}

	return Rate{Freq: int(perSecond*1000 + 0.5), Per: 1000 * time.Second}
}

// rampRate follows the stages in the open model: every 100ms it sets the arrival rate to the interpolated target.
func (r *Runner) rampRate() {
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()

	for {
		r.setRate(rateFor(targetAt(r.stages, r.now().Sub(r.begin))))

		select {
		case <-r.genCtx.Done():
			return
		case <-tick.C:
		}
	}
}

// startClosedStages follows the stages in the closed model: every 100ms it starts virtual users, or asks the most
// recent ones to leave after their current iteration, until their number is the interpolated target.
func (r *Runner) startClosedStages() {
	var next atomic.Uint64

	r.wg.Go(func() {
		tick := time.NewTicker(100 * time.Millisecond)
		defer tick.Stop()

		var stops []chan struct{}

		started := 0

		for {
			want := int(targetAt(r.stages, r.now().Sub(r.begin)) + 0.5)

			for len(stops) < want {
				stop, id := make(chan struct{}), started
				stops, started = append(stops, stop), started+1

				r.wg.Go(func() { r.userLoop(id, &next, stop) })
			}

			for len(stops) > want {
				close(stops[len(stops)-1])
				stops = stops[:len(stops)-1]
			}

			select {
			case <-r.genCtx.Done():
				return
			case <-tick.C:
			}
		}
	})
}
