// Package webauthn verifies passkey (WebAuthn Level 3) registration and
// authentication ceremonies for a relying party: it builds the options a
// browser passes to PublicKeyCredential.parseCreationOptionsFromJSON and
// parseRequestOptionsFromJSON, and checks the JSON the browser returns
// from credential.toJSON(). Standard library only: a minimal CBOR decoder,
// COSE keys for ES256, EdDSA (Ed25519) and RS256, and attestation "none"
// (authenticator provenance is not verified, which is what consumer
// passkeys provide).
//
// The package is stateless. The caller stores the challenge server-side
// (e.g. in the session), deletes it before verifying so it is single-use,
// stores the returned Credential and saves the updated sign count after
// every successful assertion.
//
// See https://github.com/AlexGalhardo/vukem/blob/main/docs/authentication.md.
package webauthn

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
)

// ChallengeSize is the number of random bytes in a challenge and a user
// handle.
const ChallengeSize = 32

// minChallengeLen is the shortest challenge accepted for verification:
// 16 random bytes, base64url-encoded.
const minChallengeLen = 22

// DefaultTimeout is the ceremony timeout, in milliseconds, put in the
// options.
const DefaultTimeout = 300_000

// maxCredentialIDLen is the WebAuthn limit on credential ids.
const maxCredentialIDLen = 1023

// maxUserHandleLen is the WebAuthn limit on user handles.
const maxUserHandleLen = 64

// Authenticator data flags.
const (
	flagUP = 0x01
	flagUV = 0x04
	flagBE = 0x08
	flagBS = 0x10
	flagAT = 0x40
	flagED = 0x80
)

// Errors returned by the ceremonies. Every one means "reject the
// ceremony"; none of their text is meant for the client.
var (
	ErrMalformed              = errors.New("webauthn: malformed response")
	ErrChallenge              = errors.New("webauthn: challenge mismatch")
	ErrOrigin                 = errors.New("webauthn: origin not allowed")
	ErrRPID                   = errors.New("webauthn: relying party id hash mismatch")
	ErrUserPresence           = errors.New("webauthn: user presence flag not set")
	ErrUserVerification       = errors.New("webauthn: user verification required")
	ErrUnsupportedAttestation = errors.New("webauthn: only attestation \"none\" is accepted")
	ErrCredential             = errors.New("webauthn: credential does not match")
	ErrSignature              = errors.New("webauthn: invalid signature")
	ErrSignCount              = errors.New("webauthn: sign count did not increase (possible cloned authenticator)")
	ErrConfig                 = errors.New("webauthn: invalid relying party configuration")
)

// RelyingParty is the site credentials are scoped to.
type RelyingParty struct {
	// ID is the RP ID: the site's registrable domain or a parent domain of
	// every origin, e.g. "example.com". Credentials are bound to it.
	ID string
	// Name is shown by the authenticator during registration.
	Name string
	// Origins are the exact origins ceremonies may come from, e.g.
	// "https://example.com". http is accepted only for localhost.
	Origins []string
	// InsecureSkipUserVerification accepts ceremonies where the
	// authenticator did not verify the user (PIN or biometrics). A passkey
	// is then only "something you have"; keep it off when passkeys sign
	// users in without a password.
	InsecureSkipUserVerification bool
}

// Validate reports a configuration that would accept ceremonies from
// origins outside the RP ID or over plain http on a public host.
func (rp RelyingParty) Validate() error {
	if rp.ID == "" || strings.ContainsAny(rp.ID, ":/ ") {
		return fmt.Errorf("%w: ID %q must be a bare domain", ErrConfig, rp.ID)
	}

	if len(rp.Origins) == 0 {
		return fmt.Errorf("%w: no origins", ErrConfig)
	}

	for _, o := range rp.Origins {
		u, err := url.Parse(o)
		if err != nil || u.Host == "" || u.Path != "" || u.RawQuery != "" || u.User != nil {
			return fmt.Errorf("%w: origin %q is not scheme://host[:port]", ErrConfig, o)
		}

		host := u.Hostname()

		if u.Scheme != "https" && (u.Scheme != "http" || host != "localhost") {
			return fmt.Errorf("%w: origin %q must use https (http only for localhost)", ErrConfig, o)
		}

		if host != rp.ID && !strings.HasSuffix(host, "."+rp.ID) {
			return fmt.Errorf("%w: origin %q is not within RP ID %q", ErrConfig, o, rp.ID)
		}
	}

	return nil
}

