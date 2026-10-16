package config

import (
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
)

// MinKeyLen is the minimum APP_KEY length in bytes.
const MinKeyLen = 32

// Key is the decoded application key (APP_KEY). Never use it directly for
// cryptography: derive a purpose-specific key with Derive.
type Key struct {
	bytes []byte
}

// ParseKey decodes an APP_KEY in the form "base64:<std base64>" (the prefix
// is optional) and enforces MinKeyLen.
func ParseKey(s Secret) (Key, error) {
	v := s.Reveal()
	if v == "" {
		return Key{}, errors.New("config: APP_KEY is not set (run `vukem key:generate`)")
	}

	b, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(v, "base64:"))
	if err != nil {
		return Key{}, errors.New("config: APP_KEY is not valid base64")
	}

	if len(b) < MinKeyLen {
		return Key{}, fmt.Errorf("config: APP_KEY must be at least %d bytes, got %d", MinKeyLen, len(b))
	}

	return Key{bytes: b}, nil
}

// Derive returns an n-byte key for purpose using HKDF-SHA256, so cookies,
// sessions and encryption never share key material.
func (k Key) Derive(purpose string, n int) []byte {
	out, err := hkdf.Key(sha256.New, k.bytes, nil, "vukem:"+purpose, n)
	if err != nil {
		// Only possible when n exceeds 255*32 bytes: a programming error.
		panic(fmt.Sprintf("config: derive %q: %v", purpose, err))
	}

	return out
}

// GenerateKey returns a new random APP_KEY value ("base64:..." of 32 bytes).
func GenerateKey() string {
	b := make([]byte, MinKeyLen)
	_, _ = rand.Read(b) // never fails since Go 1.24

	return "base64:" + base64.StdEncoding.EncodeToString(b)
}
