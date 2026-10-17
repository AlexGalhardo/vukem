// Package hash hashes and verifies passwords: argon2id by default (PHC
// string format), bcrypt for compatibility. Verify recognises both, so a
// database can move from bcrypt to argon2id through NeedsRehash on login.
//
// Every Hasher ([Default], [Argon2id], [Bcrypt], [Fake] for tests) makes and
// verifies hashes in constant time; the auth guards take one to check
// passwords on login.
//
// See https://github.com/AlexGalhardo/vukem/blob/main/docs/authentication.md.
package hash

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
	"golang.org/x/crypto/bcrypt"
)

// ErrPasswordTooLong is returned by bcrypt for inputs over 72 bytes, which
// bcrypt would otherwise silently truncate.
var ErrPasswordTooLong = errors.New("hash: password longer than 72 bytes (bcrypt limit)")

// ErrUnknownFormat is returned for hashes neither argon2id nor bcrypt.
var ErrUnknownFormat = errors.New("hash: unknown hash format")

// Hasher creates password hashes and verifies them in constant time.
type Hasher interface {
	// Make hashes password with a fresh random salt.
	Make(password string) (string, error)
	// Verify reports whether password matches hash (argon2id or bcrypt).
	Verify(hash, password string) (bool, error)
	// NeedsRehash reports whether hash was made with other parameters or
	// algorithm than this hasher's, so it should be replaced after login.
	NeedsRehash(hash string) bool
}

// Argon2Params configures argon2id (RFC 9106).
type Argon2Params struct {
	Memory  uint32 // KiB
	Time    uint32
	Threads uint8
	SaltLen uint32
	KeyLen  uint32
}

// DefaultArgon2 follows the OWASP password storage recommendation
// (m=64 MiB, t=1, p=4) with 16-byte salts and 32-byte keys.
var DefaultArgon2 = Argon2Params{
	Memory:  64 * 1024,
	Time:    1,
	Threads: 4,
	SaltLen: 16,
	KeyLen:  32,
} //nolint:gochecknoglobals // read-only defaults

// Default returns the argon2id hasher with DefaultArgon2.
func Default() Hasher { return Argon2id(DefaultArgon2) }

// Fake returns a fast argon2id hasher for tests (never in production).
func Fake() Hasher {
	return Argon2id(Argon2Params{Memory: 8, Time: 1, Threads: 1, SaltLen: 8, KeyLen: 16})
}

// Argon2id returns an argon2id hasher.
func Argon2id(p Argon2Params) Hasher { return argonHasher{p: p} }

// Bcrypt returns a bcrypt hasher (cost 4–31; 12 is a good default).
func Bcrypt(cost int) Hasher { return bcryptHasher{cost: cost} }

type argonHasher struct{ p Argon2Params }

const argonPrefix = "$argon2id$"

func (h argonHasher) Make(password string) (string, error) {
	salt := make([]byte, h.p.SaltLen)
	_, _ = rand.Read(salt) // never fails since Go 1.24

	key := argon2.IDKey([]byte(password), salt, h.p.Time, h.p.Memory, h.p.Threads, h.p.KeyLen)
	enc := base64.RawStdEncoding

	return fmt.Sprintf("%sv=%d$m=%d,t=%d,p=%d$%s$%s", argonPrefix, argon2.Version, h.p.Memory, h.p.Time, h.p.Threads,
		enc.EncodeToString(salt), enc.EncodeToString(key)), nil
}

func (argonHasher) Verify(hash, password string) (bool, error) { return verify(hash, password) }

func (h argonHasher) NeedsRehash(hash string) bool {
	p, _, _, err := parseArgon(hash)

	return err != nil || p.Memory != h.p.Memory || p.Time != h.p.Time || p.Threads != h.p.Threads ||
		p.KeyLen != h.p.KeyLen || p.SaltLen != h.p.SaltLen
}

type bcryptHasher struct{ cost int }

func (h bcryptHasher) Make(password string) (string, error) {
	if len(password) > 72 {
		return "", ErrPasswordTooLong
	}

	b, err := bcrypt.GenerateFromPassword([]byte(password), h.cost)

	return string(b), err
}

func (bcryptHasher) Verify(hash, password string) (bool, error) { return verify(hash, password) }

func (h bcryptHasher) NeedsRehash(hash string) bool {
	cost, err := bcrypt.Cost([]byte(hash))
	return err != nil || cost != h.cost
}

// Verify checks password against an argon2id or bcrypt hash.
func Verify(hash, password string) (bool, error) { return verify(hash, password) }

func verify(hash, password string) (bool, error) {
	switch {
	case strings.HasPrefix(hash, argonPrefix):
		p, salt, key, err := parseArgon(hash)
		if err != nil {
			return false, err
		}

		got := argon2.IDKey([]byte(password), salt, p.Time, p.Memory, p.Threads, p.KeyLen)

		return subtle.ConstantTimeCompare(got, key) == 1, nil
	case strings.HasPrefix(hash, "$2a$"), strings.HasPrefix(hash, "$2b$"), strings.HasPrefix(hash, "$2y$"):
		if len(password) > 72 {
			return false, nil
		}

		err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
		if errors.Is(err, bcrypt.ErrMismatchedHashAndPassword) {
			return false, nil
		}

		return err == nil, err
	default:
		return false, ErrUnknownFormat
	}
}

func parseArgon(hash string) (Argon2Params, []byte, []byte, error) {
	var (
		p       Argon2Params
		version int
	)

	parts := strings.Split(hash, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return p, nil, nil, ErrUnknownFormat
	}

	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return p, nil, nil, fmt.Errorf("hash: unsupported argon2 version %q", parts[2])
	}

	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &p.Memory, &p.Time, &p.Threads); err != nil {
		return p, nil, nil, fmt.Errorf("hash: bad argon2 parameters: %w", err)
	}

	enc := base64.RawStdEncoding

	salt, err := enc.DecodeString(parts[4])
	if err != nil {
		return p, nil, nil, fmt.Errorf("hash: bad argon2 salt: %w", err)
	}

	key, err := enc.DecodeString(parts[5])
	if err != nil {
		return p, nil, nil, fmt.Errorf("hash: bad argon2 key: %w", err)
	}

	p.SaltLen, p.KeyLen = uint32(len(salt)), uint32(len(key)) //nolint:gosec // lengths come from short base64 fields

	if p.Memory == 0 || p.Time == 0 || p.Threads == 0 || p.KeyLen == 0 || p.Memory > 4*1024*1024 {
		return p, nil, nil, errors.New("hash: argon2 parameters out of range")
	}

	return p, salt, key, nil
}