// NewChallenge returns a challenge for one ceremony: ChallengeSize random
// bytes, base64url-encoded so it can be kept in any session store as is.
func NewChallenge() string {
	return base64.RawURLEncoding.EncodeToString(NewUserHandle())
}

// NewUserHandle returns a random user handle. Use it instead of a database
// id or an email: the handle is stored on the authenticator and must not
// identify the user to anyone else.
func NewUserHandle() []byte {
	b := make([]byte, ChallengeSize)
	_, _ = rand.Read(b) // never fails (crypto/rand)

	return b
}

// User is the account a credential is registered for.
type User struct {
	// Handle is the opaque user handle (NewUserHandle), at most 64 bytes.
	Handle []byte
	// Name is the account identifier shown by the authenticator (an email
	// or username); DisplayName is the human name.
	Name, DisplayName string
}

// Credential is a registered passkey. Store every field; ID is unique
// across users.
type Credential struct {
	ID         []byte
	PublicKey  []byte // COSE_Key
	Algorithm  int64
	SignCount  uint32
	UserHandle []byte
	// BackupEligible never changes for a credential; BackupState reports
	// whether it is currently synced (a "multi-device" passkey).
	BackupEligible, BackupState bool
	Transports                  []string
}

// Descriptor names a credential in the options.
type Descriptor struct {
	Type       string   `json:"type"`
	ID         string   `json:"id"`
	Transports []string `json:"transports,omitempty"`
}

// CreationOptions is PublicKeyCredentialCreationOptionsJSON.
type CreationOptions struct {
	Challenge              string                 `json:"challenge"`
	RP                     RPEntity               `json:"rp"`
	User                   UserEntity             `json:"user"`
	PubKeyCredParams       []CredentialParameter  `json:"pubKeyCredParams"`
	Timeout                int                    `json:"timeout"`
	ExcludeCredentials     []Descriptor           `json:"excludeCredentials"`
	AuthenticatorSelection AuthenticatorSelection `json:"authenticatorSelection"`
	Attestation            string                 `json:"attestation"`
}

// RPEntity is PublicKeyCredentialRpEntity.
type RPEntity struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// UserEntity is PublicKeyCredentialUserEntityJSON.
type UserEntity struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	DisplayName string `json:"displayName"`
}

// CredentialParameter is PublicKeyCredentialParameters.
type CredentialParameter struct {
	Type string `json:"type"`
	Alg  int64  `json:"alg"`
}

// AuthenticatorSelection is AuthenticatorSelectionCriteria.
type AuthenticatorSelection struct {
	ResidentKey        string `json:"residentKey"`
	RequireResidentKey bool   `json:"requireResidentKey"`
	UserVerification   string `json:"userVerification"`
}

// RequestOptions is PublicKeyCredentialRequestOptionsJSON.
type RequestOptions struct {
	Challenge        string       `json:"challenge"`
	RPID             string       `json:"rpId"`
	Timeout          int          `json:"timeout"`
	AllowCredentials []Descriptor `json:"allowCredentials"`
	UserVerification string       `json:"userVerification"`
}

func (rp RelyingParty) userVerification() string {
	if rp.InsecureSkipUserVerification {
		return "preferred"
	}

	return "required"
}

func descriptors(creds []Credential) []Descriptor {
	out := make([]Descriptor, 0, len(creds))
	for _, c := range creds {
		out = append(
			out,
			Descriptor{Type: "public-key", ID: base64.RawURLEncoding.EncodeToString(c.ID), Transports: c.Transports},
		)
	}

	return out
}

