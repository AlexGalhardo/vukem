package crypt

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func newKey(tb testing.TB) []byte {
	tb.Helper()

	k := make([]byte, KeySize)
	_, _ = rand.Read(k)

	return k
}

func mustNew(tb testing.TB, current []byte, previous ...[]byte) *Encrypter {
	tb.Helper()

	e, err := New(current, previous...)
	if err != nil {
		tb.Fatalf("New() error = %v", err)
	}

	return e
}

func TestNewRejectsBadKeys(t *testing.T) {
	t.Parallel()

	good := newKey(t)

	tests := []struct {
		name     string
		current  []byte
		previous [][]byte
	}{
		{name: "nil current", current: nil},
		{name: "short current", current: make([]byte, 16)},
		{name: "long current", current: make([]byte, 64)},
		{name: "short previous", current: good, previous: [][]byte{make([]byte, 31)}},
		{name: "empty previous", current: good, previous: [][]byte{{}}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if _, err := New(tt.current, tt.previous...); err == nil {
				t.Fatalf("New(%d bytes) error = nil, want an error", len(tt.current))
			}
		})
	}
}

func TestEncryptDecryptRoundTrip(t *testing.T) {
	t.Parallel()

	e := mustNew(t, newKey(t))

	tests := []struct {
		name  string
		plain []byte
	}{
		{name: "empty", plain: []byte{}},
		{name: "text", plain: []byte("4111 1111 1111 1111")},
		{name: "binary", plain: []byte{0, 1, 2, 255, 254}},
		{name: "large", plain: bytes.Repeat([]byte("x"), 64<<10)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			token := e.Encrypt(tt.plain)
			if strings.ContainsAny(token, "+/=") {
				t.Fatalf("Encrypt() = %q, want unpadded base64url", token)
			}

			got, err := e.Decrypt(token)
			if err != nil {
				t.Fatalf("Decrypt() error = %v", err)
			}

			if diff := cmp.Diff(tt.plain, got, cmp.Comparer(bytes.Equal)); diff != "" {
				t.Fatalf("Decrypt(Encrypt(x)) mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestEncryptStringAndRandomNonce(t *testing.T) {
	t.Parallel()

	e := mustNew(t, newKey(t))

	a, b := e.EncryptString("secret"), e.EncryptString("secret")
	if a == b {
		t.Fatalf("EncryptString() returned the same token twice: %q", a)
	}

	got, err := e.DecryptString(a)
	if err != nil || got != "secret" {
		t.Fatalf("DecryptString() = %q, %v, want %q, nil", got, err, "secret")
	}
}

func TestSecurity_A04_KeyRotation(t *testing.T) {
	t.Parallel()

	oldKey, newKeyB := newKey(t), newKey(t)
	old := mustNew(t, oldKey)
	rotated := mustNew(t, newKeyB, oldKey)
	fresh := mustNew(t, newKeyB)

	legacy := old.Seal([]byte("card"), []byte("users.card"))

	got, err := rotated.Open(legacy, []byte("users.card"))
	if err != nil || string(got) != "card" {
		t.Fatalf("Open(old token) with previous key = %q, %v, want card, nil", got, err)
	}

	if !rotated.NeedsRotation(legacy) {
		t.Fatal("NeedsRotation(old token) = false, want true")
	}

	if _, err := fresh.Open(legacy, []byte("users.card")); !errors.Is(err, ErrInvalid) {
		t.Fatalf("Open(old token) without previous key error = %v, want ErrInvalid", err)
	}

	renewed, err := rotated.Rotate(legacy, []byte("users.card"))
	if err != nil {
		t.Fatalf("Rotate() error = %v", err)
	}

	if rotated.NeedsRotation(renewed) {
		t.Fatal("NeedsRotation(rotated token) = true, want false")
	}

	if got, err := fresh.Open(renewed, []byte("users.card")); err != nil || string(got) != "card" {
		t.Fatalf("Open(rotated token) with the new key only = %q, %v, want card, nil", got, err)
	}

	if _, err := old.Open(rotated.Seal([]byte("x"), nil), nil); !errors.Is(err, ErrInvalid) {
		t.Fatalf("old Encrypter opened a token sealed with the new key: err = %v", err)
	}

	if _, err := rotated.Rotate("garbage", nil); !errors.Is(err, ErrInvalid) {
		t.Fatalf("Rotate(garbage) error = %v, want ErrInvalid", err)
	}

	if rotated.NeedsRotation("garbage") {
		t.Fatal("NeedsRotation(garbage) = true, want false")
	}
}

func TestSecurity_A04_TamperingRejected(t *testing.T) {
	t.Parallel()

	e := mustNew(t, newKey(t))
	token := e.Encrypt([]byte("payload"))

	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		t.Fatalf("token is not base64url: %v", err)
	}

	for i := range raw {
		flipped := bytes.Clone(raw)
		flipped[i] ^= 0x01

		if _, err := e.Decrypt(base64.RawURLEncoding.EncodeToString(flipped)); !errors.Is(err, ErrInvalid) {
			t.Fatalf("Decrypt(byte %d flipped) error = %v, want ErrInvalid", i, err)
		}
	}

	malformed := []string{
		"",
		"not base64!",
		token[:len(token)-1],
		base64.RawURLEncoding.EncodeToString(raw[:headerSize+10]),
		token + "A",
		base64.StdEncoding.EncodeToString(raw),
	}
	for _, m := range malformed {
		if _, err := e.Decrypt(m); !errors.Is(err, ErrInvalid) {
			t.Fatalf("Decrypt(%q) error = %v, want ErrInvalid", m, err)
		}
	}
}

func TestSecurity_A04_AssociatedDataMismatchRejected(t *testing.T) {
	t.Parallel()

	e := mustNew(t, newKey(t))
	token := e.Seal([]byte("42"), []byte("cookie:cart"))

	for _, ad := range [][]byte{nil, []byte("cookie:admin"), []byte("cookie:cart ")} {
		if _, err := e.Open(token, ad); !errors.Is(err, ErrInvalid) {
			t.Fatalf("Open(token, %q) error = %v, want ErrInvalid", ad, err)
		}
	}

	if _, err := e.Decrypt(token); !errors.Is(err, ErrInvalid) {
		t.Fatalf("Decrypt(token sealed with AD) error = %v, want ErrInvalid", err)
	}

	if got, err := e.Open(token, []byte("cookie:cart")); err != nil || string(got) != "42" {
		t.Fatalf("Open(token, right AD) = %q, %v, want 42, nil", got, err)
	}
}

func TestSecurity_A04_NoPlaintextInCiphertext(t *testing.T) {
	t.Parallel()

	e := mustNew(t, newKey(t))
	plain := "very-recognisable-plaintext"
	token := e.EncryptString(plain)

	raw, _ := base64.RawURLEncoding.DecodeString(token)
	if strings.Contains(token, plain) || bytes.Contains(raw, []byte(plain)) {
		t.Fatalf("ciphertext %q contains the plaintext", token)
	}
}

func FuzzDecrypt(f *testing.F) {
	e := mustNew(f, newKey(f))
	f.Add(e.Encrypt([]byte("seed")))
	f.Add("")
	f.Add("AQAAAAA")

	f.Fuzz(func(t *testing.T, token string) {
		plain, err := e.Decrypt(token)
		if err != nil && !errors.Is(err, ErrInvalid) {
			t.Fatalf("Decrypt(%q) error = %v, want ErrInvalid", token, err)
		}

		if err == nil {
			again, err := e.Decrypt(e.Encrypt(plain))
			if err != nil || !bytes.Equal(again, plain) {
				t.Fatalf("round trip of fuzzed plaintext failed: %v", err)
			}
		}
	})
}
