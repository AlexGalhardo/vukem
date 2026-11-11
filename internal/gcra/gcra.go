// Package gcra is the generic cell rate algorithm shared by the in-process
// limiters (ratelimit.Memory and the httpclient throttle). The Redis limiter
// runs the same arithmetic as a Lua script on the server and mirrors it.
package gcra

import "time"

// Next returns the theoretical arrival time after admitting one request at
// now and the earliest instant that request is allowed: a caller that
// rejects compares now with allowAt and stores next only when admitted; a
// caller that waits stores next and sleeps until allowAt. A tat in the past
// counts as now.
func Next(tat, now time.Time, interval, tolerance time.Duration) (next, allowAt time.Time) {
	if tat.Before(now) {
		tat = now
	}

	next = tat.Add(interval)

	return next, next.Add(-tolerance)
}