// RegistrationOptions returns the options for navigator.credentials.create
// for a discoverable credential (a passkey). exclude lists the user's
// existing credentials so the same authenticator is not registered twice.
func (rp RelyingParty) RegistrationOptions(user User, challenge string, exclude []Credential) CreationOptions {
	return CreationOptions{
		Challenge: challenge,
		RP:        RPEntity{ID: rp.ID, Name: rp.Name},
		User: UserEntity{
			ID:          base64.RawURLEncoding.EncodeToString(user.Handle),
			Name:        user.Name,
			DisplayName: user.DisplayName,
		},
		PubKeyCredParams: []CredentialParameter{
			{Type: "public-key", Alg: AlgES256},
			{Type: "public-key", Alg: AlgEdDSA},
			{Type: "public-key", Alg: AlgRS256},
		},
		Timeout:            DefaultTimeout,
		ExcludeCredentials: descriptors(exclude),
		AuthenticatorSelection: AuthenticatorSelection{
			ResidentKey:        "required",
			RequireResidentKey: true,
			UserVerification:   rp.userVerification(),
		},
		Attestation: "none",
	}
}

// AssertionOptions returns the options for navigator.credentials.get. An
// empty allow list lets the user pick any passkey for this RP (usernameless
// sign-in); ParseAssertion then reports which one was used.
func (rp RelyingParty) AssertionOptions(challenge string, allow []Credential) RequestOptions {
	return RequestOptions{
		Challenge:        challenge,
		RPID:             rp.ID,
		Timeout:          DefaultTimeout,
		AllowCredentials: descriptors(allow),
		UserVerification: rp.userVerification(),
	}
}

type clientData struct {
	Type        string `json:"type"`
	Challenge   string `json:"challenge"`
	Origin      string `json:"origin"`
	CrossOrigin bool   `json:"crossOrigin"`
}

// checkClientData verifies the ceremony type, the challenge and the origin.
// Cross-origin (iframe) ceremonies are rejected.
func (rp RelyingParty) checkClientData(raw []byte, typ, challenge string) error {
	var cd clientData
	if err := json.Unmarshal(raw, &cd); err != nil {
		return fmt.Errorf("%w: clientDataJSON: %w", ErrMalformed, err)
	}

	if cd.Type != typ {
		return fmt.Errorf("%w: clientData type %q, want %q", ErrMalformed, cd.Type, typ)
	}

	// An empty or short expected challenge (a missing session value) never
	// matches, so a lost challenge fails closed.
	if len(challenge) < minChallengeLen || subtle.ConstantTimeCompare([]byte(cd.Challenge), []byte(challenge)) != 1 {
		return ErrChallenge
	}

	if cd.CrossOrigin || !slices.Contains(rp.Origins, cd.Origin) {
		return ErrOrigin
	}

	return nil
}

type authData struct {
	rpIDHash  []byte
	flags     byte
	signCount uint32
	credID    []byte
	publicKey []byte
}

// parseAuthData parses authenticator data; trailing bytes are rejected.
func parseAuthData(raw []byte) (authData, error) {
	const fixed = 37

	if len(raw) < fixed {
		return authData{}, fmt.Errorf("%w: authenticator data too short", ErrMalformed)
	}

	ad := authData{
		rpIDHash:  raw[:32],
		flags:     raw[32],
		signCount: binary.BigEndian.Uint32(raw[33:37]),
	}
	rest := raw[fixed:]

	if ad.flags&flagAT != 0 {
		if len(rest) < 18 {
			return authData{}, fmt.Errorf("%w: attested credential data too short", ErrMalformed)
		}

		n := int(binary.BigEndian.Uint16(rest[16:18]))
		rest = rest[18:]

		if n == 0 || n > maxCredentialIDLen || n > len(rest) {
			return authData{}, fmt.Errorf("%w: credential id length", ErrMalformed)
		}

		ad.credID, rest = rest[:n], rest[n:]

		_, used, err := decodeCBOR(rest)
		if err != nil {
			return authData{}, fmt.Errorf("%w: credential public key: %w", ErrMalformed, err)
		}

		ad.publicKey, rest = rest[:used], rest[used:]
	}

	if ad.flags&flagED != 0 {
		v, used, err := decodeCBOR(rest)
		if _, ok := v.(map[any]any); err != nil || !ok {
			return authData{}, fmt.Errorf("%w: extensions", ErrMalformed)
		}

		rest = rest[used:]
	}

	if len(rest) != 0 {
		return authData{}, fmt.Errorf("%w: trailing authenticator data", ErrMalformed)
	}

	if ad.flags&flagBS != 0 && ad.flags&flagBE == 0 {
		return authData{}, fmt.Errorf("%w: backup state without backup eligibility", ErrMalformed)
	}

	return ad, nil
}

