// Package jwt issues and verifies JSON Web Tokens (RFC 7519) with the
// standard library: HS256, RS256, ES256 and EdDSA, an algorithm allowlist
// tied to each key (no algorithm confusion, never "none"), key ids with
// static or remote (JWKS) key sets that rotate, strict claim checks, a
// Guard for package auth, and rotating refresh tokens with reuse detection.
//
//	tok, err := jwt.Sign(key, jwt.Claims{Subject: u.AuthID(), ExpiresAt: time.Now().Add(15 * time.Minute)})
//	claims, err := verifier.Verify(ctx, tok)
//
// See https://github.com/AlexGalhardo/vukem/blob/main/docs/authentication.md.
package jwt

import (
	"bytes"
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"math/big"
	"slices"
	"strings"
	"time"
)

// Supported algorithms.
const (
	AlgHS256 = "HS256"
	AlgRS256 = "RS256"
	AlgES256 = "ES256"
	AlgEdDSA = "EdDSA"
)

// ErrInvalid is returned for every token that fails verification; the
// cause is wrapped for logs and never shown to clients.
var ErrInvalid = errors.New("jwt: invalid token")

const (
	maxTokenSize    = 8 << 10
	minHMACKeySize  = 32
	minRSAKeyBits   = 2048
	defaultLeeway   = 30 * time.Second
	reservedClaimsN = 7
)

// Key is a signing or verification key bound to one algorithm.
type Key struct {
	// ID is the "kid" header.
	ID string
	// Alg is the only algorithm this key accepts.
	Alg string
	// Secret is the HS256 secret (at least 32 bytes).
	Secret []byte
	// Private signs (RSA, ECDSA P-256 or Ed25519); nil for verify-only keys.
	Private crypto.Signer
	// Public verifies.
	Public crypto.PublicKey
}

// HS256 returns an HMAC-SHA256 key.
func HS256(id string, secret []byte) Key { return Key{ID: id, Alg: AlgHS256, Secret: secret} }

// RS256 returns an RSA PKCS#1 v1.5 SHA-256 key (at least 2048 bits).
func RS256(id string, k *rsa.PrivateKey) Key {
	return Key{ID: id, Alg: AlgRS256, Private: k, Public: &k.PublicKey}
}

// ES256 returns an ECDSA P-256 SHA-256 key.
func ES256(id string, k *ecdsa.PrivateKey) Key {
	return Key{ID: id, Alg: AlgES256, Private: k, Public: &k.PublicKey}
}

// EdDSA returns an Ed25519 key.
func EdDSA(id string, k ed25519.PrivateKey) Key {
	return Key{ID: id, Alg: AlgEdDSA, Private: k, Public: k.Public()}
}

// Claims are the registered claims plus Extra private claims.
type Claims struct {
	Issuer    string
	Subject   string
	Audience  []string
	ExpiresAt time.Time
	NotBefore time.Time
	IssuedAt  time.Time
	ID        string
	// Extra holds the other claims (decoded JSON values).
	Extra map[string]any
}

func (c Claims) toMap() map[string]any {
	m := make(map[string]any, len(c.Extra)+reservedClaimsN)
	maps.Copy(m, c.Extra)

	set := func(k string, v any, ok bool) {
		if ok {
			m[k] = v
		}
	}

	set("iss", c.Issuer, c.Issuer != "")
	set("sub", c.Subject, c.Subject != "")
	set("jti", c.ID, c.ID != "")
	set("exp", c.ExpiresAt.Unix(), !c.ExpiresAt.IsZero())
	set("nbf", c.NotBefore.Unix(), !c.NotBefore.IsZero())
	set("iat", c.IssuedAt.Unix(), !c.IssuedAt.IsZero())

	switch len(c.Audience) {
	case 0:
	case 1:
		m["aud"] = c.Audience[0]
	default:
		m["aud"] = c.Audience
	}

	return m
}

