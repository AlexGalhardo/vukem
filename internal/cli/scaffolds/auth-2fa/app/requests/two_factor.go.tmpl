package requests

import "github.com/AlexGalhardo/vukem/z"

// TwoFactorCode is a code from the authenticator app.
type TwoFactorCode struct {
	Code string `json:"code" form:"code"`
}

// TwoFactorCodeSchema validates TwoFactorCode.
var TwoFactorCodeSchema = z.Struct[TwoFactorCode](z.Shape{"code": z.String().Trim().Min(1).Max(16)})

// TwoFactorChallenge is the login step after the password: a code from
// the app or one of the recovery codes.
type TwoFactorChallenge struct {
	Code         string `json:"code"          form:"code"`
	RecoveryCode string `json:"recovery_code" form:"recovery_code"`
}

// TwoFactorChallengeSchema validates TwoFactorChallenge.
var TwoFactorChallengeSchema = z.Struct[TwoFactorChallenge](z.Shape{
	"code":          z.String().Trim().Max(16).Optional(),
	"recovery_code": z.String().Trim().Max(32).Optional(),
}).Refine(func(r TwoFactorChallenge) error {
	if r.Code == "" && r.RecoveryCode == "" {
		return z.FieldError("code", "Enter the code from your authenticator app or a recovery code.")
	}

	return nil
})

// ConfirmPassword asks for the current password before a sensitive change.
type ConfirmPassword struct {
	Password string `json:"password" form:"password"`
}

// ConfirmPasswordSchema validates ConfirmPassword.
var ConfirmPasswordSchema = z.Struct[ConfirmPassword](z.Shape{"password": z.String().Min(1).Max(256)})