// checkAuthData verifies the RP ID hash and the user presence and
// verification flags.
func (rp RelyingParty) checkAuthData(ad authData) error {
	want := sha256.Sum256([]byte(rp.ID))
	if subtle.ConstantTimeCompare(ad.rpIDHash, want[:]) != 1 {
		return ErrRPID
	}

	if ad.flags&flagUP == 0 {
		return ErrUserPresence
	}

	if ad.flags&flagUV == 0 && !rp.InsecureSkipUserVerification {
		return ErrUserVerification
	}

	return nil
}

type registrationJSON struct {
	ID       string `json:"id"`
	RawID    string `json:"rawId"`
	Type     string `json:"type"`
	Response struct {
		ClientDataJSON    string   `json:"clientDataJSON"`
		AttestationObject string   `json:"attestationObject"`
		Transports        []string `json:"transports"`
	} `json:"response"`
}

// decodeFields base64url-decodes each value or fails on the first error.
func decodeFields(values ...string) ([][]byte, error) {
	out := make([][]byte, len(values))

	for i, v := range values {
		b, err := base64.RawURLEncoding.DecodeString(v)
		if err != nil {
			return nil, fmt.Errorf("%w: base64url field: %w", ErrMalformed, err)
		}

		out[i] = b
	}

	return out, nil
}

// VerifyRegistration checks a RegistrationResponseJSON (the browser's
// credential.toJSON() after navigator.credentials.create) against the
// challenge it was created with and returns the credential to store for
// user. The challenge must already be deleted from storage.
func (rp RelyingParty) VerifyRegistration(challenge string, user User, body []byte) (Credential, error) {
	var r registrationJSON
	if err := json.Unmarshal(body, &r); err != nil {
		return Credential{}, fmt.Errorf("%w: %w", ErrMalformed, err)
	}

	f, err := decodeFields(r.RawID, r.Response.ClientDataJSON, r.Response.AttestationObject)
	if err != nil {
		return Credential{}, err
	}

	rawID, cdj, attObj := f[0], f[1], f[2]

	if r.Type != "public-key" || r.ID != r.RawID {
		return Credential{}, fmt.Errorf("%w: credential type or id", ErrMalformed)
	}

	if len(user.Handle) == 0 || len(user.Handle) > maxUserHandleLen {
		return Credential{}, fmt.Errorf("%w: user handle must be 1 to %d bytes", ErrConfig, maxUserHandleLen)
	}

	if err := rp.checkClientData(cdj, "webauthn.create", challenge); err != nil {
		return Credential{}, err
	}

	ad, err := attestedAuthData(attObj)
	if err != nil {
		return Credential{}, err
	}

	if err := rp.checkAuthData(ad); err != nil {
		return Credential{}, err
	}

	if ad.flags&flagAT == 0 || !bytes.Equal(ad.credID, rawID) {
		return Credential{}, fmt.Errorf("%w: attested credential id", ErrCredential)
	}

	key, err := parseCOSEKey(ad.publicKey)
	if err != nil {
		return Credential{}, err
	}

	return Credential{
		ID:             rawID,
		PublicKey:      ad.publicKey,
		Algorithm:      key.alg,
		SignCount:      ad.signCount,
		UserHandle:     user.Handle,
		BackupEligible: ad.flags&flagBE != 0,
		BackupState:    ad.flags&flagBS != 0,
		Transports:     r.Response.Transports,
	}, nil
}

// attestedAuthData decodes an attestation object of format "none" and
// returns its authenticator data.
func attestedAuthData(attObj []byte) (authData, error) {
	v, used, err := decodeCBOR(attObj)
	if err != nil {
		return authData{}, fmt.Errorf("%w: attestation object: %w", ErrMalformed, err)
	}

	m, ok := v.(map[any]any)
	if !ok || used != len(attObj) {
		return authData{}, fmt.Errorf("%w: attestation object", ErrMalformed)
	}

	stmt, _ := m["attStmt"].(map[any]any)
	if m["fmt"] != "none" || stmt == nil || len(stmt) != 0 {
		return authData{}, ErrUnsupportedAttestation
	}

	raw, ok := m["authData"].([]byte)
	if !ok {
		return authData{}, fmt.Errorf("%w: authData", ErrMalformed)
	}

	return parseAuthData(raw)
}

