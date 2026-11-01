// Package challenge stops bots on form routes (signup, login, contact)
// with a challenge the browser solves: Cloudflare Turnstile and hCaptcha
// through [SiteVerify], or any [Verifier] of your own.
//
// [Middleware] verifies unsafe requests only. A missing or rejected answer
// is a validation error on the "challenge" field (422, or a redirect back
// with errors for HTML forms); a provider that cannot be reached denies
// with 503 (fail closed). Both log a security event. Pass When to ask only
// suspicious clients, for example after a soft rate limit trips.
//
// [TurnstileConfig] reads TURNSTILE_SITE_KEY and TURNSTILE_SECRET_KEY (off
// while unset) and renders the widget, its nonce'd script and CSP sources.
// [StepUp] challenges a client mid-browse with an interstitial page and a
// signed clearance cookie.
//
//	ts, _ := config.Load[challenge.TurnstileConfig](src)
//	g.Use(ts.Protect("register", nil)) // on the group holding POST /register
//
// See https://github.com/AlexGalhardo/vukem/blob/main/docs/subsystems.md#bot-challenge.
package challenge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/AlexGalhardo/vukem/config"
	"github.com/AlexGalhardo/vukem/errs"
	"github.com/AlexGalhardo/vukem/httpclient"
	"github.com/AlexGalhardo/vukem/uuid"
	"github.com/AlexGalhardo/vukem/web"
)

// Field is the validation-error field of a failed challenge.
const Field = "challenge"

// Header carries the answer for JSON and Inertia requests, which do not
// post the widget's hidden form field.
const Header = "X-Challenge-Token"

// ErrRejected is returned by a Verifier when the answer is missing or
// wrong. Any other error means the check could not run.
var ErrRejected = errors.New("challenge: rejected")

// ErrUnavailable wraps the errors of a provider that could not answer
// (network, timeout, non-200, unreadable answer, its own internal-error),
// as opposed to our misconfiguration (a missing or rejected secret).
var ErrUnavailable = errors.New("challenge: provider unavailable")

// Verifier checks the challenge answer carried by the request.
type Verifier interface {
	// Verify returns nil for a valid answer, an error wrapping ErrRejected
	// for a missing or wrong one, and any other error when it cannot tell.
	Verify(c *web.Ctx) error
}

// Middleware verifies the challenge on POST, PUT, PATCH and DELETE
// requests for which when returns true (nil: all of them); safe methods
// pass, so the page that renders the widget is not challenged.
func Middleware(v Verifier, when func(c *web.Ctx) bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.Method {
			case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodTrace:
				next.ServeHTTP(w, r)
				return
			}

			c := web.NewCtx(w, r, web.ServicesFrom(r.Context()))
			if when != nil && !when(c) {
				next.ServeHTTP(w, c.Request())
				return
			}

			err := v.Verify(c)
			switch {
			case err == nil:
				next.ServeHTTP(w, c.Request())
			case errors.Is(err, ErrRejected):
				web.SecurityEvent(r, "challenge.failed")
				c.HandleError(errs.NewValidation().Add(Field, "Verification failed. Please try again."))
			default:
				web.SecurityEvent(r, "challenge.unavailable")
				c.HandleError(errs.Wrap(http.StatusServiceUnavailable, err))
			}
		})
	}
}

// SiteVerify verifies tokens with a siteverify endpoint, the protocol of
// Cloudflare Turnstile and hCaptcha: the secret, the token and the client
// IP are posted, and the JSON answer must say success.
type SiteVerify struct {
	// URL is the siteverify endpoint.
	URL string
	// Secret is the server-side secret key.
	Secret config.Secret
	// FormField is the form field the widget fills in (the Header is also
	// read, for JSON requests).
	FormField string
	// Hostname, when set, must equal the hostname the provider reports the
	// widget was solved on, so a token solved on another site is refused.
	Hostname string
	// Action, when set, must equal the action the provider reports (the
	// widget's data-action, a Turnstile feature), so a token solved on the
	// newsletter form is refused on the login form.
	Action string
	// Client sends the request; nil uses httpclient.Safe with a 5s timeout.
	Client *http.Client
	// InsecureFailOpen lets requests through while the provider is unavailable
	// (ErrUnavailable), logging a "challenge.fail_open" security event.
	// The default (false) denies with 503: no bot gets in during an
	// outage, and neither does anyone else. A misconfigured secret never
	// fails open.
	InsecureFailOpen bool
}

