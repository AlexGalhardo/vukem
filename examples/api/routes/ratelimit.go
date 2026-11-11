package routes

import (
	"fmt"
	"strconv"
	"time"

	"github.com/AlexGalhardo/vukem"
	"github.com/AlexGalhardo/vukem/ratelimit"
)

// maxRateScale bounds RATE_LIMIT_SCALE so a typo cannot switch the limits off in practice.
const maxRateScale = 10000

// rateLimiters is app.RateLimiters with every limit multiplied by RATE_LIMIT_SCALE (default 1). A load test comes from
// one address and one user, so it needs more room than the limits meant for real clients (vukem bench:run).
func rateLimiters(app *vukem.App) (ratelimit.Factory, error) {
	base, err := app.RateLimiters()
	if err != nil {
		return nil, err
	}

	raw := app.Source.Get("RATE_LIMIT_SCALE")
	if raw == "" {
		return base, nil
	}

	scale, err := strconv.Atoi(raw)
	if err != nil || scale < 1 || scale > maxRateScale {
		return nil, fmt.Errorf("RATE_LIMIT_SCALE %q: want an integer from 1 to %d", raw, maxRateScale)
	}

	return func(name string, limit int, window time.Duration) ratelimit.Limiter {
		return base(name, limit*scale, window)
	}, nil
}
