package loadtest

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// Rate is an arrival rate: Freq requests every Per.
type Rate struct {
	Freq int
	Per  time.Duration
}

// String formats the rate as "200/s" or "300/1m0s".
func (r Rate) String() string {
	if r.Per == time.Second {
		return fmt.Sprintf("%d/s", r.Freq)
	}

	return fmt.Sprintf("%d/%s", r.Freq, r.Per)
}

// perSecond returns the arrival rate in requests per second.
func (r Rate) perSecond() float64 { return float64(r.Freq) / r.Per.Seconds() }

// ParseRate parses "50/s", "300/1m" or "50" (per second).
func ParseRate(s string) (Rate, error) {
	freq, per, found := strings.Cut(strings.TrimSpace(s), "/")

	n, err := strconv.Atoi(freq)
	if err != nil || n < 1 {
		return Rate{}, fmt.Errorf("loadtest: invalid rate %q: want N, N/s or N/1m", s)
	}

	if !found {
		return Rate{n, time.Second}, nil
	}

	// A bare unit such as "s" or "m" means one of it.
	if per != "" && unicode.IsLetter(rune(per[0])) {
		per = "1" + per
	}

	d, err := time.ParseDuration(per)
	if err != nil || d <= 0 {
		return Rate{}, errors.New(
			"loadtest: invalid rate " + strconv.Quote(s) + ": the period must be a positive duration",
		)
	}

	return Rate{n, d}, nil
}