// Turnstile returns a Cloudflare Turnstile verifier.
func Turnstile(secret config.Secret) *SiteVerify {
	return &SiteVerify{
		URL:       "https://challenges.cloudflare.com/turnstile/v0/siteverify",
		Secret:    secret,
		FormField: "cf-turnstile-response",
	}
}

// HCaptcha returns an hCaptcha verifier.
func HCaptcha(secret config.Secret) *SiteVerify {
	return &SiteVerify{URL: "https://api.hcaptcha.com/siteverify", Secret: secret, FormField: "h-captcha-response"}
}

// maxToken bounds the token forwarded to the provider (Turnstile: 2048).
const maxToken = 4096

// Verify implements Verifier.
func (s *SiteVerify) Verify(c *web.Ctx) error {
	if s.Secret.IsZero() {
		return errors.New("challenge: SiteVerify.Secret is empty")
	}

	token := c.Request().Header.Get(Header)
	if token == "" {
		token = c.Request().PostFormValue(s.FormField)
	}

	if token == "" || len(token) > maxToken {
		return ErrRejected
	}

	err := s.check(c.Context(), token, c.IP())
	if err != nil && s.InsecureFailOpen && errors.Is(err, ErrUnavailable) {
		web.SecurityEvent(c.Request(), "challenge.fail_open", "err", err.Error())
		return nil
	}

	return err
}

func (s *SiteVerify) check(ctx context.Context, token, ip string) error {
	// One idempotency key for both attempts, so a retry of a token the
	// provider already redeemed is not answered as a duplicate.
	form := url.Values{
		"secret": {s.Secret.Reveal()}, "response": {token}, "remoteip": {ip},
		"idempotency_key": {uuid.NewV4().String()},
	}

	client := s.Client
	if client == nil {
		client = httpclient.Safe(httpclient.Options{Timeout: 5 * time.Second})
	}

	res, err := s.post(ctx, client, form)
	if err != nil || res.StatusCode >= http.StatusInternalServerError {
		if res != nil {
			_ = res.Body.Close()
		}

		res, err = s.post(ctx, client, form)
	}

	if err != nil {
		return fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("%w: siteverify answered %d", ErrUnavailable, res.StatusCode)
	}

	var out struct {
		Success  bool     `json:"success"`
		Hostname string   `json:"hostname"`
		Action   string   `json:"action"`
		Codes    []string `json:"error-codes"`
	}

	if err := json.NewDecoder(io.LimitReader(res.Body, 64<<10)).Decode(&out); err != nil {
		return fmt.Errorf("%w: siteverify answer: %w", ErrUnavailable, err)
	}

	if !out.Success {
		codes := strings.Join(out.Codes, ",")
		// Our own misconfiguration or a provider fault: not the user's answer.
		if strings.Contains(codes, "internal-error") {
			return fmt.Errorf("%w: siteverify: %s", ErrUnavailable, codes)
		}

		if strings.Contains(codes, "secret") {
			return fmt.Errorf("challenge: siteverify: %s", codes)
		}

		return fmt.Errorf("%w: %s", ErrRejected, codes)
	}

	if s.Hostname != "" && !strings.EqualFold(out.Hostname, s.Hostname) {
		return fmt.Errorf("%w: hostname %q", ErrRejected, out.Hostname)
	}

	if s.Action != "" && out.Action != s.Action {
		return fmt.Errorf("%w: action %q", ErrRejected, out.Action)
	}

	return nil
}

func (s *SiteVerify) post(ctx context.Context, client *http.Client, form url.Values) (*http.Response, error) {
	// G704: s.URL is app configuration, never request input.
	req, err := http.NewRequestWithContext( //nolint:gosec // G704: see above
		ctx, http.MethodPost, s.URL, strings.NewReader(form.Encode()),
	)
	if err != nil {
		return nil, fmt.Errorf("challenge: %w", err)
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	return client.Do(req) //nolint:gosec // G704: see above
}
