package cloudflare

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/AlexGalhardo/vukem/auth/jwt"
)

// AccessHeader carries the Cloudflare Access application token.
const AccessHeader = "Cf-Access-Jwt-Assertion"

// Access validates Cloudflare Access application tokens
// (https://developers.cloudflare.com/cloudflare-one/identity/authorization-cookie/validating-json/,
// read 2026-10-29): RS256, signed by the team's keys at
// <team domain>/cdn-cgi/access/certs, issued by the team domain, for the
// application's AUD tag, not expired.
type Access struct {
	verifier *jwt.Verifier
}

// AccessOptions configures NewAccess.
type AccessOptions struct {
	// TeamDomain is the Zero Trust team domain, such as
	// "https://example.cloudflareaccess.com".
	TeamDomain string
	// Audience is the application's AUD tag.
	Audience string
	// Client fetches the keys; nil uses a 10s timeout.
	Client *http.Client
	// Now is the clock (default time.Now).
	Now func() time.Time
}

type accessKey struct{}

// NewAccess returns an Access validator. An empty audience or a team
// domain that is not an https origin is an error: either would accept
// tokens Access issued for other applications or other accounts.
func NewAccess(o AccessOptions) (*Access, error) {
	team := strings.TrimSuffix(o.TeamDomain, "/")

	u, err := url.Parse(team)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.Path != "" || u.RawQuery != "" {
		return nil, errors.New(
			"cloudflare: Access TeamDomain must be an https origin such as https://team.cloudflareaccess.com",
		)
	}

	if o.Audience == "" {
		return nil, errors.New("cloudflare: Access Audience (the application's AUD tag) is required")
	}

	keys := &jwt.RemoteJWKS{URL: team + "/cdn-cgi/access/certs", Client: o.Client, Now: o.Now}
	v := jwt.NewVerifier(keys, jwt.VerifyOptions{
		Algorithms: []string{"RS256"}, Issuer: team, Audience: o.Audience, Now: o.Now,
	})

	if err := v.Err(); err != nil {
		return nil, err
	}

	return &Access{verifier: v}, nil
}

// Verify validates an application token.
func (a *Access) Verify(ctx context.Context, token string) (jwt.Claims, error) {
	return a.verifier.Verify(ctx, token)
}

// Middleware returns middleware that answers 403 unless the request
// carries a valid token in AccessHeader (the CF_Authorization cookie is
// not read: Cloudflare does not guarantee it), and puts the claims in the
// request context for AccessClaimsFrom. A key fetch that fails denies.
func (a *Access) Middleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token := r.Header.Get(AccessHeader)
			if token == "" {
				http.Error(w, http.StatusText(http.StatusForbidden), http.StatusForbidden)
				return
			}

			claims, err := a.verifier.Verify(r.Context(), token)
			if err != nil {
				http.Error(w, http.StatusText(http.StatusForbidden), http.StatusForbidden)
				return
			}

			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), accessKey{}, claims)))
		})
	}
}

// AccessClaimsFrom returns the claims Middleware verified; the user's
// email is in Extra["email"].
func AccessClaimsFrom(ctx context.Context) (jwt.Claims, bool) {
	c, ok := ctx.Value(accessKey{}).(jwt.Claims)
	return c, ok
}
