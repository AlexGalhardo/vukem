package schedule

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Cron is a parsed five-field cron expression (minute hour day-of-month
// month day-of-week) with the usual syntax: "*", "5", "1-5", "*/15",
// "10-50/10", lists ("1,15"), month and weekday names ("JAN", "MON"), and
// the macros @yearly, @monthly, @weekly, @daily, @hourly. As in Vixie
// cron, when both day fields are restricted a day matches either.
type Cron struct {
	expr                          string
	minute, hour, dom, month, dow uint64
	domStar, dowStar              bool
}

var macros = map[string]string{ //nolint:gochecknoglobals // read-only table
	"@yearly": "0 0 1 1 *", "@annually": "0 0 1 1 *", "@monthly": "0 0 1 * *",
	"@weekly": "0 0 * * 0", "@daily": "0 0 * * *", "@midnight": "0 0 * * *", "@hourly": "0 * * * *",
}

var names = map[string]int{ //nolint:gochecknoglobals // read-only table
	"JAN": 1, "FEB": 2, "MAR": 3, "APR": 4, "MAY": 5, "JUN": 6, "JUL": 7, "AUG": 8, "SEP": 9, "OCT": 10,
	"NOV": 11, "DEC": 12, "SUN": 0, "MON": 1, "TUE": 2, "WED": 3, "THU": 4, "FRI": 5, "SAT": 6,
}

// ErrCron reports an invalid expression.
var ErrCron = errors.New("schedule: invalid cron expression")

// ParseCron parses expr.
func ParseCron(expr string) (Cron, error) {
	src := strings.TrimSpace(expr)
	if m, ok := macros[strings.ToLower(src)]; ok {
		src = m
	}

	fields := strings.Fields(src)
	if len(fields) != 5 {
		return Cron{}, fmt.Errorf("%w %q: want 5 fields", ErrCron, expr)
	}

	c := Cron{expr: expr}
	bounds := [5][2]int{{0, 59}, {0, 23}, {1, 31}, {1, 12}, {0, 7}}
	sets := [5]*uint64{&c.minute, &c.hour, &c.dom, &c.month, &c.dow}

	for i, f := range fields {
		set, err := parseField(f, bounds[i][0], bounds[i][1])
		if err != nil {
			return Cron{}, fmt.Errorf("%w %q: field %d: %w", ErrCron, expr, i+1, err)
		}

		*sets[i] = set
	}

	if c.dow&(1<<7) != 0 { // 7 is Sunday too
		c.dow |= 1
	}

	c.domStar, c.dowStar = fields[2] == "*", fields[4] == "*"

	return c, nil
}

// MustParseCron is ParseCron that panics (for literals).
func MustParseCron(expr string) Cron {
	c, err := ParseCron(expr)
	if err != nil {
		panic(err)
	}

	return c
}

func parseField(f string, lo, hi int) (uint64, error) {
	var set uint64

	for part := range strings.SplitSeq(f, ",") {
		rng, stepStr, hasStep := strings.Cut(part, "/")
		step := 1

		if hasStep {
			n, err := strconv.Atoi(stepStr)
			if err != nil || n < 1 || n > hi {
				return 0, fmt.Errorf("bad step %q", stepStr)
			}

			step = n
		}

		from, to := lo, hi

		switch {
		case rng == "*":
		case strings.Contains(rng, "-"):
			a, b, _ := strings.Cut(rng, "-")

			var err error
			if from, err = value(a, lo, hi); err != nil {
				return 0, err
			}

			if to, err = value(b, lo, hi); err != nil {
				return 0, err
			}

			if from > to {
				return 0, fmt.Errorf("bad range %q", rng)
			}
		default:
			v, err := value(rng, lo, hi)
			if err != nil {
				return 0, err
			}

			from = v
			if !hasStep {
				to = v
			}
		}

		for v := from; v <= to; v += step {
			set |= 1 << uint(v)
		}
	}

	return set, nil
}

func value(s string, lo, hi int) (int, error) {
	v, ok := names[strings.ToUpper(s)]
	if !ok {
		n, err := strconv.Atoi(s)
		if err != nil {
			return 0, fmt.Errorf("bad value %q", s)
		}

		v = n
	}

	if v < lo || v > hi {
		return 0, fmt.Errorf("%d out of range %d-%d", v, lo, hi)
	}

	return v, nil
}

// String returns the expression as given.
func (c Cron) String() string { return c.expr }

// Matches reports whether t (to the minute, in t's location) is a run time.
func (c Cron) Matches(t time.Time) bool {
	return c.minute&(1<<uint(t.Minute())) != 0 && c.hour&(1<<uint(t.Hour())) != 0 &&
		c.month&(1<<uint(t.Month())) != 0 && c.dayMatches(t)
}

func (c Cron) dayMatches(t time.Time) bool {
	dom := c.dom&(1<<uint(t.Day())) != 0
	dow := c.dow&(1<<uint(t.Weekday())) != 0

	if c.domStar || c.dowStar {
		return dom && dow
	}

	return dom || dow
}

// Next returns the first run time strictly after t (in t's location), or
// the zero time if there is none within five years.
func (c Cron) Next(t time.Time) time.Time {
	t = t.Truncate(time.Minute).Add(time.Minute)
	limit := t.AddDate(5, 0, 0)

	for t.Before(limit) {
		switch {
		case c.month&(1<<uint(t.Month())) == 0:
			t = time.Date(t.Year(), t.Month()+1, 1, 0, 0, 0, 0, t.Location())
		case !c.dayMatches(t):
			t = time.Date(t.Year(), t.Month(), t.Day()+1, 0, 0, 0, 0, t.Location())
		case c.hour&(1<<uint(t.Hour())) == 0:
			// time.Date, not Truncate: zones such as +05:30 are not hour-aligned.
			t = time.Date(t.Year(), t.Month(), t.Day(), t.Hour()+1, 0, 0, 0, t.Location())
		case c.minute&(1<<uint(t.Minute())) == 0:
			t = t.Add(time.Minute)
		default:
			return t
		}
	}

	return time.Time{}
}
