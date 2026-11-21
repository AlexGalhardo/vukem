package oauth

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/AlexGalhardo/vukem/httpclient"
)

// Client is a registered OAuth client.
type Client struct {
	ID   string
	Name string
	// RedirectURIs are matched exactly, character for character.
	RedirectURIs []string
	// SecretHash is the SHA-256 of the client secret of a confidential
	// client; nil makes a public client, which relies on PKCE alone.
	SecretHash []byte
}

// SecretHash returns the value for Client.SecretHash of secret.
func SecretHash(secret string) []byte {
	sum := sha256.Sum256([]byte(secret))

	return sum[:]
}

// checkSecret compares in constant time, and fails for a public client.
func (c *Client) checkSecret(secret string) bool {
	if len(c.SecretHash) == 0 {
		return false
	}

	got := sha256.Sum256([]byte(secret))

	return subtle.ConstantTimeCompare(got[:], c.SecretHash) == 1
}

func (c *Client) hasRedirect(uri string) bool {
	for _, r := range c.RedirectURIs {
		if subtle.ConstantTimeCompare([]byte(r), []byte(uri)) == 1 {
			return true
		}
	}

	return false
}

// ErrUnknownClient is returned by a ClientResolver for a client it does not
// know.
var ErrUnknownClient = errors.New("oauth: unknown client")

// ClientResolver finds a client by id.
type ClientResolver interface {
	Client(ctx context.Context, id string) (*Client, error)
}

// StaticClients resolves pre-registered clients.
type StaticClients []Client

// Client implements ClientResolver.
func (s StaticClients) Client(_ context.Context, id string) (*Client, error) {
	for i := range s {
		if s[i].ID == id {
			c := s[i]

			return &c, nil
		}
	}

	return nil, ErrUnknownClient
}

// Resolvers tries each resolver in order and returns the first client found;
// a resolver error other than ErrUnknownClient stops the search.
type Resolvers []ClientResolver

// Client implements ClientResolver.
func (r Resolvers) Client(ctx context.Context, id string) (*Client, error) {
	for _, res := range r {
		c, err := res.Client(ctx, id)
		if !errors.Is(err, ErrUnknownClient) {
			return c, err
		}
	}

	return nil, ErrUnknownClient
}

// validRedirectURI accepts https, http on a loopback host, and a private-use
// scheme with a dot (reverse domain, RFC 8252); never a fragment, user info
// or a script scheme.
func validRedirectURI(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Fragment != "" || u.User != nil || u.Scheme == "" || strings.ContainsAny(raw, "\r\n") {
		return false
	}

	switch u.Scheme {
	case "https":
		return u.Host != ""
	case "http":
		h := u.Hostname()

		return h == "localhost" || h == "127.0.0.1" || h == "::1"
	case "javascript", "data", "file", "vbscript", "blob", "about", "ftp":
		return false
	default:
		return strings.Contains(u.Scheme, ".")
	}
}

// MetadataDocuments resolves clients whose client_id is an HTTPS URL that
// serves a Client ID Metadata Document
// (draft-ietf-oauth-client-id-metadata-document): the authorization server
// fetches the URL and trusts what it says about redirect URIs.
//
// The fetch is the risky part, so it is boxed in: HTTPS only, no user info,
// no fragment; public addresses only, checked after DNS and again when the
// socket connects (httpclient.Safe, which covers IPv6 and IPv4-mapped
// forms); no redirects are followed; the body is read up to MaxBytes and the
// whole request is bounded by Timeout.
type MetadataDocuments struct {
	// HTTP is the client used for the fetch; nil builds httpclient.Safe. A
	// custom one must keep the address check, or the fetch is an SSRF hole.
	HTTP *http.Client
	// Timeout bounds the fetch (default 5s).
	Timeout time.Duration
	// MaxBytes bounds the document (default 5 KiB, as the draft suggests).
	MaxBytes int64
}

// Client implements ClientResolver.
func (m *MetadataDocuments) Client(ctx context.Context, id string) (*Client, error) {
	u, err := url.Parse(id)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Fragment != "" ||
		u.Path == "" || u.Path == "/" || strings.ContainsAny(id, "\r\n") {
		return nil, ErrUnknownClient
	}

	if ip := net.ParseIP(strings.Trim(u.Hostname(), "[]")); ip != nil && !publicIP(ip) {
		return nil, ErrUnknownClient
	}

	timeout := m.Timeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}

	limit := m.MaxBytes
	if limit <= 0 {
		limit = 5 << 10
	}

	hc := m.HTTP
	if hc == nil {
		hc = httpclient.Safe(httpclient.Options{Timeout: timeout, MaxBody: limit + 1})
	}

	// A copy that never follows a redirect: the document must live at the
	// client_id itself.
	nc := *hc
	nc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, id, http.NoBody)
	if err != nil {
		return nil, ErrUnknownClient
	}

	req.Header.Set("Accept", "application/json")

	resp, err := nc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: fetch failed", ErrUnknownClient)
	}

	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: status %d", ErrUnknownClient, resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil || int64(len(body)) > limit {
		return nil, fmt.Errorf("%w: document too large", ErrUnknownClient)
	}

	var doc struct {
		ClientID     string   `json:"client_id"`
		ClientName   string   `json:"client_name"`
		RedirectURIs []string `json:"redirect_uris"`
		AuthMethod   string   `json:"token_endpoint_auth_method"`
		Secret       string   `json:"client_secret"`
	}

	if err := json.Unmarshal(body, &doc); err != nil || doc.ClientID != id || len(doc.RedirectURIs) == 0 ||
		doc.Secret != "" || (doc.AuthMethod != "" && doc.AuthMethod != "none") {
		return nil, fmt.Errorf("%w: invalid document", ErrUnknownClient)
	}

	for _, r := range doc.RedirectURIs {
		if !validRedirectURI(r) {
			return nil, fmt.Errorf("%w: invalid redirect URI", ErrUnknownClient)
		}
	}

	return &Client{ID: id, Name: doc.ClientName, RedirectURIs: slices.Clone(doc.RedirectURIs)}, nil
}

// publicIP rejects the literal addresses httpclient.Safe would refuse, so a
// custom HTTP client is never asked to try them either.
func publicIP(ip net.IP) bool {
	return !ip.IsLoopback() && !ip.IsPrivate() && !ip.IsLinkLocalUnicast() && !ip.IsLinkLocalMulticast() &&
		!ip.IsUnspecified() && !ip.IsMulticast() && ip.To16() != nil
}
