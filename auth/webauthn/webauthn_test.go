package webauthn

import (
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"math/big"
	"testing"
)

// kv and cmap give the test encoder an ordered CBOR map.
type kv struct {
	k, v any
}

type cmap []kv

// enc is a minimal CBOR encoder for building authenticator responses.
func enc(v any) []byte {
	head := func(major byte, n uint64) []byte {
		switch {
		case n < 24:
			return []byte{major<<5 | byte(n)}
		case n <= 0xff:
			return []byte{major<<5 | 24, byte(n)}
		case n <= 0xffff:
			return binary.BigEndian.AppendUint16([]byte{major<<5 | 25}, uint16(n))
		default:
			return binary.BigEndian.AppendUint32([]byte{major<<5 | 26}, uint32(n))
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
	case bool:
		if x {
			return []byte{0xf5}
		}

		return []byte{0xf4}
	case cmap:
		out := head(5, uint64(len(x)))
		for _, e := range x {
			out = append(out, enc(e.k)...)
			out = append(out, enc(e.v)...)
		}

		return out
	default:
		panic("enc: unsupported type")
	}
}

var b64t = base64.RawURLEncoding

func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}

	return b
}

const (
	testRPID   = "example.com"
	testOrigin = "https://example.com"
)

func testRP() RelyingParty {
	return RelyingParty{ID: testRPID, Name: "Example", Origins: []string{testOrigin}}
}

// authenticator is a software passkey.
type authenticator struct {
	signer    crypto.Signer
	hash      crypto.Hash
	alg       int
	credID    []byte
	signCount uint32
	flags     byte
	rpID      string
}

func newAuthenticator(t *testing.T, alg int) *authenticator {
	t.Helper()

	a := &authenticator{alg: alg, credID: NewUserHandle(), flags: flagUP | flagUV, rpID: testRPID, hash: crypto.SHA256}

	var err error

	switch alg {
	case int(AlgES256):
		a.signer, err = ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	case int(AlgEdDSA):
		_, a.signer, err = ed25519.GenerateKey(rand.Reader)
		a.hash = 0
	case int(AlgRS256):
		a.signer, err = rsa.GenerateKey(rand.Reader, 2048)
	}

	if err != nil {
		t.Fatal(err)
	}

	return a
}

func (a *authenticator) coseKey() []byte {
	switch pub := a.signer.Public().(type) {
	case *ecdsa.PublicKey:
		raw, _ := pub.Bytes()

		return enc(cmap{{1, 2}, {3, -7}, {-1, 1}, {-2, raw[1:33]}, {-3, raw[33:]}})
	case ed25519.PublicKey:
		return enc(cmap{{1, 1}, {3, -8}, {-1, 6}, {-2, []byte(pub)}})
	case *rsa.PublicKey:
		return enc(cmap{{1, 3}, {3, -257}, {-1, pub.N.Bytes()}, {-2, big.NewInt(int64(pub.E)).Bytes()}})
	}

	panic("unreachable")
}

func (a *authenticator) authData(attested bool, key []byte) []byte {
	h := sha256.Sum256([]byte(a.rpID))
	flags := a.flags

	if attested {
		flags |= flagAT
	}

	out := append(h[:], flags)
	out = binary.BigEndian.AppendUint32(out, a.signCount)

	if attested {
		out = append(out, make([]byte, 16)...) // AAGUID
		out = binary.BigEndian.AppendUint16(out, uint16(len(a.credID)))
		out = append(out, a.credID...)
		out = append(out, key...)
	}

	return out
}

func clientDataJSON(typ, challenge, origin string) []byte {
	b := mustJSON(map[string]any{
		"type": typ, "challenge": challenge, "origin": origin, "crossOrigin": false,
	})

	return b
}

// register returns a RegistrationResponseJSON; mutate edits the parts
// before encoding.
func (a *authenticator) register(challenge string, mutate func(*regParts)) []byte {
	p := &regParts{
		fmt:     "none",
		stmt:    cmap{},
		cdj:     clientDataJSON("webauthn.create", challenge, testOrigin),
		key:     a.coseKey(),
		rawID:   a.credID,
		typ:     "public-key",
		authFor: a,
	}
	if mutate != nil {
		mutate(p)
	}

	ad := p.authData
	if ad == nil {
		ad = a.authData(true, p.key)
	}

	att := enc(cmap{{"fmt", p.fmt}, {"attStmt", p.stmt}, {"authData", ad}})
	id := b64t.EncodeToString(p.rawID)
	body := mustJSON(map[string]any{
		"id": id, "rawId": id, "type": p.typ,
		"response": map[string]any{
			"clientDataJSON":    b64t.EncodeToString(p.cdj),
			"attestationObject": b64t.EncodeToString(att),
			"transports":        []string{"internal"},
		},
	})

	return body
}