func claimsFrom(m map[string]any) (Claims, error) {
	var c Claims

	str := func(k string) (string, error) {
		v, ok := m[k]
		if !ok {
			return "", nil
		}

		s, ok := v.(string)
		if !ok {
			return "", fmt.Errorf("claim %q is not a string", k)
		}

		return s, nil
	}

	date := func(k string) (time.Time, error) {
		v, ok := m[k]
		if !ok {
			return time.Time{}, nil
		}

		n, ok := v.(json.Number)
		if !ok {
			return time.Time{}, fmt.Errorf("claim %q is not a number", k)
		}

		f, err := n.Float64()
		if err != nil {
			return time.Time{}, fmt.Errorf("claim %q: %w", k, err)
		}

		return time.Unix(int64(f), 0).UTC(), nil
	}

	var err error
	if c.Issuer, err = str("iss"); err != nil {
		return c, err
	}

	if c.Subject, err = str("sub"); err != nil {
		return c, err
	}

	if c.ID, err = str("jti"); err != nil {
		return c, err
	}

	if c.ExpiresAt, err = date("exp"); err != nil {
		return c, err
	}

	if c.NotBefore, err = date("nbf"); err != nil {
		return c, err
	}

	if c.IssuedAt, err = date("iat"); err != nil {
		return c, err
	}

	switch a := m["aud"].(type) {
	case nil:
	case string:
		c.Audience = []string{a}
	case []any:
		for _, v := range a {
			s, ok := v.(string)
			if !ok {
				return c, errors.New(`claim "aud" holds a non-string`)
			}

			c.Audience = append(c.Audience, s)
		}
	default:
		return c, errors.New(`claim "aud" is not a string or list`)
	}

	c.Extra = map[string]any{}

	for k, v := range m {
		switch k {
		case "iss", "sub", "aud", "exp", "nbf", "iat", "jti":
		default:
			c.Extra[k] = v
		}
	}

	return c, nil
}

type header struct {
	Alg string `json:"alg"`
	Typ string `json:"typ,omitempty"`
	Kid string `json:"kid,omitempty"`
}

var b64 = base64.RawURLEncoding

// Sign returns a compact JWS of claims signed with k.
func Sign(k Key, c Claims) (string, error) {
	if err := k.check(true); err != nil {
		return "", err
	}

	h, err := json.Marshal(header{Alg: k.Alg, Typ: "JWT", Kid: k.ID})
	if err != nil {
		return "", err
	}

	p, err := json.Marshal(c.toMap())
	if err != nil {
		return "", fmt.Errorf("jwt: encode claims: %w", err)
	}

	input := b64.EncodeToString(h) + "." + b64.EncodeToString(p)

	sig, err := k.sign([]byte(input))
	if err != nil {
		return "", err
	}

	return input + "." + b64.EncodeToString(sig), nil
}

func (k Key) check(signing bool) error {
	switch k.Alg {
	case AlgHS256:
		if len(k.Secret) < minHMACKeySize {
			return fmt.Errorf("jwt: HS256 key %q is shorter than %d bytes", k.ID, minHMACKeySize)
		}

		return nil
	case AlgRS256:
		pub, ok := k.Public.(*rsa.PublicKey)
		if !ok || pub.N.BitLen() < minRSAKeyBits {
			return fmt.Errorf("jwt: RS256 key %q needs an RSA key of at least %d bits", k.ID, minRSAKeyBits)
		}
	case AlgES256:
		pub, ok := k.Public.(*ecdsa.PublicKey)
		if !ok || pub.Curve != elliptic256() {
			return fmt.Errorf("jwt: ES256 key %q needs a P-256 key", k.ID)
		}
	case AlgEdDSA:
		if _, ok := k.Public.(ed25519.PublicKey); !ok {
			return fmt.Errorf("jwt: EdDSA key %q needs an Ed25519 key", k.ID)
		}
	default:
		return fmt.Errorf("jwt: unsupported algorithm %q", k.Alg)
	}

	if signing && k.Private == nil {
		return fmt.Errorf("jwt: key %q has no private key", k.ID)
	}

	return nil
}

