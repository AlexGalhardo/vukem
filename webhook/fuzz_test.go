package webhook

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"strings"
	"testing"
)

func FuzzVerifyHMAC(f *testing.F) {
	secret := []byte("fuzz-secret")
	payload := []byte(`{"id":1}`)

	f.Add(payload, Sign(secret, payload))
	f.Add(payload, "sha256="+Sign(secret, payload))
	f.Add(payload, "")
	f.Add([]byte(""), "sha256=")
	f.Add(payload, strings.ToUpper(Sign(secret, payload)))

	f.Fuzz(func(t *testing.T, payload []byte, signature string) {
		if !VerifyHMAC(secret, payload, signature) {
			return
		}

		// Accepted signatures must decode to the real MAC: anything else
		// means a forged signature was accepted.
		m := hmac.New(sha256.New, secret)
		m.Write(payload)
		want := m.Sum(nil)

		sig := strings.TrimPrefix(strings.TrimSpace(signature), "sha256=")
		for _, decode := range []func(string) ([]byte, error){
			hex.DecodeString, base64.StdEncoding.DecodeString, base64.RawURLEncoding.DecodeString,
		} {
			if got, err := decode(sig); err == nil && hmac.Equal(got, want) {
				return
			}
		}

		t.Fatalf("VerifyHMAC(%q, %q) = true for a signature that is not the MAC", payload, signature)
	})
}
