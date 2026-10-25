package session

import "testing"

func TestSecretField(t *testing.T) {
	t.Parallel()

	tests := []struct {
		key  string
		want bool
	}{
		{"password", true},
		{"password_confirmation", true},
		{"newPassword", true},
		{"user[Password]", true},
		{"passwd", true},
		{"passphrase", true},
		{"pass", true},
		{"pwd", true},
		{"client_secret", true},
		{"api_token", true},
		{"_token", true},
		{"_method", true},
		{"otp", true},
		{"otp_code", true},
		{"code", true},
		{"Code", true},
		{"recovery_code", true},
		{"card_cvv", true},
		{"cvc", true},
		{"pin", true},
		{"card-pin", true},
		{"title", false},
		{"email", false},
		{"shipping_address", false},
		{"footprint", false},
		{"opinion", false},
		{"compass", false},
		{"zip_code", false},
		{"postcode", false},
	}

	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			t.Parallel()

			if got := secretField(tt.key); got != tt.want {
				t.Errorf("secretField(%q) = %v, want %v", tt.key, got, tt.want)
			}
		})
	}
}