type regParts struct {
	fmt      string
	stmt     cmap
	cdj      []byte
	key      []byte
	rawID    []byte
	typ      string
	authData []byte
	authFor  *authenticator
}

type assertParts struct {
	cdj        []byte
	authData   []byte
	rawID      []byte
	userHandle []byte
	tamper     bool
}

// assert signs an assertion and returns it parsed.
func (a *authenticator) assert(t *testing.T, challenge string, userHandle []byte, mutate func(*assertParts)) Assertion {
	t.Helper()

	a.signCount++
	p := &assertParts{
		cdj:        clientDataJSON("webauthn.get", challenge, testOrigin),
		authData:   a.authData(false, nil),
		rawID:      a.credID,
		userHandle: userHandle,
	}
	if mutate != nil {
		mutate(p)
	}

	h := sha256.Sum256(p.cdj)
	msg := append(append([]byte(nil), p.authData...), h[:]...)
	digest := msg

	if a.hash != 0 {
		d := sha256.Sum256(msg)
		digest = d[:]
	}

	sig, err := a.signer.Sign(rand.Reader, digest, a.hash)
	if err != nil {
		t.Fatal(err)
	}

	if p.tamper {
		sig[len(sig)-1] ^= 1
	}

	id := b64t.EncodeToString(p.rawID)
	body := mustJSON(map[string]any{
		"id": id, "rawId": id, "type": "public-key",
		"response": map[string]any{
			"clientDataJSON":    b64t.EncodeToString(p.cdj),
			"authenticatorData": b64t.EncodeToString(p.authData),
			"signature":         b64t.EncodeToString(sig),
			"userHandle":        b64t.EncodeToString(p.userHandle),
		},
	})

	got, err := ParseAssertion(body)
	if err != nil {
		t.Fatalf("ParseAssertion() = %v", err)
	}

	return got
}

func registered(t *testing.T, alg int) (*authenticator, Credential) {
	t.Helper()

	a := newAuthenticator(t, alg)
	challenge := NewChallenge()
	user := User{Handle: NewUserHandle(), Name: "ada@example.com", DisplayName: "Ada"}

	cred, err := testRP().VerifyRegistration(challenge, user, a.register(challenge, nil))
	if err != nil {
		t.Fatalf("VerifyRegistration() = %v, want nil", err)
	}

	return a, cred
}

func TestRegisterAndAssert(t *testing.T) {
	t.Parallel()

	for _, alg := range []int{int(AlgES256), int(AlgEdDSA), int(AlgRS256)} {
		t.Run(big.NewInt(int64(alg)).String(), func(t *testing.T) {
			t.Parallel()

			a, cred := registered(t, alg)
			if cred.Algorithm != int64(alg) || !bytes.Equal(cred.ID, a.credID) ||
				len(cred.UserHandle) != ChallengeSize {
				t.Fatalf("VerifyRegistration() = %+v", cred)
			}

			for range 2 {
				challenge := NewChallenge()

				next, err := testRP().VerifyAssertion(challenge, cred, a.assert(t, challenge, cred.UserHandle, nil))
				if err != nil {
					t.Fatalf("VerifyAssertion() = %v, want nil", err)
				}

				if next.SignCount != a.signCount {
					t.Fatalf("VerifyAssertion() sign count = %d, want %d", next.SignCount, a.signCount)
				}

				cred = next
			}
		})
	}
}

func TestAssertZeroSignCount(t *testing.T) {
	t.Parallel()

	a, cred := registered(t, int(AlgES256))
	a.signCount = 0
	cred.SignCount = 0

	// Synced passkeys never count: 0 then 0 is not a clone.
	challenge := NewChallenge()
	a.signCount = ^uint32(0) // assert increments it back to 0

	if _, err := testRP().VerifyAssertion(challenge, cred, a.assert(t, challenge, nil, nil)); err != nil {
		t.Fatalf("VerifyAssertion() with sign count 0 = %v, want nil", err)
	}
}

