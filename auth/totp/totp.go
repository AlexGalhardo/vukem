// Package totp implements time-based one-time passwords (RFC 6238, the
// codes of authenticator apps) for two-factor authentication: secrets,
// otpauth:// enrolment URIs with an SVG QR code, validation with one step
// of clock skew and replay protection, and hashed single-use recovery
// codes. Standard library only.
//
// See https://github.com/AlexGalhardo/vukem/blob/main/docs/authentication.md.
package totp

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1" //nolint:gosec // G505: HMAC-SHA1 is the RFC 6238 default every authenticator app uses
	"crypto/sha256"
	"crypto/sha512"
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"hash"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Algorithm is the HMAC hash of the codes.
type Algorithm string

// Algorithms. SHA1 is what authenticator apps expect.
const (
	SHA1   Algorithm = "SHA1"
	SHA256 Algorithm = "SHA256"
	SHA512 Algorithm = "SHA512"
)

// Options tune code generation; zero values are the authenticator-app
// defaults (SHA1, 6 digits, 30s).
type Options struct {
	Algorithm Algorithm
	Digits    int
	Period    time.Duration
}

func (o Options) withDefaults() Options {
	if o.Algorithm == "" {
		o.Algorithm = SHA1
	}

	if o.Digits == 0 {
		o.Digits = 6
	}

	if o.Period == 0 {
		o.Period = 30 * time.Second
	}

	return o
}

var b32 = base32.StdEncoding.WithPadding(base32.NoPadding)

// GenerateSecret returns a new 160-bit secret, base32 encoded.
func GenerateSecret() (string, error) {
	b := make([]byte, 20)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}

	return b32.EncodeToString(b), nil
}

// Code returns the code of secret (base32) at t.
func Code(secret string, t time.Time, o Options) (string, error) {
	o = o.withDefaults()

	key, err := b32.DecodeString(strings.ToUpper(strings.TrimRight(strings.ReplaceAll(secret, " ", ""), "=")))
	if err != nil {
		return "", fmt.Errorf("totp: bad secret: %w", err)
	}

	return code(key, t.Unix()/int64(o.Period.Seconds()), o)
}

func code(key []byte, step int64, o Options) (string, error) {
	var h func() hash.Hash

	switch o.Algorithm {
	case SHA1:
		h = sha1.New
	case SHA256:
		h = sha256.New
	case SHA512:
		h = sha512.New
	default:
		return "", fmt.Errorf("totp: unsupported algorithm %q", o.Algorithm)
	}

	if o.Digits < 6 || o.Digits > 10 {
		return "", fmt.Errorf("totp: %d digits not supported", o.Digits)
	}

	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], uint64(step)) //nolint:gosec // G115: steps are positive

	mac := hmac.New(h, key)
	mac.Write(msg[:])
	sum := mac.Sum(nil)

	off := sum[len(sum)-1] & 0x0f
	bin := binary.BigEndian.Uint32(sum[off:off+4]) & 0x7fffffff

	mod := uint64(1)
	for range o.Digits {
		mod *= 10
	}

	return fmt.Sprintf("%0*d", o.Digits, uint64(bin)%mod), nil
}

// Validate checks code at now, accepting the previous and next step for
// clock skew. lastStep is the step of the code the user last used (0 for
// none): codes at or before it are refused, so a code works once. It
// returns the matched step, to store as the new lastStep.
func Validate(secret, code string, now time.Time, lastStep int64, o Options) (int64, bool) {
	o = o.withDefaults()

	if len(code) != o.Digits {
		return 0, false
	}

	for _, ch := range code {
		if ch < '0' || ch > '9' {
			return 0, false
		}
	}

	key, err := b32.DecodeString(strings.ToUpper(strings.TrimRight(strings.ReplaceAll(secret, " ", ""), "=")))
	if err != nil {
		return 0, false
	}

	current := now.Unix() / int64(o.Period.Seconds())

	matched := int64(0)

	// Check every candidate (no early exit) so timing does not reveal
	// which step matched.
	for _, step := range []int64{current - 1, current, current + 1} {
		want, err := codeFor(key, step, o)
		if err != nil {
			return 0, false
		}

		if subtle.ConstantTimeCompare([]byte(want), []byte(code)) == 1 && step > lastStep {
			matched = step
		}
	}

	return matched, matched != 0
}

func codeFor(key []byte, step int64, o Options) (string, error) { return code(key, step, o) }

// URI returns the otpauth:// URI authenticator apps import (usually from
// a QR code, see QR).
func URI(issuer, account, secret string, o Options) string {
	o = o.withDefaults()

	q := url.Values{}
	q.Set("secret", secret)
	q.Set("issuer", issuer)
	q.Set("algorithm", string(o.Algorithm))
	q.Set("digits", strconv.Itoa(o.Digits))
	q.Set("period", strconv.Itoa(int(o.Period.Seconds())))

	u := url.URL{Scheme: "otpauth", Host: "totp", Path: "/" + issuer + ":" + account, RawQuery: q.Encode()}

	return u.String()
}

// RecoveryCodes returns n single-use codes ("ABCDE-FGHIJ") to show the
// user once, and their hashes to store.
func RecoveryCodes(n int) (plain, hashes []string, err error) {
	for range n {
		b := make([]byte, 7)
		if _, err := rand.Read(b); err != nil {
			return nil, nil, err
		}

		s := b32.EncodeToString(b)[:10]
		c := s[:5] + "-" + s[5:]

		plain = append(plain, c)
		hashes = append(hashes, HashRecoveryCode(c))
	}

	return plain, hashes, nil
}

// HashRecoveryCode returns the stored form of a recovery code, as
// RecoveryCodes returns it: hex SHA-256 of "totp-recovery:" followed by the
// code trimmed and upper-cased. Use it to import codes generated elsewhere
// (another auth system) so UseRecoveryCode accepts them.
func HashRecoveryCode(c string) string {
	sum := sha256.Sum256([]byte("totp-recovery:" + strings.ToUpper(strings.TrimSpace(c))))
	return hex.EncodeToString(sum[:])
}

// UseRecoveryCode reports whether code matches one of hashes and returns
// the hashes left after consuming it (store them).
func UseRecoveryCode(code string, hashes []string) ([]string, bool) {
	h := HashRecoveryCode(code)
	idx := -1

	for i, stored := range hashes {
		if subtle.ConstantTimeCompare([]byte(stored), []byte(h)) == 1 {
			idx = i
		}
	}

	if idx < 0 {
		return hashes, false
	}

	left := make([]string, 0, len(hashes)-1)
	left = append(left, hashes[:idx]...)

	return append(left, hashes[idx+1:]...), true
}
