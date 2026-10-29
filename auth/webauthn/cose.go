package webauthn

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rsa"
	"crypto/sha256"
	"errors"
	"fmt"
	"math/big"
)

// COSE algorithm identifiers (IANA "COSE Algorithms") this package
// verifies.
const (
	AlgES256 int64 = -7
	AlgEdDSA int64 = -8
	AlgRS256 int64 = -257
)

// minRSABits rejects RSA keys too short to sign safely.
const minRSABits = 2048

// COSE key parameters (RFC 9053).
const (
	coseKty    = 1
	coseAlg    = 3
	coseCrv    = -1
	coseX      = -2
	coseY      = -3
	coseRSAN   = -1
	coseRSAE   = -2
	ktyOKP     = 1
	ktyEC2     = 2
	ktyRSA     = 3
	crvP256    = 1
	crvEd25519 = 6
)

// ErrUnsupportedKey reports a credential public key with an algorithm,
// key type or curve this package does not verify.
var ErrUnsupportedKey = errors.New("webauthn: unsupported credential public key")

// publicKey is a parsed COSE_Key.
type publicKey struct {
	alg int64
	key crypto.PublicKey
}

// parseCOSEKey parses a COSE_Key and checks its algorithm matches its key
// type, so a key cannot be used with an algorithm it was not issued for.
func parseCOSEKey(raw []byte) (publicKey, error) {
	v, n, err := decodeCBOR(raw)
	if err != nil {
		return publicKey{}, err
	}

	if n != len(raw) {
		return publicKey{}, fmt.Errorf("%w: trailing bytes after the key", errCBOR)
	}

	m, ok := v.(map[any]any)
	if !ok {
		return publicKey{}, fmt.Errorf("%w: key is not a map", ErrUnsupportedKey)
	}

	kty, _ := m[int64(coseKty)].(int64)
	alg, _ := m[int64(coseAlg)].(int64)

	switch {
	case kty == ktyEC2 && alg == AlgES256:
		return parseEC2(m)
	case kty == ktyOKP && alg == AlgEdDSA:
		return parseOKP(m)
	case kty == ktyRSA && alg == AlgRS256:
		return parseRSA(m)
	default:
		return publicKey{}, fmt.Errorf("%w: kty %d alg %d", ErrUnsupportedKey, kty, alg)
	}
}

func parseEC2(m map[any]any) (publicKey, error) {
	crv, _ := m[int64(coseCrv)].(int64)
	x, _ := m[int64(coseX)].([]byte)
	y, _ := m[int64(coseY)].([]byte)

	if crv != crvP256 || len(x) != 32 || len(y) != 32 {
		return publicKey{}, fmt.Errorf("%w: EC2 key is not a P-256 point", ErrUnsupportedKey)
	}

	// ParseUncompressedPublicKey rejects points off the curve.
	pub, err := ecdsa.ParseUncompressedPublicKey(elliptic.P256(), append(append([]byte{4}, x...), y...))
	if err != nil {
		return publicKey{}, fmt.Errorf("%w: %w", ErrUnsupportedKey, err)
	}

	return publicKey{alg: AlgES256, key: pub}, nil
}

func parseOKP(m map[any]any) (publicKey, error) {
	crv, _ := m[int64(coseCrv)].(int64)
	x, _ := m[int64(coseX)].([]byte)

	if crv != crvEd25519 || len(x) != ed25519.PublicKeySize {
		return publicKey{}, fmt.Errorf("%w: OKP key is not Ed25519", ErrUnsupportedKey)
	}

	return publicKey{alg: AlgEdDSA, key: ed25519.PublicKey(x)}, nil
}

func parseRSA(m map[any]any) (publicKey, error) {
	n, _ := m[int64(coseRSAN)].([]byte)
	e, _ := m[int64(coseRSAE)].([]byte)

	if len(e) == 0 || len(e) > 4 {
		return publicKey{}, fmt.Errorf("%w: RSA exponent", ErrUnsupportedKey)
	}

	exp := int(new(big.Int).SetBytes(e).Int64())
	mod := new(big.Int).SetBytes(n)

	if mod.BitLen() < minRSABits || exp < 3 || exp%2 == 0 {
		return publicKey{}, fmt.Errorf("%w: RSA key below %d bits or bad exponent", ErrUnsupportedKey, minRSABits)
	}

	return publicKey{alg: AlgRS256, key: &rsa.PublicKey{N: mod, E: exp}}, nil
}

// verify checks sig over data with the key's algorithm.
func (k publicKey) verify(data, sig []byte) bool {
	switch pub := k.key.(type) {
	case *ecdsa.PublicKey:
		sum := sha256.Sum256(data)
		return ecdsa.VerifyASN1(pub, sum[:], sig)
	case ed25519.PublicKey:
		return ed25519.Verify(pub, data, sig)
	case *rsa.PublicKey:
		sum := sha256.Sum256(data)
		return rsa.VerifyPKCS1v15(pub, crypto.SHA256, sum[:], sig) == nil
	default:
		return false
	}
}
