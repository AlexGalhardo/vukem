package social

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"
)

// OIDCProvider is an OpenID Connect provider: endpoints come from the
// issuer's discovery document and the ID token is verified (signature via
// JWKS with RS256/ES256 only, iss, aud, exp, iat, nonce).
type OIDCProvider struct {
	name   string
	issuer string
	cfg    Config
	scopes []string
	extra  map[string][]string
	now    func() time.Time
	// emailVerified reads the provider's notion of a verified email.
	emailVerified func(claims map[string]any) bool

	mu   sync.Mutex
	disc *discovery
	keys map[string]crypto.PublicKey
}

type discovery struct {
	Issuer   string `json:"issuer"`
	Auth     string `json:"authorization_endpoint"`
	Token    string `json:"token_endpoint"`
	UserInfo string `json:"userinfo_endpoint"`
	JWKS     string `json:"jwks_uri"`
}

// OIDC builds a provider for any OpenID Connect issuer
// ("https://accounts.example.com"); name is the URL segment.
func OIDC(name, issuer string, cfg Config) *OIDCProvider {
	return &OIDCProvider{
		name: name, issuer: strings.TrimSuffix(issuer, "/"), cfg: cfg,
		scopes: []string{"openid", "email", "profile"}, now: time.Now,
		emailVerified: func(c map[string]any) bool { return c["email_verified"] == true },
	}
}

// Google signs in with Google (OIDC).
func Google(cfg Config) *OIDCProvider { return OIDC("google", "https://accounts.google.com", cfg) }

// Microsoft signs in with Microsoft Entra ID (OIDC). tenant is "common",
// "organizations", "consumers" or a tenant id. Microsoft's email claim is
// not verified unless the xms_edov claim is true.
func Microsoft(tenant string, cfg Config) *OIDCProvider {
	p := OIDC("microsoft", "https://login.microsoftonline.com/"+tenant+"/v2.0", cfg)
	p.emailVerified = func(c map[string]any) bool { return c["xms_edov"] == true }

	return p
}

// Name implements Provider.
func (p *OIDCProvider) Name() string { return p.name }

// AuthParams adds authorization URL parameters (e.g. prompt=select_account).
func (p *OIDCProvider) AuthParams(v map[string][]string) *OIDCProvider { p.extra = v; return p }

func (p *OIDCProvider) discover(ctx context.Context, h *http.Client) (*discovery, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.disc != nil {
		return p.disc, nil
	}

	var d discovery
	if err := getJSON(ctx, h, p.issuer+"/.well-known/openid-configuration", "", &d); err != nil {
		return nil, fmt.Errorf("social: oidc discovery: %w", err)
	}

	if d.Auth == "" || d.Token == "" || d.JWKS == "" {
		return nil, errors.New("social: oidc discovery document is incomplete")
	}

	p.disc = &d

	return &d, nil
}

// AuthURL implements Provider.
func (p *OIDCProvider) AuthURL(
	ctx context.Context,
	h *http.Client,
	state, codeChallenge, nonce string,
) (string, error) {
	d, err := p.discover(ctx, h)
	if err != nil {
		return "", err
	}

	return authURL(d.Auth, p.cfg, p.scopes, state, codeChallenge, nonce, p.extra), nil
}

// Exchange implements Provider.
func (p *OIDCProvider) Exchange(ctx context.Context, h *http.Client, code, verifier string) (Token, error) {
	d, err := p.discover(ctx, h)
	if err != nil {
		return Token{}, err
	}

	return exchange(ctx, h, d.Token, p.cfg, code, verifier, p.now())
}

// User implements Provider: claims come from the verified ID token, or
// from the userinfo endpoint in stateless mode (no ID token).
func (p *OIDCProvider) User(ctx context.Context, h *http.Client, tok Token, nonce string) (User, error) {
	d, err := p.discover(ctx, h)
	if err != nil {
		return User{}, err
	}

	var claims map[string]any

	if tok.IDToken != "" {
		if claims, err = p.verify(ctx, h, d, tok.IDToken, nonce); err != nil {
			return User{}, err
		}
	} else {
		if d.UserInfo == "" {
			return User{}, errors.New("social: no ID token and no userinfo endpoint")
		}

		if err := getJSON(ctx, h, d.UserInfo, tok.Access, &claims); err != nil {
			return User{}, fmt.Errorf("social: userinfo: %w", err)
		}
	}

	u := User{
		ID:            str(claims["sub"]),
		Email:         str(claims["email"]),
		EmailVerified: p.emailVerified(claims) && str(claims["email"]) != "",
		Name:          str(claims["name"]),
		Nickname:      str(claims["preferred_username"]),
		AvatarURL:     str(claims["picture"]),
		Raw:           claims,
	}

	if u.ID == "" {
		return User{}, errNoID
	}

	return u, nil
}

// ErrIDToken is wrapped by every ID token verification failure.
var ErrIDToken = errors.New("social: invalid ID token")

func idTokenErr(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrIDToken, fmt.Sprintf(format, args...))
}

