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
		// Token and OTP words anywhere stay secret (fail closed).
		"tokens_hash": true, "tokens_digest": true, "tokens_json": true, "tokenhash": true, "tokenvalue": true,
		"accesstoken_hash": true, "authtoken_digest": true, "otpcode": true, "otphash": true, "otpbackup": true,
		"apiTokenHash": true, "refreshTokenId": true, "token_count_total": true,
		// Only these known counters are exempt.
		"tokens_used": false, "max_tokens": false, "max_tokens_per_day": false, "prompt_token_count": false,
		"footprint": false, "notes": false, "email": false,
	} {
		if got := sensitiveName(col); got != want {
			t.Errorf("sensitiveName(%q) = %v, want %v", col, got, want)
		}
	}
}
