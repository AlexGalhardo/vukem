package config

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestParseKey(t *testing.T) {
	t.Parallel()

	raw := bytes.Repeat([]byte{7}, 32)
	enc := base64.StdEncoding.EncodeToString(raw)

	tests := []struct {
		name    string
		in      string
		wantErr string
	}{
		{name: "prefixed", in: "base64:" + enc},
		{name: "bare base64", in: enc},
		{name: "empty", in: "", wantErr: "APP_KEY is not set"},
		{name: "bad base64", in: "base64:@@@", wantErr: "not valid base64"},
		{name: "short", in: "base64:" + base64.StdEncoding.EncodeToString(raw[:16]), wantErr: "at least 32 bytes"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := ParseKey(NewSecret(tt.in))
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("ParseKey() error = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil || !bytes.Equal(got.bytes, raw) {
				t.Fatalf("ParseKey() = %v, %v", got.bytes, err)
			}
		})
	}
}

func TestKeyDerive(t *testing.T) {
	t.Parallel()

	k, err := ParseKey(NewSecret("base64:" + base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 32))))
	if err != nil {
		t.Fatal(err)
	}

	a1 := k.Derive("session", 32)
	a2 := k.Derive("session", 32)
	b := k.Derive("cookie", 32)

	if len(a1) != 32 || !bytes.Equal(a1, a2) {
		t.Fatal("Derive() is not deterministic or wrong length")
	}
	if bytes.Equal(a1, b) {
		t.Fatal("Derive() returned the same key for different purposes")
	}
	if bytes.Equal(a1, k.bytes) {
		t.Fatal("Derive() returned the raw key")
	}
}

func TestGenerateKey(t *testing.T) {
	t.Parallel()

	s := GenerateKey()
	if !strings.HasPrefix(s, "base64:") {
		t.Fatalf("GenerateKey() = %q, want base64: prefix", s)
	}
	if _, err := ParseKey(NewSecret(s)); err != nil {
		t.Fatalf("ParseKey(GenerateKey()) error = %v", err)
	}
	if GenerateKey() == s {
		t.Fatal("GenerateKey() repeated a key")
	}
}

func TestSecurity_A04_ZeroKeyNeverDerives(t *testing.T) {
	t.Parallel()

	if _, err := ParseKey(NewSecret("")); !errors.Is(err, ErrKeyMissing) {
		t.Fatalf("ParseKey(\"\") error = %v, want ErrKeyMissing", err)
	}

	var k Key
	if !k.IsZero() {
		t.Fatal("Key{}.IsZero() = false, want true")
	}

	defer func() {
		if p := fmt.Sprint(recover()); !strings.Contains(p, "APP_KEY is not set") {
			t.Fatalf("Key{}.Derive() panic = %q, want an APP_KEY message", p)
		}
	}()

	k.Derive("session.cookie", 32)
	t.Fatal("Key{}.Derive() returned, want a panic")
}