func TestSecurity_A07_WebAuthnAssertionRejected(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		rp     func(*RelyingParty)
		cred   func(*Credential)
		mutate func(a *authenticator, p *assertParts, challenge string)
		empty  bool // verify against an empty stored challenge
		want   error
	}{
		{
			name: "replayed challenge",
			mutate: func(_ *authenticator, p *assertParts, _ string) {
				p.cdj = clientDataJSON("webauthn.get", NewChallenge(), testOrigin)
			},
			want: ErrChallenge,
		},
		{
			name: "lost challenge",
			mutate: func(_ *authenticator, p *assertParts, _ string) {
				p.cdj = clientDataJSON("webauthn.get", "", testOrigin)
			},
			empty: true,
			want:  ErrChallenge,
		},
		{
			name: "wrong origin",
			mutate: func(_ *authenticator, p *assertParts, c string) {
				p.cdj = clientDataJSON("webauthn.get", c, "https://evil.example.net")
			},
			want: ErrOrigin,
		},
		{
			name: "cross-origin iframe",
			mutate: func(_ *authenticator, p *assertParts, c string) {
				p.cdj = mustJSON(map[string]any{
					"type":        "webauthn.get",
					"challenge":   c,
					"origin":      testOrigin,
					"crossOrigin": true,
				})
			},
			want: ErrOrigin,
		},
		{
			name: "registration client data",
			mutate: func(_ *authenticator, p *assertParts, c string) {
				p.cdj = clientDataJSON("webauthn.create", c, testOrigin)
			},
			want: ErrMalformed,
		},
		{
			name: "wrong RP ID hash",
			mutate: func(a *authenticator, p *assertParts, _ string) {
				a.rpID = "evil.example.net"
				p.authData = a.authData(false, nil)
			},
			want: ErrRPID,
		},
		{
			name: "sign count regression",
			cred: func(c *Credential) { c.SignCount = 50 },
			want: ErrSignCount,
		},
		{
			name: "foreign credential id",
			mutate: func(_ *authenticator, p *assertParts, _ string) {
				p.rawID = NewUserHandle()
			},
			want: ErrCredential,
		},
		{
			name: "credential of another user",
			mutate: func(_ *authenticator, p *assertParts, _ string) {
				p.userHandle = NewUserHandle()
			},
			want: ErrCredential,
		},
		{
			name:   "tampered signature",
			mutate: func(_ *authenticator, p *assertParts, _ string) { p.tamper = true },
			want:   ErrSignature,
		},
		{
			name: "no user verification",
			mutate: func(a *authenticator, p *assertParts, _ string) {
				a.flags = flagUP
				p.authData = a.authData(false, nil)
			},
			want: ErrUserVerification,
		},
		{
			name: "no user presence",
			rp:   func(rp *RelyingParty) { rp.InsecureSkipUserVerification = true },
			mutate: func(a *authenticator, p *assertParts, _ string) {
				a.flags = flagUV
				p.authData = a.authData(false, nil)
			},
			want: ErrUserPresence,
		},
		{
			name: "backup eligibility changed",
			mutate: func(a *authenticator, p *assertParts, _ string) {
				a.flags |= flagBE
				p.authData = a.authData(false, nil)
			},
			want: ErrCredential,
		},
		{
			name: "trailing authenticator data",
			mutate: func(_ *authenticator, p *assertParts, _ string) {
				p.authData = append(p.authData, 0)
			},
			want: ErrMalformed,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			a, cred := registered(t, int(AlgES256))
			rp := testRP()

			if tt.rp != nil {
				tt.rp(&rp)
			}

			if tt.cred != nil {
				tt.cred(&cred)
			}

			challenge := NewChallenge()

			var mutate func(*assertParts)
			if tt.mutate != nil {
				mutate = func(p *assertParts) { tt.mutate(a, p, challenge) }
			}

			assertion := a.assert(t, challenge, cred.UserHandle, mutate)
			if tt.empty {
				challenge = ""
			}

			_, err := rp.VerifyAssertion(challenge, cred, assertion)
			if !errors.Is(err, tt.want) {
				t.Fatalf("VerifyAssertion() = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestSecurity_A07_WebAuthnRegistrationRejected(t *testing.T) {
	t.Parallel()

	p384 := func(t *testing.T) []byte {
		t.Helper()

		k, err := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
		if err != nil {
			t.Fatal(err)
		}

		raw, _ := k.PublicKey.Bytes()

		return enc(cmap{{1, 2}, {3, -7}, {-1, 2}, {-2, raw[1:49]}, {-3, raw[49:]}})
	}

	tests := []struct {
		name   string
		mutate func(t *testing.T, p *regParts, challenge string)
		want   error
	}{
		{
			name: "replayed challenge",
			mutate: func(_ *testing.T, p *regParts, _ string) {
				p.cdj = clientDataJSON("webauthn.create", NewChallenge(), testOrigin)
			},
			want: ErrChallenge,
		},
		{
			name: "wrong origin",
			mutate: func(_ *testing.T, p *regParts, c string) {
				p.cdj = clientDataJSON("webauthn.create", c, "http://example.com")
			},
			want: ErrOrigin,
		},
		{
			name: "assertion client data",
			mutate: func(_ *testing.T, p *regParts, c string) {
				p.cdj = clientDataJSON("webauthn.get", c, testOrigin)
			},
			want: ErrMalformed,
		},
		{
			name: "wrong RP ID hash",
			mutate: func(_ *testing.T, p *regParts, _ string) {
				p.authFor.rpID = "evil.example.net"
				p.authData = p.authFor.authData(true, p.key)
			},
			want: ErrRPID,
		},
		{
			name: "packed attestation",
			mutate: func(_ *testing.T, p *regParts, _ string) {
				p.fmt = "packed"
				p.stmt = cmap{{"alg", -7}, {"sig", []byte{1}}}
			},
			want: ErrUnsupportedAttestation,
		},
		{
			name: "raw id differs from attested id",
			mutate: func(_ *testing.T, p *regParts, _ string) {
				p.rawID = NewUserHandle()
			},
			want: ErrCredential,
		},
		{
			name: "no user verification",
			mutate: func(_ *testing.T, p *regParts, _ string) {
				p.authFor.flags = flagUP
				p.authData = p.authFor.authData(true, p.key)
			},
			want: ErrUserVerification,
		},
		{
			name: "P-384 key",
			mutate: func(t *testing.T, p *regParts, _ string) {
				t.Helper()

				p.key = p384(t)
			},
			want: ErrUnsupportedKey,
		},
		{
			name: "algorithm does not match the key type",
			mutate: func(_ *testing.T, p *regParts, _ string) {
				p.key = enc(cmap{{1, 2}, {3, -8}, {-1, 1}, {-2, make([]byte, 32)}, {-3, make([]byte, 32)}})
			},
			want: ErrUnsupportedKey,
		},
		{
			name: "point not on the curve",
			mutate: func(_ *testing.T, p *regParts, _ string) {
				p.key = enc(cmap{{1, 2}, {3, -7}, {-1, 1}, {-2, make([]byte, 32)}, {-3, make([]byte, 32)}})
			},
			want: ErrUnsupportedKey,
		},
		{
			name: "RSA 1024",
			mutate: func(t *testing.T, p *regParts, _ string) {
				t.Helper()

				k, err := rsa.GenerateKey(
					rand.Reader,
					1024,
				)
				if err != nil {
					t.Fatal(err)
				}

				p.key = enc(cmap{{1, 3}, {3, -257}, {-1, k.N.Bytes()}, {-2, big.NewInt(int64(k.E)).Bytes()}})
			},
			want: ErrUnsupportedKey,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			a := newAuthenticator(t, int(AlgES256))
			challenge := NewChallenge()
			body := a.register(challenge, func(p *regParts) { tt.mutate(t, p, challenge) })

			_, err := testRP().VerifyRegistration(challenge, User{Handle: NewUserHandle()}, body)
			if !errors.Is(err, tt.want) {
				t.Fatalf("VerifyRegistration() = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestSecurity_A07_WebAuthnRequiresUserVerificationByDefault(t *testing.T) {
	t.Parallel()

	rp := testRP()
	reg := rp.RegistrationOptions(User{Handle: []byte{1}}, NewChallenge(), nil)
	req := rp.AssertionOptions(NewChallenge(), nil)

	if reg.AuthenticatorSelection.UserVerification != "required" || req.UserVerification != "required" ||
		reg.Attestation != "none" || reg.AuthenticatorSelection.ResidentKey != "required" {
		t.Fatalf("options = %+v / %+v, want user verification and resident key required, attestation none", reg, req)
	}
}

func TestSecurity_A05_WebAuthnRelyingPartyValidate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		id      string
		origins []string
		wantErr bool
	}{
		{"https origin", "example.com", []string{"https://example.com"}, false},
		{"subdomain origin", "example.com", []string{"https://app.example.com"}, false},
		{"localhost over http", "localhost", []string{"http://localhost:8080"}, false},
		{"empty id", "", []string{"https://example.com"}, true},
		{"id with scheme", "https://example.com", []string{"https://example.com"}, true},
		{"no origins", "example.com", nil, true},
		{"http public origin", "example.com", []string{"http://example.com"}, true},
		{"origin outside the RP ID", "example.com", []string{"https://example.com.evil.net"}, true},
		{"suffix without a dot", "example.com", []string{"https://badexample.com"}, true},
		{"origin with path", "example.com", []string{"https://example.com/login"}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := RelyingParty{ID: tt.id, Origins: tt.origins}.Validate()
			if (err != nil) != tt.wantErr {
				t.Fatalf("Validate() = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestOptionsJSON(t *testing.T) {
	t.Parallel()

	cred := Credential{ID: []byte{0xfb, 0xff}, Transports: []string{"usb"}}
	b, err := json.Marshal(testRP().AssertionOptions("AQID", []Credential{cred}))
	if err != nil {
		t.Fatal(err)
	}

	want := `{"challenge":"AQID","rpId":"example.com","timeout":300000,` +
		`"allowCredentials":[{"type":"public-key","id":"-_8","transports":["usb"]}],"userVerification":"required"}`
	if string(b) != want {
		t.Fatalf("AssertionOptions() JSON = %s, want %s", b, want)
	}
}
