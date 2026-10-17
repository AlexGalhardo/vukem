package models

import "github.com/AlexGalhardo/vukem/orm"

// TwoFactor is the two-factor state of a user, kept in the users table
// (vukem install:auth --2fa). It is a separate model so the secret never
// reaches User, its JSON or the pages that show it. The secret and the
// recovery code hashes are encrypted with APP_KEY.
type TwoFactor struct {
	orm.Model

	TwoFactorSecret        orm.Encrypted[string]   `json:"-"`
	TwoFactorRecoveryCodes orm.Encrypted[[]string] `json:"-"`
	TwoFactorConfirmedAt   orm.NullTime            `json:"-"`
	// TwoFactorLastStep is the time step of the last accepted code: older
	// and equal steps are refused, so a code works once.
	TwoFactorLastStep int64 `json:"-"`
}

// Table maps TwoFactor to the users table.
func (TwoFactor) Table() string { return "users" }

// Enabled reports whether the user confirmed an authenticator app.
func (t TwoFactor) Enabled() bool { return t.TwoFactorConfirmedAt.Valid }

// Pending reports whether a secret waits for its confirmation code.
func (t TwoFactor) Pending() bool { return !t.Enabled() && t.TwoFactorSecret.V != "" }
