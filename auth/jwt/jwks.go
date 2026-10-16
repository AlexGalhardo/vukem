package jwt

import (
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"sync"
	"time"
)

// JWK is a public JSON Web Key (RFC 7517).
type JWK struct {
	Kty string `json:"kty"`
	Kid string `json:"kid,omitempty"`
	Alg string `json:"alg,omitempty"`
	Use string `json:"use,omitempty"`
	N   string `json:"n,omitempty"`
	E   string `json:"e,omitempty"`
	Crv string `json:"crv,omitempty"`
	X   string `json:"x,omitempty"`
	Y   string `json:"y,omitempty"`
}

// JWKS is a JSON Web Key Set.
type JWKS struct {
	Keys []JWK `json:"keys"`
}

// PublicJWKS returns the public part of keys as a JWKS document, to serve
// at /.well-known/jwks.json. HMAC keys are never published.
func PublicJWKS(keys ...Key) (JWKS, error) {
	set := JWKS{Keys: []JWK{}}

	for _, k := range keys {
		j := JWK{Kid: k.ID, Alg: k.Alg, Use: "sig"}

		switch pub := k.Public.(type) {
		case *rsa.PublicKey:
			j.Kty, j.N, j.E = "RSA", b64.EncodeToString(
				pub.N.Bytes(),
			), b64.EncodeToString(
				big.NewInt(int64(pub.E)).Bytes(),
			)
		case *ecdsa.PublicKey:
			ecdh, err := pub.ECDH()
			if err != nil {
				return JWKS{}, fmt.Errorf("jwt: key %q: %w", k.ID, err)
			}

			raw := ecdh.Bytes() // 0x04 || X || Y
			j.Kty, j.Crv, j.X, j.Y = "EC", "P-256", b64.EncodeToString(raw[1:33]), b64.EncodeToString(raw[33:])
		case ed25519.PublicKey:
			j.Kty, j.Crv, j.X = "OKP", "Ed25519", b64.EncodeToString(pub)
		default:
			continue // HMAC secrets stay private
		}

		set.Keys = append(set.Keys, j)
	}

	return set, nil
}

// Key converts j to a verification Key; symmetric keys are refused.
func (j JWK) Key() (Key, error) {
	switch j.Kty {
	case "RSA":
		n, err1 := b64.DecodeString(j.N)
		e, err2 := b64.DecodeString(j.E)

		if err := errors.Join(err1, err2); err != nil || len(e) > 4 {
			return Key{}, fmt.Errorf("jwt: RSA key %q: bad n/e", j.Kid)
		}

		pub := &rsa.PublicKey{N: new(big.Int).SetBytes(n), E: int(new(big.Int).SetBytes(e).Int64())}
		k := Key{ID: j.Kid, Alg: AlgRS256, Public: pub}

		return k, k.check(false)
	case "EC":
		if j.Crv != "P-256" {
			return Key{}, fmt.Errorf("jwt: EC key %q: curve %q not supported", j.Kid, j.Crv)
		}

		x, err1 := b64.DecodeString(j.X)
		y, err2 := b64.DecodeString(j.Y)

		if err := errors.Join(err1, err2); err != nil || len(x) != 32 || len(y) != 32 {
			return Key{}, fmt.Errorf("jwt: EC key %q: bad coordinates", j.Kid)
		}

		raw := append(append([]byte{4}, x...), y...)

		pub, err := ecdsa.ParseUncompressedPublicKey(elliptic.P256(), raw)
		if err != nil {
			return Key{}, fmt.Errorf("jwt: EC key %q: %w", j.Kid, err)
		}

		return Key{ID: j.Kid, Alg: AlgES256, Public: pub}, nil
	case "OKP":
		x, err := b64.DecodeString(j.X)
		if err != nil || j.Crv != "Ed25519" || len(x) != ed25519.PublicKeySize {
			return Key{}, fmt.Errorf("jwt: OKP key %q: bad Ed25519 key", j.Kid)
		}

		return Key{ID: j.Kid, Alg: AlgEdDSA, Public: ed25519.PublicKey(x)}, nil
	}

	return Key{}, fmt.Errorf("jwt: key %q: type %q not accepted", j.Kid, j.Kty)
}

// RemoteJWKS fetches an identity provider's key set and refreshes it when
// a token names an unknown key id (rotation), at most once per MinRefresh.
type RemoteJWKS struct {
	URL string
	// Client fetches the set (default: 10s timeout).
	Client *http.Client
	// MaxAge refreshes the cache after this long (default 1h).
	MaxAge time.Duration
	// MinRefresh limits refetches for unknown key ids (default 1m), so
	// tokens with random kids cannot hammer the provider.
	MinRefresh time.Duration
	// Now is the clock (default time.Now).
	Now func() time.Time

	mu      sync.Mutex
	keys    map[string]Key
	fetched time.Time
}

// Key implements KeySet.
func (r *RemoteJWKS) Key(ctx context.Context, kid string) (Key, error) {
	now := time.Now
	if r.Now != nil {
		now = r.Now
	}

	maxAge, minRefresh := r.MaxAge, r.MinRefresh
	if maxAge == 0 {
		maxAge = time.Hour
	}

	if minRefresh == 0 {
		minRefresh = time.Minute
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	k, ok := r.lookup(kid)
	stale := now().Sub(r.fetched) > maxAge
	canRefresh := r.fetched.IsZero() || now().Sub(r.fetched) >= minRefresh

	if (!ok || stale) && canRefresh {
		if err := r.fetch(ctx); err != nil && !ok {
			return Key{}, err
		}

		r.fetched = now()
		k, ok = r.lookup(kid)
	}

	if !ok {
		return Key{}, fmt.Errorf("unknown key id %q", kid)
	}

	return k, nil
}

func (r *RemoteJWKS) lookup(kid string) (Key, bool) {
	if kid == "" && len(r.keys) == 1 {
		for _, k := range r.keys {
			return k, true
		}
	}

	k, ok := r.keys[kid]

	return k, ok
}

func (r *RemoteJWKS) fetch(ctx context.Context) error {
	client := r.Client
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.URL, http.NoBody)
	if err != nil {
		return err
	}

	res, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("jwt: fetch JWKS: %w", err)
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("jwt: fetch JWKS: %s", res.Status)
	}

	var set JWKS
	if err := json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&set); err != nil {
		return fmt.Errorf("jwt: decode JWKS: %w", err)
	}

	keys := map[string]Key{}

	for i := range set.Keys {
		j := &set.Keys[i]
		if j.Use != "" && j.Use != "sig" {
			continue
		}

		if k, err := j.Key(); err == nil {
			keys[k.ID] = k
		}
	}

	r.keys = keys

	return nil
}