// verify checks the JWS signature and the standard claims.
func (p *OIDCProvider) verify(
	ctx context.Context,
	h *http.Client,
	d *discovery,
	raw, nonce string,
) (map[string]any, error) {
	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		return nil, idTokenErr("malformed")
	}

	var header struct {
		Alg string `json:"alg"`
		Kid string `json:"kid"`
	}

	if err := decodePart(parts[0], &header); err != nil {
		return nil, idTokenErr("header: %v", err)
	}

	if header.Alg != "RS256" && header.Alg != "ES256" {
		return nil, idTokenErr("algorithm %q not allowed", header.Alg) // never "none" or HS*
	}

	key, err := p.key(ctx, h, d, header.Kid)
	if err != nil {
		return nil, err
	}

	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return nil, idTokenErr("signature encoding")
	}

	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))

	switch k := key.(type) {
	case *rsa.PublicKey:
		if header.Alg != "RS256" || rsa.VerifyPKCS1v15(k, crypto.SHA256, digest[:], sig) != nil {
			return nil, idTokenErr("bad signature")
		}
	case *ecdsa.PublicKey:
		if header.Alg != "ES256" || len(sig) != 64 ||
			!ecdsa.Verify(k, digest[:], new(big.Int).SetBytes(sig[:32]), new(big.Int).SetBytes(sig[32:])) {
			return nil, idTokenErr("bad signature")
		}
	default:
		return nil, idTokenErr("unsupported key")
	}

	var claims map[string]any
	if err := decodePart(parts[1], &claims); err != nil {
		return nil, idTokenErr("claims: %v", err)
	}

	return claims, p.checkClaims(d, claims, nonce)
}

const leeway = time.Minute

func (p *OIDCProvider) checkClaims(d *discovery, claims map[string]any, nonce string) error {
	iss := d.Issuer
	if strings.Contains(iss, "{tenantid}") { // Microsoft multi-tenant metadata
		iss = strings.ReplaceAll(iss, "{tenantid}", str(claims["tid"]))
	}

	if str(claims["iss"]) != iss || iss == "" {
		return idTokenErr("issuer %q", str(claims["iss"]))
	}

	switch aud := claims["aud"].(type) {
	case string:
		if aud != p.cfg.ClientID {
			return idTokenErr("audience")
		}
	case []any:
		if !slices.Contains(aud, any(p.cfg.ClientID)) {
			return idTokenErr("audience")
		}

		if len(aud) > 1 && str(claims["azp"]) != p.cfg.ClientID {
			return idTokenErr("authorized party")
		}
	default:
		return idTokenErr("audience")
	}

	now := p.now()

	exp, ok := claims["exp"].(float64)
	if !ok || now.After(time.Unix(int64(exp), 0).Add(leeway)) {
		return idTokenErr("expired")
	}

	if iat, ok := claims["iat"].(float64); ok && time.Unix(int64(iat), 0).After(now.Add(leeway)) {
		return idTokenErr("issued in the future")
	}

	if nonce != "" && subtle.ConstantTimeCompare([]byte(str(claims["nonce"])), []byte(nonce)) != 1 {
		return idTokenErr("nonce")
	}

	return nil
}

func decodePart(s string, v any) error {
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return err
	}

	return json.Unmarshal(b, v)
}

// key returns the JWKS key kid, refetching the set once for unknown ids
// (key rotation).
func (p *OIDCProvider) key(ctx context.Context, h *http.Client, d *discovery, kid string) (crypto.PublicKey, error) {
	p.mu.Lock()
	k, ok := p.keys[kid]
	p.mu.Unlock()

	if ok {
		return k, nil
	}

	var set struct {
		Keys []jwk `json:"keys"`
	}

	if err := getJSON(ctx, h, d.JWKS, "", &set); err != nil {
		return nil, fmt.Errorf("social: jwks: %w", err)
	}

	keys := map[string]crypto.PublicKey{}

	for _, j := range set.Keys {
		if pub, err := j.public(); err == nil {
			keys[j.Kid] = pub
		}
	}

	p.mu.Lock()
	p.keys = keys
	p.mu.Unlock()

	if k, ok := keys[kid]; ok {
		return k, nil
	}

	return nil, idTokenErr("unknown key id %q", kid)
}

type jwk struct {
	Kty string `json:"kty"`
	Kid string `json:"kid"`
	N   string `json:"n"`
	E   string `json:"e"`
	Crv string `json:"crv"`
	X   string `json:"x"`
	Y   string `json:"y"`
}

func (j jwk) public() (crypto.PublicKey, error) {
	dec := base64.RawURLEncoding

	switch j.Kty {
	case "RSA":
		n, err1 := dec.DecodeString(j.N)
		e, err2 := dec.DecodeString(j.E)

		if err := errors.Join(err1, err2); err != nil || len(e) > 4 {
			return nil, errors.New("bad RSA key")
		}

		pub := &rsa.PublicKey{N: new(big.Int).SetBytes(n), E: int(new(big.Int).SetBytes(e).Int64())}
		if pub.N.BitLen() < 2048 {
			return nil, errors.New("RSA key too small")
		}

		return pub, nil
	case "EC":
		if j.Crv != "P-256" {
			return nil, errors.New("unsupported curve")
		}

		x, err1 := dec.DecodeString(j.X)
		y, err2 := dec.DecodeString(j.Y)

		if err := errors.Join(err1, err2); err != nil {
			return nil, err
		}

		uncompressed := append(append([]byte{4}, pad32(x)...), pad32(y)...)

		return ecdsa.ParseUncompressedPublicKey(elliptic.P256(), uncompressed)
	default:
		return nil, errors.New("unsupported key type")
	}
}

// pad32 left-pads a big-endian P-256 coordinate or signature half.
func pad32(b []byte) []byte {
	if len(b) >= 32 {
		return b
	}

	return append(make([]byte, 32-len(b)), b...)
}