func (k Key) sign(input []byte) ([]byte, error) {
	sum := sha256.Sum256(input)

	switch k.Alg {
	case AlgHS256:
		mac := hmac.New(sha256.New, k.Secret)
		mac.Write(input)

		return mac.Sum(nil), nil
	case AlgRS256:
		return k.Private.Sign(rand.Reader, sum[:], crypto.SHA256)
	case AlgES256:
		priv, ok := k.Private.(*ecdsa.PrivateKey)
		if !ok {
			return nil, errors.New("jwt: ES256 needs an *ecdsa.PrivateKey")
		}

		r, s, err := ecdsa.Sign(rand.Reader, priv, sum[:])
		if err != nil {
			return nil, err
		}

		out := make([]byte, 64)
		r.FillBytes(out[:32])
		s.FillBytes(out[32:])

		return out, nil
	case AlgEdDSA:
		return k.Private.Sign(rand.Reader, input, crypto.Hash(0))
	}

	return nil, fmt.Errorf("jwt: unsupported algorithm %q", k.Alg)
}

func (k Key) verify(input, sig []byte) bool {
	switch k.Alg {
	case AlgHS256:
		mac := hmac.New(sha256.New, k.Secret)
		mac.Write(input)

		return hmac.Equal(sig, mac.Sum(nil))
	case AlgRS256:
		sum := sha256.Sum256(input)
		pub, ok := k.Public.(*rsa.PublicKey)

		return ok && rsa.VerifyPKCS1v15(pub, crypto.SHA256, sum[:], sig) == nil
	case AlgES256:
		sum := sha256.Sum256(input)
		pub, ok := k.Public.(*ecdsa.PublicKey)

		if !ok || len(sig) != 64 {
			return false
		}

		r, s := new(big.Int).SetBytes(sig[:32]), new(big.Int).SetBytes(sig[32:])

		return ecdsa.Verify(pub, sum[:], r, s)
	case AlgEdDSA:
		pub, ok := k.Public.(ed25519.PublicKey)
		return ok && ed25519.Verify(pub, input, sig)
	}

	return false
}

// KeySet finds verification keys by id (kid); "" asks for the only key.
type KeySet interface {
	Key(ctx context.Context, kid string) (Key, error)
}

type staticKeys []Key

// StaticKeys is a fixed key set (current and previous keys during a
// rotation).
func StaticKeys(keys ...Key) KeySet { return staticKeys(keys) }

func (s staticKeys) Key(_ context.Context, kid string) (Key, error) {
	if kid == "" && len(s) == 1 {
		return s[0], nil
	}

	for _, k := range s {
		if k.ID == kid {
			return k, nil
		}
	}

	return Key{}, fmt.Errorf("unknown key id %q", kid)
}

// VerifyOptions configures a Verifier.
type VerifyOptions struct {
	// Algorithms allowed (default: all supported); each key still only
	// accepts its own algorithm.
	Algorithms []string
	// Issuer, when set, must equal "iss". Required with a *RemoteJWKS.
	Issuer string
	// Audience, when set, must be in "aud". Required with a *RemoteJWKS:
	// an identity provider signs tokens for all of its clients with the
	// same keys, so without it a token issued to another app is accepted.
	Audience string
	// InsecureSkipAudience accepts any "aud" with a *RemoteJWKS.
	// Dangerous: tokens the provider issued to other apps pass.
	InsecureSkipAudience bool
	// InsecureSkipIssuer accepts any "iss" with a *RemoteJWKS.
	// Dangerous: tokens from other tenants of a multi-tenant provider pass.
	InsecureSkipIssuer bool
	// Leeway tolerates clock skew (default 30s).
	Leeway time.Duration
	// AllowMissingExpiry accepts tokens without "exp" (off by default).
	AllowMissingExpiry bool
	// Now is the clock (default time.Now).
	Now func() time.Time
}

// Verifier checks tokens against a key set and claim rules.
type Verifier struct {
	keys KeySet
	opts VerifyOptions
	err  error // misconfiguration: every Verify fails with it
}

