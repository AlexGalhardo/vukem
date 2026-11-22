package oauth

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
)

// Consent is what the app supplies to sign the user in and ask for consent.
// The server owns every security decision; the app owns the screens.
type Consent interface {
	// Subject returns the signed-in user. When nobody is signed in it
	// writes its own response (a redirect to the login page, which later
	// sends the browser back to r.URL) and returns ok=false.
	Subject(w http.ResponseWriter, r *http.Request) (subject string, ok bool)
	// Render shows the consent screen. The form must POST to req.Action with
	// every field of req.Fields as a hidden input and one more field,
	// "decision", whose value is "approve" or "deny". Serve it with the
	// app's own CSRF and cache headers (Cache-Control: no-store).
	Render(w http.ResponseWriter, r *http.Request, req ConsentRequest) error
}

// ErrConsentDenied is what a ConsentDecider returns to refuse the grant: the
// client gets access_denied, as if the user had pressed deny.
var ErrConsentDenied = errors.New("oauth: consent denied")

// ConsentDecider is an optional extension of Consent. When the Consent of the
// Config implements it, the server calls Decide on an approved consent POST,
// after the method, same-site, CSRF and decision checks, so the app can tell
// which of the requested scopes the user approved and in which tenant (for
// example the workspace picked on the screen). Read the form of r with
// r.PostForm; it is the user's input, so validate it.
//
// A Consent without Decide keeps the old behavior: the grant gets exactly the
// requested scopes and no tenant.
type ConsentDecider interface {
	Decide(w http.ResponseWriter, r *http.Request, req ConsentRequest) (Decision, error)
}

// Decision is the outcome of ConsentDecider.Decide.
type Decision struct {
	// Scopes is the approved subset of ConsentRequest.Scopes; to approve all
	// of them, return req.Scopes. Empty approves nothing and the client gets
	// access_denied, so a form with no box ticked never grants everything.
	// The server refuses a scope that was not requested (the requested set
	// is bound to the form by the CSRF token, so the user cannot widen it)
	// and the grant, its code, refresh and access tokens carry exactly this
	// set.
	Scopes []string
	// Tenant is stored on the Grant and returned by VerifyAccessToken.
	Tenant string
}

// ConsentRequest describes what the client is asking for.
type ConsentRequest struct {
	Client   *Client
	Subject  string
	Scopes   []string
	Resource string
	// Action is the URL the form posts to; Fields are the hidden inputs,
	// including the "csrf" token.
	Action string
	Fields url.Values
}

type authParams struct {
	clientID, redirectURI, state, challenge, resource string
	scopes                                            []string
}

func parseParams(v url.Values) authParams {
	return authParams{
		clientID: v.Get("client_id"), redirectURI: v.Get("redirect_uri"), state: v.Get("state"),
		challenge: v.Get("code_challenge"), resource: v.Get("resource"), scopes: strings.Fields(v.Get("scope")),
	}
}

