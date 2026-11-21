package accounts

import (
	"context"
	"fmt"
	"time"

	"github.com/AlexGalhardo/vukem/examples/saas/app/mail"
	"github.com/AlexGalhardo/vukem/examples/saas/app/models"
	"github.com/AlexGalhardo/vukem/ratelimit"
)

// The per-account limit on failed login codes: whatever IPs they come from,
// a user whose password someone knows gets TwoFactorAccountFailures wrong
// codes per TwoFactorAccountWindow in total (VF-097). The per-IP limit of the
// route stays as the first line.
const (
	TwoFactorAccountFailures = 10
	TwoFactorAccountWindow   = 15 * time.Minute
)

func twoFactorAccountKey(u models.User) string { return "2fa-account:" + u.AuthID() }

// NewTwoFactorAccountLimiter returns the limiter for the per-account budget.
func NewTwoFactorAccountLimiter() ratelimit.Limiter {
	return ratelimit.NewMemory(TwoFactorAccountFailures, TwoFactorAccountWindow)
}

// TwoFactorLocked reports whether u has used up the failed-code budget. It
// spends nothing, so a request that fails earlier (no pending challenge)
// costs nothing, and it is checked before the code is tried: otherwise the
// next guess would still be free. A limiter that cannot answer locks.
func TwoFactorLocked(ctx context.Context, l ratelimit.Limiter, u models.User) bool {
	p, ok := l.(ratelimit.Peeker)
	if !ok {
		return false
	}

	res, err := p.Peek(ctx, twoFactorAccountKey(u))

	return err != nil || !res.Allowed
}

// RecordTwoFactorFailure counts one failed code and reports whether the
// budget is now used up. The failure that uses it up emails the user once.
func (a *Accounts) RecordTwoFactorFailure(ctx context.Context, l ratelimit.Limiter, u models.User) (bool, error) {
	res, err := l.Allow(ctx, twoFactorAccountKey(u))
	if err != nil {
		return true, fmt.Errorf("accounts: two-factor account limit: %w", err)
	}

	if !res.Allowed {
		return true, nil
	}

	if res.Remaining > 0 {
		return false, nil
	}

	return true, a.Mailer.Send(ctx, mail.TwoFactorLocked{User: u})
}
