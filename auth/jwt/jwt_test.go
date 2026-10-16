package jwt_test

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/AlexGalhardo/vukem/auth/jwt"
)

var now = time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)

func clock() time.Time { return now }

func keys(t *testing.T) []jwt.Key {
	t.Helper()

	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}

	ecKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	_, edKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	return []jwt.Key{
		jwt.HS256("hs", []byte("0123456789abcdef0123456789abcdef")),
		jwt.RS256("rs", rsaKey),
		jwt.ES256("es", ecKey),
		jwt.EdDSA("ed", edKey),
	}
}

func claims() jwt.Claims {
	return jwt.Claims{
		Subject: "42", Issuer: "https://vukem.test", Audience: []string{"api"},
		ExpiresAt: now.Add(time.Hour), IssuedAt: now, Extra: map[string]any{"role": "admin"},
	}
}

func verifier(ks ...jwt.Key) *jwt.Verifier {
	return jwt.NewVerifier(jwt.StaticKeys(ks...), jwt.VerifyOptions{
		Issuer: "https://vukem.test", Audience: "api", Now: clock,
	})
}

func TestRoundTripEveryAlgorithm(t *testing.T) {
	t.Parallel()

	for _, k := range keys(t) {
		t.Run(k.Alg, func(t *testing.T) {
			t.Parallel()

			tok, err := jwt.Sign(k, claims())
			if err != nil {
				t.Fatal(err)
			}

			got, err := verifier(k).Verify(t.Context(), tok)
			if err != nil {
				t.Fatalf("Verify() = %v", err)
			}

			if got.Subject != "42" || got.Extra["role"] != "admin" || !got.ExpiresAt.Equal(now.Add(time.Hour)) {
				t.Errorf("claims = %+v", got)
			}
		})
	}
}

func TestVerifyRejects(t *testing.T) {
	t.Parallel()

	ks := keys(t)
	hs, rs := ks[0], ks[1]

	sign := func(c jwt.Claims) string {
		tok, err := jwt.Sign(hs, c)
		if err != nil {
			t.Fatal(err)
		}

		return tok
	}

	with := func(fn func(*jwt.Claims)) string {
		c := claims()
		fn(&c)

		return sign(c)
	}

	good := sign(claims())
	parts := strings.Split(good, ".")

	tests := map[string]string{
		"expired":          with(func(c *jwt.Claims) { c.ExpiresAt = now.Add(-time.Minute) }),
		"no expiry":        with(func(c *jwt.Claims) { c.ExpiresAt = time.Time{} }),
		"not yet valid":    with(func(c *jwt.Claims) { c.NotBefore = now.Add(time.Minute) }),
		"issued in future": with(func(c *jwt.Claims) { c.IssuedAt = now.Add(time.Hour) }),
		"wrong issuer":     with(func(c *jwt.Claims) { c.Issuer = "https://evil.test" }),
		"wrong audience":   with(func(c *jwt.Claims) { c.Audience = []string{"web"} }),
		"tampered payload": parts[0] + "." + base64.RawURLEncoding.EncodeToString(
			[]byte(`{"sub":"1","exp":9999999999}`),
		) + "." + parts[2],
		"bad signature":   parts[0] + "." + parts[1] + "." + base64.RawURLEncoding.EncodeToString([]byte("nope")),
		"not three parts": parts[0] + "." + parts[1],
		"garbage":         "a.b.c",
		"alg none":        unsigned(`{"alg":"none","typ":"JWT"}`, parts[1]),
		"unknown kid": resign(
			t,
			`{"alg":"HS256","kid":"other"}`,
			parts[1],
			[]byte("0123456789abcdef0123456789abcdef"),
		),
		"alg not allowed": resign(
			t,
			`{"alg":"HS384","kid":"hs"}`,
			parts[1],
			[]byte("0123456789abcdef0123456789abcdef"),
		),
		"alg/key mismatch":  resign(t, `{"alg":"HS256","kid":"rs"}`, parts[1], rsaPublicBytes(t, rs)),
		"oversized":         strings.Repeat("a", 9000) + "." + parts[1] + "." + parts[2],
		"duplicate members": unsignedHS(t, `{"alg":"HS256","kid":"hs"}`, `{"sub":"1","sub":"2","exp":9999999999}`),
	}

	v := verifier(ks...)

	for name, tok := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if _, err := v.Verify(t.Context(), tok); !errors.Is(err, jwt.ErrInvalid) {
				t.Errorf("Verify(%s) = %v, want ErrInvalid", name, err)
			}
		})
	}
}

func TestSecurity_A04_JWTAlgorithmConfusion(t *testing.T) {
	t.Parallel()

	ks := keys(t)
	rs := ks[1]

	// An attacker signs with HMAC using the RSA public key as the secret.
	tok := resign(
		t,
		`{"alg":"HS256","kid":"rs"}`,
		base64.RawURLEncoding.EncodeToString(
			mustJSON(
				t,
				map[string]any{"sub": "1", "exp": now.Add(time.Hour).Unix(), "iss": "https://vukem.test", "aud": "api"},
			),
		),
		rsaPublicBytes(t, rs),
	)

	if _, err := verifier(rs).Verify(t.Context(), tok); !errors.Is(err, jwt.ErrInvalid) {
		t.Errorf("HS256 token verified against an RSA key: %v", err)
	}

	if _, err := verifier(rs).Verify(t.Context(), unsigned(`{"alg":"none"}`, "e30")); !errors.Is(err, jwt.ErrInvalid) {
		t.Error(`alg "none" accepted`)
	}
}

func TestWeakKeysRejected(t *testing.T) {
	t.Parallel()

	if _, err := jwt.Sign(jwt.HS256("short", []byte("too short")), claims()); err == nil {
		t.Error("HS256 with a 9-byte secret signed")
	}

	small, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := jwt.Sign(jwt.RS256("small", small), claims()); err == nil {
		t.Error("RS256 with a 1024-bit key signed")
	}
}

func unsigned(header, payload string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(header)) + "." + payload + "."
}

func unsignedHS(t *testing.T, header, payload string) string {
	t.Helper()

	return resign(
		t,
		header,
		base64.RawURLEncoding.EncodeToString([]byte(payload)),
		[]byte("0123456789abcdef0123456789abcdef"),
	)
}

func resign(t *testing.T, header, payload string, secret []byte) string {
	t.Helper()

	input := base64.RawURLEncoding.EncodeToString([]byte(header)) + "." + payload
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(input))

	return input + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func rsaPublicBytes(t *testing.T, k jwt.Key) []byte {
	t.Helper()

	b, err := x509.MarshalPKIXPublicKey(k.Public)
	if err != nil {
		t.Fatal(err)
	}

	return b
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()

	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}

	return b
}
