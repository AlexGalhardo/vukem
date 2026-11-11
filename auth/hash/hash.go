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
	"runtime"
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
	// MaxConcurrent bounds how many argon2id computations the hasher runs
	// at once (each one allocates Memory KiB); the rest wait. 0 means
	// runtime.GOMAXPROCS(0).
	MaxConcurrent int
}

// DefaultArgon2Params returns the OWASP password storage recommendation
// (m=64 MiB, t=1, p=4) with 16-byte salts and 32-byte keys. It returns a
// copy, so tuning one field never weakens Default for the whole process.
func DefaultArgon2Params() Argon2Params {
	return Argon2Params{
		Memory:  64 * 1024,
		Time:    1,
		Threads: 4,
		SaltLen: 16,
		KeyLen:  32,
	}
}

// Default returns the argon2id hasher with DefaultArgon2Params.
func Default() Hasher { return Argon2id(DefaultArgon2Params()) }

// Fake returns a fast argon2id hasher for tests (never in production).
func Fake() Hasher {
	return Argon2id(Argon2Params{Memory: 8, Time: 1, Threads: 1, SaltLen: 8, KeyLen: 16})
}

// Argon2id returns an argon2id hasher. The limit of Argon2Params.MaxConcurrent
// belongs to the returned hasher, so create one and reuse it: a hasher per
// request does not bound anything.
func Argon2id(p Argon2Params) Hasher {
	n := p.MaxConcurrent
	if n <= 0 {
		n = runtime.GOMAXPROCS(0)
	}

	return argonHasher{p: p, sem: make(chan struct{}, n)}
}

// Bcrypt returns a bcrypt hasher (cost 4–31; 12 is a good default).
func Bcrypt(cost int) Hasher { return bcryptHasher{cost: cost} }

// argonHasher is copied by value; sem is a channel, so every copy shares the
// one limit.
type argonHasher struct {
	p   Argon2Params
	sem chan struct{}
}

// limit runs fn once a slot of the hasher's concurrency limit is free.
func (h argonHasher) limit(fn func()) {
	h.sem <- struct{}{}
	defer func() { <-h.sem }()

	fn()
}

const argonPrefix = "$argon2id$"

func (h argonHasher) Make(password string) (string, error) {
	salt := make([]byte, h.p.SaltLen)
	_, _ = rand.Read(salt) // never fails since Go 1.24

	var key []byte

	h.limit(func() { key = argon2.IDKey([]byte(password), salt, h.p.Time, h.p.Memory, h.p.Threads, h.p.KeyLen) })

	enc := base64.RawStdEncoding

	return fmt.Sprintf("%sv=%d$m=%d,t=%d,p=%d$%s$%s", argonPrefix, argon2.Version, h.p.Memory, h.p.Time, h.p.Threads,
		enc.EncodeToString(salt), enc.EncodeToString(key)), nil
}

func (h argonHasher) Verify(hash, password string) (bool, error) {
	if !strings.HasPrefix(hash, argonPrefix) {
		return verify(hash, password)
	}

	var (
		ok  bool
		err error
	)

	h.limit(func() { ok, err = verify(hash, password) })

	return ok, err
}

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

// Verify checks password against an argon2id or bcrypt hash. It does not
// bound concurrency: use a Hasher's Verify for that.
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
