package totp_test

import (
	"encoding/base32"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/AlexGalhardo/vukem/auth/totp"
)

// RFC 6238 Appendix B test vectors (8 digits).
func TestRFC6238Vectors(t *testing.T) {
	t.Parallel()

	b32 := func(s string) string {
		return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString([]byte(s))
	}

	secrets := map[totp.Algorithm]string{
		totp.SHA1:   b32("12345678901234567890"),
		totp.SHA256: b32("12345678901234567890123456789012"),
		totp.SHA512: b32("1234567890123456789012345678901234567890123456789012345678901234"),
	}

	vectors := []struct {
		unix int64
		alg  totp.Algorithm
		code string
	}{
		{59, totp.SHA1, "94287082"},
		{59, totp.SHA256, "46119246"},
		{59, totp.SHA512, "90693936"},
		{1111111109, totp.SHA1, "07081804"},
		{1111111109, totp.SHA256, "68084774"},
		{1111111109, totp.SHA512, "25091201"},
		{1111111111, totp.SHA1, "14050471"},
		{1234567890, totp.SHA256, "91819424"},
		{2000000000, totp.SHA512, "38618901"},
		{20000000000, totp.SHA1, "65353130"},
		{20000000000, totp.SHA256, "77737706"},
		{20000000000, totp.SHA512, "47863826"},
	}

	for _, v := range vectors {
		opts := totp.Options{Algorithm: v.alg, Digits: 8}

		got, err := totp.Code(secrets[v.alg], time.Unix(v.unix, 0), opts)
		if err != nil || got != v.code {
			t.Errorf("Code(%s, %d) = %q, %v, want %q", v.alg, v.unix, got, err, v.code)
		}
	}
}

func TestValidateWindowAndReplay(t *testing.T) {
	t.Parallel()

	secret, err := totp.GenerateSecret()
	if err != nil {
		t.Fatal(err)
	}

	now := time.Unix(1_800_000_000, 0)
	code := func(at time.Time) string {
		c, err := totp.Code(secret, at, totp.Options{})
		if err != nil {
			t.Fatal(err)
		}

		return c
	}

	step, ok := totp.Validate(secret, code(now), now, 0, totp.Options{})
	if !ok || step != now.Unix()/30 {
		t.Fatalf("current code = %d, %v", step, ok)
	}

	if _, ok := totp.Validate(secret, code(now.Add(-30*time.Second)), now, 0, totp.Options{}); !ok {
		t.Error("previous step rejected (clock skew of one step is allowed)")
	}

	if _, ok := totp.Validate(secret, code(now.Add(-90*time.Second)), now, 0, totp.Options{}); ok {
		t.Error("code from three steps ago accepted")
	}

	// Replay: a step at or before the last accepted one is refused.
	if _, ok := totp.Validate(secret, code(now), now, step, totp.Options{}); ok {
		t.Error("same code accepted twice")
	}

	if _, ok := totp.Validate(secret, code(now.Add(-30*time.Second)), now, step, totp.Options{}); ok {
		t.Error("older step accepted after a newer one was used")
	}

	for _, bad := range []string{"", "12345", "1234567", "abcdef", " 123456"} {
		if _, ok := totp.Validate(secret, bad, now, 0, totp.Options{}); ok {
			t.Errorf("malformed code %q accepted", bad)
		}
	}
}

func TestURI(t *testing.T) {
	t.Parallel()

	u, err := url.Parse(totp.URI("Vukem Shop", "ana@example.com", "JBSWY3DPEHPK3PXP", totp.Options{}))
	if err != nil {
		t.Fatal(err)
	}

	q := u.Query()
	if u.Scheme != "otpauth" || u.Host != "totp" || u.Path != "/Vukem Shop:ana@example.com" ||
		q.Get("secret") != "JBSWY3DPEHPK3PXP" || q.Get("issuer") != "Vukem Shop" ||
		q.Get("algorithm") != "SHA1" || q.Get("digits") != "6" || q.Get("period") != "30" {
		t.Errorf("URI = %s", u)
	}
}

func TestRecoveryCodes(t *testing.T) {
	t.Parallel()

	plain, hashes, err := totp.RecoveryCodes(8)
	if err != nil {
		t.Fatal(err)
	}

	if len(plain) != 8 || len(hashes) != 8 || len(plain[0]) != 11 || plain[0][5] != '-' {
		t.Fatalf("codes = %v", plain)
	}

	for _, h := range hashes {
		for _, p := range plain {
			if strings.Contains(h, p) {
				t.Fatal("hash contains the plain code")
			}
		}
	}

	left, ok := totp.UseRecoveryCode(strings.ToLower(plain[3]), hashes)
	if !ok || len(left) != 7 {
		t.Fatalf("UseRecoveryCode = %d left, %v", len(left), ok)
	}

	if _, ok := totp.UseRecoveryCode(plain[3], left); ok {
		t.Error("recovery code used twice")
	}

	if _, ok := totp.UseRecoveryCode("nope-nope", hashes); ok {
		t.Error("unknown recovery code accepted")
	}
}