func (p authParams) csrf(key []byte, subject string, exp int64) string {
	m := hmac.New(sha256.New, key)

	for _, part := range []string{
		"consent", subject, p.clientID, p.redirectURI, p.state, p.challenge, p.resource, strings.Join(p.scopes, " "),
		strconv.FormatInt(exp, 10),
	} {
		m.Write([]byte(strconv.Itoa(len(part)) + ":" + part))
	}

	return strconv.FormatInt(exp, 10) + "." + base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

func (s *Server) checkCSRF(p authParams, subject, token string) bool {
	expStr, _, ok := strings.Cut(token, ".")

	exp, err := strconv.ParseInt(expStr, 10, 64)
	if !ok || err != nil || s.cfg.Now().Unix() > exp {
		return false
	}

	return subtle.ConstantTimeCompare([]byte(token), []byte(p.csrf(s.cfg.CSRFKey, subject, exp))) == 1
}

// AuthorizeHandler serves the authorization endpoint: GET validates the
// request and shows the consent screen, POST takes the decision.
func (s *Server) AuthorizeHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Content-Security-Policy", "frame-ancestors 'none'")

		if !s.allowed(w, r, "authorize") {
			return
		}

		switch r.Method {
		case http.MethodGet:
			s.authorize(w, r, r.URL.Query(), false)
		case http.MethodPost:
			r.Body = http.MaxBytesReader(w, r.Body, maxFormBytes)

			if err := r.ParseForm(); err != nil {
				http.Error(w, "invalid authorization request", http.StatusBadRequest)

				return
			}

			s.authorize(w, r, r.PostForm, true)
		default:
			w.Header().Set("Allow", "GET, POST")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
}

// authorize validates a request. Until the client and the redirect URI are
// both validated an error is shown to the user and never redirected, so the
// endpoint is not an open redirector.
func (s *Server) authorize(w http.ResponseWriter, r *http.Request, v url.Values, decision bool) {
	bad := func() { http.Error(w, "invalid authorization request", http.StatusBadRequest) }
	p := parseParams(v)

	client, err := s.cfg.Clients.Client(r.Context(), p.clientID)
	if err != nil || client == nil {
		bad()

		return
	}

	registered, ok := client.registeredRedirect(p.redirectURI)
	if !ok {
		bad()

		return
	}

	p.redirectURI = registered

	// RFC 6749 section 4.1.2.1 redirects an error once the redirect URI is
	// valid. For a client from a metadata document "valid" only means what the
	// client's own document says, so redirecting before the person has signed in
	// would let an anonymous link on this domain bounce to a page the attacker
	// chose, carrying error, state and iss. Those clients get a 400 page until
	// Subject succeeds; from then on (the consent screen, the CSRF-checked
	// decision) every client keeps the standard redirect.
	deny := func(code string) {
		if client.fromDocument {
			bad()

			return
		}

		s.redirect(w, r, p, url.Values{"error": {code}})
	}

	switch {
	case v.Get("response_type") != "code":
		deny("unsupported_response_type")

		return
	case p.challenge == "" || v.Get("code_challenge_method") != "S256" || !validChallenge(p.challenge):
		deny("invalid_request")

		return
	case p.resource == "" || !s.allowedResource(p.resource):
		deny("invalid_target")

		return
	case !s.scopesWithin(p.scopes):
		deny("invalid_scope")

		return
	}

	subject, ok := s.cfg.Consent.Subject(w, r)
	if !ok {
		return
	}

	deny = func(code string) { s.redirect(w, r, p, url.Values{"error": {code}}) }

	if !decision {
		exp := s.cfg.Now().Add(s.cfg.ConsentTTL).Unix()

		fields := url.Values{
			"client_id": {p.clientID}, "redirect_uri": {p.redirectURI}, "response_type": {"code"},
			"code_challenge": {p.challenge}, "code_challenge_method": {"S256"}, "scope": {strings.Join(p.scopes, " ")},
			"resource": {p.resource}, "csrf": {p.csrf(s.cfg.CSRFKey, subject, exp)},
		}
		if p.state != "" {
			fields.Set("state", p.state)
		}

		req := ConsentRequest{
			Client:   client,
			Subject:  subject,
			Scopes:   p.scopes,
			Resource: p.resource,
			Action:   r.URL.Path,
			Fields:   fields,
		}
		if err := s.cfg.Consent.Render(w, r, req); err != nil {
			s.cfg.Logger.ErrorContext(r.Context(), "oauth: render consent", "err", err)
		}

		return
	}

	// The consent POST: same-site only, with the CSRF token that binds the
	// decision to this user and these exact parameters.
	if site := r.Header.Get(
		"Sec-Fetch-Site",
	); site != "" && site != "same-origin" && site != "same-site" &&
		site != "none" {
		bad()

		return
	}

	if !s.checkCSRF(p, subject, v.Get("csrf")) {
		bad()

		return
	}

	if v.Get("decision") != "approve" {
		deny("access_denied")

		return
	}

	scopes, tenant := p.scopes, ""

	if d, ok := s.cfg.Consent.(ConsentDecider); ok {
		req := ConsentRequest{
			Client: client, Subject: subject, Scopes: slices.Clone(p.scopes), Resource: p.resource, Action: r.URL.Path,
		}

		dec, err := d.Decide(w, r, req)

		switch {
		case errors.Is(err, ErrConsentDenied):
			deny("access_denied")

			return
		case err != nil:
			s.cfg.Logger.ErrorContext(r.Context(), "oauth: consent decision", "err", err)
			deny("server_error")

			return
		}

		if len(dec.Scopes) == 0 {
			deny("access_denied")

			return
		}

		if scopes = narrow(p.scopes, dec.Scopes); scopes == nil {
			s.cfg.Logger.ErrorContext(
				r.Context(),
				"oauth: consent decision approved a scope that was not requested",
			)
			deny("server_error")

			return
		}

		tenant = dec.Tenant
	}

	s.approve(w, r, p, client, subject, scopes, tenant)
}

// narrow returns the approved scopes without duplicates, in request order, or
// nil when one of them was not requested.
func narrow(requested, approved []string) []string {
	for _, sc := range approved {
		if !slices.Contains(requested, sc) {
			return nil
		}
	}

	return slices.DeleteFunc(slices.Clone(requested), func(sc string) bool { return !slices.Contains(approved, sc) })
}

func (s *Server) approve(
	w http.ResponseWriter,
	r *http.Request,
	p authParams,
	client *Client,
	subject string,
	scopes []string,
	tenant string,
) {
	ctx, now := r.Context(), s.cfg.Now()
	grant := Grant{
		ID:        randomToken(),
		ClientID:  client.ID,
		Subject:   subject,
		Tenant:    tenant,
		Scopes:    scopes,
		Resource:  p.resource,
		CreatedAt: now,
	}
	code := randomToken()

	if err := s.cfg.Store.CreateGrant(ctx, grant); err != nil {
		s.cfg.Logger.ErrorContext(ctx, "oauth: create grant", "err", err)
		s.redirect(w, r, p, url.Values{"error": {"server_error"}})

		return
	}

	err := s.cfg.Store.SaveCode(ctx, Code{
		Hash: hashOf(code), GrantID: grant.ID, ClientID: client.ID, RedirectURI: p.redirectURI,
		Challenge: p.challenge, ExpiresAt: now.Add(s.cfg.CodeTTL),
	})
	if err != nil {
		s.cfg.Logger.ErrorContext(ctx, "oauth: save code", "err", err)
		s.redirect(w, r, p, url.Values{"error": {"server_error"}})

		return
	}

	s.redirect(w, r, p, url.Values{"code": {code}})
}

// redirect sends the user back to the client with the parameters and the
// issuer (RFC 9207), preserving state.
func (s *Server) redirect(w http.ResponseWriter, r *http.Request, p authParams, params url.Values) {
	u, err := url.Parse(p.redirectURI)
	if err != nil {
		http.Error(w, "invalid authorization request", http.StatusBadRequest)

		return
	}

	q := u.Query()
	maps.Copy(q, params)

	if p.state != "" {
		q.Set("state", p.state)
	}

	q.Set("iss", s.cfg.Issuer)
	u.RawQuery = q.Encode()

	http.Redirect(w, r, u.String(), http.StatusFound)
}

// validChallenge accepts a base64url SHA-256 (43 characters).
func validChallenge(c string) bool {
	if len(c) != 43 {
		return false
	}

	b, err := base64.RawURLEncoding.DecodeString(c)

	return err == nil && len(b) == sha256.Size
}

// verifyPKCE checks the S256 code verifier (RFC 7636 section 4.1: 43 to 128
// unreserved characters) against the challenge in constant time.
func verifyPKCE(verifier, challenge string) bool {
	if len(verifier) < 43 || len(verifier) > 128 || slices.ContainsFunc([]rune(verifier), func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '-' && r != '.' &&
			r != '_' &&
			r != '~'
	}) {
		return false
	}

	sum := sha256.Sum256([]byte(verifier))

	return subtle.ConstantTimeCompare([]byte(base64.RawURLEncoding.EncodeToString(sum[:])), []byte(challenge)) == 1
}
