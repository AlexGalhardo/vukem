// Package webauthntest is a software passkey for tests of WebAuthn
// endpoints: it answers the options JSON a server sends to the browser
// with the JSON credential.toJSON() would post back, so registration and
// sign-in can run end to end without a browser.
//
// It creates ES256 credentials with attestation "none", user presence and
// user verification set, and a sign count that grows by one per sign-in.
// It is not an authenticator for production use.
package webauthntest

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"slices"
)

const (
	credIDLen = 16

	flagUP = 0x01
	flagUV = 0x04
	flagAT = 0x40
)

var b64 = base64.RawURLEncoding

// ErrNoCredential means Get found no credential for the RP ID (and the
// allow list) of the options.
var ErrNoCredential = errors.New("webauthntest: no matching credential")

// Authenticator is a software passkey store bound to one origin. It is
// not safe for concurrent use.
type Authenticator struct {
	origin string
	creds  []*credential
}

type credential struct {
	id         []byte
	key        *ecdsa.PrivateKey
	rpID       string
	userHandle []byte
	signCount  uint32
}

// New returns an Authenticator whose ceremonies claim to come from origin,
// e.g. "http://localhost:3000".
func New(origin string) *Authenticator { return &Authenticator{origin: origin} }

// Create answers a PublicKeyCredentialCreationOptionsJSON with the
// RegistrationResponseJSON of a new credential for its rp.id and user.id.
func (a *Authenticator) Create(options []byte) ([]byte, error) {
	var o struct {
		Challenge string `json:"challenge"`
		RP        struct {
			ID string `json:"id"`
		} `json:"rp"`
		User struct {
			ID string `json:"id"`
		} `json:"user"`
	}
	if err := json.Unmarshal(options, &o); err != nil {
		return nil, err
	}

	handle, err := b64.DecodeString(o.User.ID)
	if err != nil {
		return nil, err
	}

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}

	c := &credential{id: make([]byte, credIDLen), key: key, rpID: o.RP.ID, userHandle: handle}
	if _, err := rand.Read(c.id); err != nil {
		return nil, err
	}

	raw, err := key.PublicKey.Bytes()
	if err != nil {
		return nil, err
	}

	cose := encode(cmap{{1, 2}, {3, -7}, {-1, 1}, {-2, raw[1:33]}, {-3, raw[33:]}})

	ad := c.authData(flagUP | flagUV | flagAT)
	ad = append(ad, make([]byte, 16)...) // AAGUID
	ad = binary.BigEndian.AppendUint16(ad, credIDLen)
	ad = append(ad, c.id...)
	ad = append(ad, cose...)

	att := encode(cmap{{"fmt", "none"}, {"attStmt", cmap{}}, {"authData", ad}})

	cdj, err := a.clientData("webauthn.create", o.Challenge)
	if err != nil {
		return nil, err
	}

	a.creds = append(a.creds, c)
	id := b64.EncodeToString(c.id)

	return json.Marshal(map[string]any{
		"id": id, "rawId": id, "type": "public-key",
		"response": map[string]any{
			"clientDataJSON":    b64.EncodeToString(cdj),
			"attestationObject": b64.EncodeToString(att),
			"transports":        []string{"internal"},
		},
	})
}

// Get answers a PublicKeyCredentialRequestOptionsJSON with the
// AuthenticationResponseJSON of the newest credential for its rpId that
// the allow list (when not empty) names.
func (a *Authenticator) Get(options []byte) ([]byte, error) {
	var o struct {
		Challenge string `json:"challenge"`
		RPID      string `json:"rpId"`
		Allow     []struct {
			ID string `json:"id"`
		} `json:"allowCredentials"`
	}
	if err := json.Unmarshal(options, &o); err != nil {
		return nil, err
	}

	allow := make([]string, len(o.Allow))
	for i, d := range o.Allow {
		allow[i] = d.ID
	}

	var c *credential

	for _, cand := range slices.Backward(a.creds) {
		if cand.rpID == o.RPID && (len(allow) == 0 || slices.Contains(allow, b64.EncodeToString(cand.id))) {
			c = cand
			break
		}
	}

	if c == nil {
		return nil, ErrNoCredential
	}

	c.signCount++
	ad := c.authData(flagUP | flagUV)

	cdj, err := a.clientData("webauthn.get", o.Challenge)
	if err != nil {
		return nil, err
	}

	h := sha256.Sum256(cdj)
	digest := sha256.Sum256(append(append([]byte(nil), ad...), h[:]...))

	sig, err := ecdsa.SignASN1(rand.Reader, c.key, digest[:])
	if err != nil {
		return nil, err
	}

	id := b64.EncodeToString(c.id)

	return json.Marshal(map[string]any{
		"id": id, "rawId": id, "type": "public-key",
		"response": map[string]any{
			"clientDataJSON":    b64.EncodeToString(cdj),
			"authenticatorData": b64.EncodeToString(ad),
			"signature":         b64.EncodeToString(sig),
			"userHandle":        b64.EncodeToString(c.userHandle),
		},
	})
}

func (c *credential) authData(flags byte) []byte {
	h := sha256.Sum256([]byte(c.rpID))
	out := append(h[:], flags)

	return binary.BigEndian.AppendUint32(out, c.signCount)
}

func (a *Authenticator) clientData(typ, challenge string) ([]byte, error) {
	return json.Marshal(clientData{Type: typ, Challenge: challenge, Origin: a.origin})
}

type clientData struct {
	Type        string `json:"type"`
	Challenge   string `json:"challenge"`
	Origin      string `json:"origin"`
	CrossOrigin bool   `json:"crossOrigin"`
}

// cmap is an ordered CBOR map.
type cmap []struct{ k, v any }

// encode is the CBOR subset authenticator data needs.
func encode(v any) []byte {
	head := func(major byte, n uint64) []byte {
		switch {
		case n < 24:
			return []byte{major<<5 | byte(n)}
		case n <= 0xff:
			return []byte{major<<5 | 24, byte(n)}
		default:
			// Keys and authenticator data stay under 64 KiB: two length bytes.
			return append([]byte{major<<5 | 25}, binary.BigEndian.AppendUint64(nil, n)[6:]...)
		}
	}

	switch x := v.(type) {
	case int:
		if x < 0 {
			return head(1, uint64(-1-x))
		}

		return head(0, uint64(x))
	case []byte:
		return append(head(2, uint64(len(x))), x...)
	case string:
		return append(head(3, uint64(len(x))), x...)
	case cmap:
		out := head(5, uint64(len(x)))
		for _, e := range x {
			out = append(out, encode(e.k)...)
			out = append(out, encode(e.v)...)
		}

		return out
	}

	panic("webauthntest: unsupported CBOR value")
}
