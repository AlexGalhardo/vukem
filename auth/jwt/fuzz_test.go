package jwt_test

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"strings"
	"testing"

	"github.com/AlexGalhardo/vukem/auth/jwt"
)

// FuzzVerify feeds arbitrary tokens to a verifier with an HS256 key: any
// accepted token must carry the real HMAC of its header and payload, so a
// forged, unsigned or re-algorithmed token is never accepted.
func FuzzVerify(f *testing.F) {
	secret := []byte("0123456789abcdef0123456789abcdef")
	key := jwt.HS256("hs", secret)
	v := verifier(key)

	valid, err := jwt.Sign(key, claims())
	if err != nil {
		f.Fatal(err)
	}

	f.Add(valid)
	f.Add(unsigned(`{"alg":"none","typ":"JWT"}`, `{"sub":"42","exp":9999999999}`))
	f.Add(unsigned(`{"alg":"HS256","kid":"hs"}`, `{"sub":"42"}`))
	f.Add(valid[:len(valid)-2])
	f.Add("a.b.c")
	f.Add("..")
	f.Add(strings.Repeat("e30.", 3))

	f.Fuzz(func(t *testing.T, token string) {
		c, err := v.Verify(t.Context(), token)
		if err != nil {
			return
		}

		parts := strings.Split(token, ".")
		if len(parts) != 3 {
			t.Fatalf("Verify(%q) accepted %d segments", token, len(parts))
		}

		m := hmac.New(sha256.New, secret)
		m.Write([]byte(parts[0] + "." + parts[1]))

		sig, derr := base64.RawURLEncoding.DecodeString(parts[2])
		if derr != nil || !hmac.Equal(sig, m.Sum(nil)) {
			t.Fatalf("Verify(%q) = %+v for a token without a valid signature", token, c)
		}

		if c.ExpiresAt.IsZero() {
			t.Fatalf("Verify(%q) accepted a token without exp", token)
		}
	})
}