// Assertion is a parsed AuthenticationResponseJSON. Look up the credential
// by CredentialID, then call VerifyAssertion.
type Assertion struct {
	CredentialID []byte
	// UserHandle is set for discoverable credentials; when present it must
	// match the stored credential's user (VerifyAssertion checks it).
	UserHandle        []byte
	clientDataJSON    []byte
	authenticatorData []byte
	signature         []byte
}

type assertionJSON struct {
	ID       string `json:"id"`
	RawID    string `json:"rawId"`
	Type     string `json:"type"`
	Response struct {
		ClientDataJSON    string `json:"clientDataJSON"`
		AuthenticatorData string `json:"authenticatorData"`
		Signature         string `json:"signature"`
		UserHandle        string `json:"userHandle"`
	} `json:"response"`
}

// ParseAssertion decodes an AuthenticationResponseJSON (credential.toJSON()
// after navigator.credentials.get). It verifies nothing.
func ParseAssertion(body []byte) (Assertion, error) {
	var r assertionJSON
	if err := json.Unmarshal(body, &r); err != nil {
		return Assertion{}, fmt.Errorf("%w: %w", ErrMalformed, err)
	}

	f, err := decodeFields(r.RawID, r.Response.ClientDataJSON, r.Response.AuthenticatorData,
		r.Response.Signature, r.Response.UserHandle)
	if err != nil {
		return Assertion{}, err
	}

	if r.Type != "public-key" || r.ID != r.RawID || len(f[0]) == 0 || len(f[0]) > maxCredentialIDLen {
		return Assertion{}, fmt.Errorf("%w: credential type or id", ErrMalformed)
	}

	return Assertion{
		CredentialID:      f[0],
		UserHandle:        f[4],
		clientDataJSON:    f[1],
		authenticatorData: f[2],
		signature:         f[3],
	}, nil
}

// VerifyAssertion checks an assertion made with cred against the challenge
// it was requested with and returns cred with the new sign count and
// backup state, to save before signing the user in. A sign count that did
// not increase returns ErrSignCount: the authenticator may be cloned.
func (rp RelyingParty) VerifyAssertion(challenge string, cred Credential, a Assertion) (Credential, error) {
	if len(cred.ID) == 0 || subtle.ConstantTimeCompare(a.CredentialID, cred.ID) != 1 {
		return Credential{}, ErrCredential
	}

	if len(a.UserHandle) > 0 && subtle.ConstantTimeCompare(a.UserHandle, cred.UserHandle) != 1 {
		return Credential{}, ErrCredential
	}

	if err := rp.checkClientData(a.clientDataJSON, "webauthn.get", challenge); err != nil {
		return Credential{}, err
	}

	ad, err := parseAuthData(a.authenticatorData)
	if err != nil {
		return Credential{}, err
	}

	if ad.flags&flagAT != 0 {
		return Credential{}, fmt.Errorf("%w: attested data in an assertion", ErrMalformed)
	}

	if err := rp.checkAuthData(ad); err != nil {
		return Credential{}, err
	}

	if (ad.flags&flagBE != 0) != cred.BackupEligible {
		return Credential{}, fmt.Errorf("%w: backup eligibility changed", ErrCredential)
	}

	key, err := parseCOSEKey(cred.PublicKey)
	if err != nil {
		return Credential{}, err
	}

	if key.alg != cred.Algorithm {
		return Credential{}, fmt.Errorf("%w: algorithm changed", ErrCredential)
	}

	cdHash := sha256.Sum256(a.clientDataJSON)
	signed := append(append([]byte(nil), a.authenticatorData...), cdHash[:]...)

	if !key.verify(signed, a.signature) {
		return Credential{}, ErrSignature
	}

	// Authenticators that do not count (synced passkeys) always send 0.
	if (ad.signCount != 0 || cred.SignCount != 0) && ad.signCount <= cred.SignCount {
		return Credential{}, ErrSignCount
	}

	cred.SignCount = ad.signCount
	cred.BackupState = ad.flags&flagBS != 0

	return cred, nil
}