// NewVerifier returns a Verifier.
func NewVerifier(keys KeySet, opts VerifyOptions) *Verifier {
	if len(opts.Algorithms) == 0 {
		opts.Algorithms = []string{AlgHS256, AlgRS256, AlgES256, AlgEdDSA}
	}

	if opts.Leeway == 0 {
		opts.Leeway = defaultLeeway
	}

	if opts.Now == nil {
		opts.Now = time.Now
	}

	v := &Verifier{keys: keys, opts: opts}

	if _, remote := keys.(*RemoteJWKS); remote {
		switch {
		case opts.Audience == "" && !opts.InsecureSkipAudience:
			v.err = invalid("a remote key set needs VerifyOptions.Audience")
		case opts.Issuer == "" && !opts.InsecureSkipIssuer:
			v.err = invalid("a remote key set needs VerifyOptions.Issuer")
		}
	}

	return v
}

// Err reports a misconfiguration that makes every Verify fail (a
// *RemoteJWKS without Audience or Issuer); check it at boot.
func (v *Verifier) Err() error { return v.err }

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, args...))
}

// Verify checks the signature and claims of token and returns its claims.
// Every failure wraps ErrInvalid.
func (v *Verifier) Verify(ctx context.Context, token string) (Claims, error) {
	if v.err != nil {
		return Claims{}, v.err
	}

	if len(token) > maxTokenSize {
		return Claims{}, invalid("token larger than %d bytes", maxTokenSize)
	}

	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return Claims{}, invalid("not a compact JWS")
	}

	hb, err := b64.DecodeString(parts[0])
	if err != nil {
		return Claims{}, invalid("header: %v", err)
	}

	var h header
	if err := decodeStrict(hb, &h); err != nil {
		return Claims{}, invalid("header: %v", err)
	}

	if !slices.Contains(v.opts.Algorithms, h.Alg) {
		return Claims{}, invalid("algorithm %q not allowed", h.Alg)
	}

	k, err := v.keys.Key(ctx, h.Kid)
	if err != nil {
		return Claims{}, invalid("%v", err)
	}

	if k.Alg != h.Alg || k.check(false) != nil {
		return Claims{}, invalid("key %q does not accept %q", k.ID, h.Alg)
	}

	sig, err := b64.DecodeString(parts[2])
	if err != nil || !k.verify([]byte(parts[0]+"."+parts[1]), sig) {
		return Claims{}, invalid("bad signature")
	}

	pb, err := b64.DecodeString(parts[1])
	if err != nil {
		return Claims{}, invalid("payload: %v", err)
	}

	var m map[string]any
	if err := decodeStrict(pb, &m); err != nil {
		return Claims{}, invalid("payload: %v", err)
	}

	c, err := claimsFrom(m)
	if err != nil {
		return Claims{}, invalid("%v", err)
	}

	return c, v.checkClaims(c)
}

func (v *Verifier) checkClaims(c Claims) error {
	now := v.opts.Now()
	leeway := v.opts.Leeway

	switch {
	case c.ExpiresAt.IsZero() && !v.opts.AllowMissingExpiry:
		return invalid("no exp claim")
	case !c.ExpiresAt.IsZero() && !now.Before(c.ExpiresAt.Add(leeway)):
		return invalid("expired")
	case !c.NotBefore.IsZero() && now.Add(leeway).Before(c.NotBefore):
		return invalid("not valid yet")
	case !c.IssuedAt.IsZero() && now.Add(leeway).Before(c.IssuedAt):
		return invalid("issued in the future")
	case v.opts.Issuer != "" && c.Issuer != v.opts.Issuer:
		return invalid("issuer %q", c.Issuer)
	case v.opts.Audience != "" && !slices.Contains(c.Audience, v.opts.Audience):
		return invalid("audience %v", c.Audience)
	}

	return nil
}

// decodeStrict decodes a JSON object, rejecting duplicate top-level
// members (RFC 7515 §5.2) and keeping numbers exact.
func decodeStrict(b []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()

	tok, err := dec.Token()
	if err != nil {
		return err
	}

	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return errors.New("not a JSON object")
	}

	seen := map[string]bool{}

	for dec.More() {
		key, err := dec.Token()
		if err != nil {
			return err
		}

		name, _ := key.(string)
		if seen[name] {
			return fmt.Errorf("duplicate member %q", name)
		}

		seen[name] = true

		var skip json.RawMessage
		if err := dec.Decode(&skip); err != nil {
			return err
		}
	}

	dec = json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()

	return dec.Decode(v)
}

func elliptic256() elliptic.Curve { return elliptic.P256() }
