package admin

import "testing"

func TestSecurity_A09_AdminSensitiveColumnNames(t *testing.T) {
	t.Parallel()

	for col, want := range map[string]bool{
		"password": true, "password_hash": true, "hashed_password": true, "passwd": true,
		"client_secret": true, "api_key": true, "apikey": true, "private_key": true,
		"token": true, "api_token": true, "remember_token": true, "token_hash": true, "refresh_tokens": true,
		"accesstoken": true, "authtokens": true, "userotp": true,
		"otp": true, "otp_secret": true, "totp_secret": true, "recovery_codes": true,
		"tokens_used": false, "token_count_total": true, "max_tokens_per_day": false,
		"footprint": false, "notes": false, "email": false,
	} {
		if got := sensitiveRe.MatchString(col); got != want {
			t.Errorf("sensitiveRe.MatchString(%q) = %v, want %v", col, got, want)
		}
	}
}
