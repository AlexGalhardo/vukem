package challenge

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"html/template"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/AlexGalhardo/vukem/csrf"
	"github.com/AlexGalhardo/vukem/errs"
	"github.com/AlexGalhardo/vukem/signed"
	"github.com/AlexGalhardo/vukem/web"
)

// ReturnField is the interstitial form field holding the page to go back to.
const ReturnField = "return"

// StepUp challenges a client in the middle of browsing: a middleware that
// finds a client suspicious (over a soft rate limit) calls Require, which
// serves an interstitial page with the widget. The page posts to Handle,
// which verifies the token, sets a short-lived signed clearance cookie
// bound to the client's IP prefix and User-Agent, and redirects back.
//
// Build it with NewStepUp; set the fields before serving requests.
type StepUp struct {
	// Turnstile holds the keys; StepUp is off (Require passes) when it is.
	Turnstile TurnstileConfig
	// Verifier checks the token; nil uses Turnstile.Verifier(Action).
	Verifier Verifier
	// Action is the widget's data-action, checked by the default
	// Verifier ("step-up").
	Action string
	// Path is the route Handle is mounted on ("/challenge").
	Path string
	// TTL is how long a clearance lasts (30 minutes).
	TTL time.Duration
	// Cookie is the clearance cookie name ("vukem_clearance").
	Cookie string
	// Skip exempts a request from Require (verified search crawlers,
	// signed-in users): the app decides, StepUp never guesses.
	Skip func(c *web.Ctx) bool
	// Now is the clock (time.Now).
	Now func() time.Time

	signer *signed.Signer
}

// NewStepUp returns a StepUp signing clearances with key; derive it with
// app.DeriveKey("challenge.clearance", 32). It panics on a key shorter
// than signed.MinKeyLen.
func NewStepUp(t TurnstileConfig, key []byte) *StepUp {
	return &StepUp{
		Turnstile: t,
		Action:    "step-up",
		Path:      "/challenge",
		TTL:       30 * time.Minute,
		Cookie:    "vukem_clearance",
		Now:       time.Now,
		signer:    signed.New(key),
	}
}

// Require reports whether the request may go on: Turnstile is off, Skip
// says so, or the client holds a valid clearance. Otherwise it writes the
// interstitial (403, noindex, no-store) and returns false; the caller
// must then stop.
func (s *StepUp) Require(c *web.Ctx) bool {
	if !s.Turnstile.Enabled() || s.Skip != nil && s.Skip(c) || s.cleared(c) {
		return true
	}

	ret := "/"
	if r := c.Request(); r.Method == http.MethodGet || r.Method == http.MethodHead {
		ret = r.URL.RequestURI()
	}

	web.SecurityEvent(c.Request(), "challenge.step_up")
	s.interstitial(c, http.StatusForbidden, ret)

	return false
}

// Handle verifies the interstitial's token; mount it on POST Path. A valid
// token sets the clearance cookie and redirects (303) to the return field
// when it is a local path, else to "/". A rejected token shows the page
// again (403); a provider outage is a 503 unless InsecureFailOpen.
func (s *StepUp) Handle(c *web.Ctx) error {
	ret := LocalPath(c.Request().PostFormValue(ReturnField))
	if !s.Turnstile.Enabled() {
		return c.RedirectStatus(http.StatusSeeOther, ret)
	}

	v := s.Verifier
	if v == nil {
		v = s.Turnstile.Verifier(s.Action)
	}

	if err := v.Verify(c); err != nil {
		if !errors.Is(err, ErrRejected) {
			web.SecurityEvent(c.Request(), "challenge.unavailable")
			return errs.Wrap(http.StatusServiceUnavailable, err)
		}

		web.SecurityEvent(c.Request(), "challenge.failed")
		s.interstitial(c, http.StatusForbidden, ret)

		return nil
	}

	exp := s.Now().Add(s.TTL).Unix()
	http.SetCookie(c.Writer(), &http.Cookie{
		Name:     s.Cookie,
		Value:    s.signer.Token([]byte(strconv.FormatInt(exp, 10) + "|" + binding(c))),
		Path:     "/",
		MaxAge:   int(s.TTL.Seconds()),
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})

	return c.RedirectStatus(http.StatusSeeOther, ret)
}

func (s *StepUp) cleared(c *web.Ctx) bool {
	ck, err := c.Request().Cookie(s.Cookie)
	if err != nil {
		return false
	}

	payload, err := s.signer.Open(ck.Value)
	if err != nil {
		return false
	}

	expRaw, bind, ok := strings.Cut(string(payload), "|")
	exp, err := strconv.ParseInt(expRaw, 10, 64)

	return ok && err == nil && s.Now().Unix() < exp && hmac.Equal([]byte(bind), []byte(binding(c)))
}

// binding ties a clearance to the client's network (IPv4 /24, IPv6 /48,
// so a mobile client moving inside its carrier's range keeps it) and
// browser, so a cookie copied to another network or tool is refused.
func binding(c *web.Ctx) string {
	prefix := c.IP()
	if ip, err := netip.ParseAddr(prefix); err == nil {
		bits := 48
		if ip.Unmap().Is4() {
			ip, bits = ip.Unmap(), 24
		}

		p, _ := ip.Prefix(bits)
		prefix = p.String()
	}

	sum := sha256.Sum256([]byte(prefix + "\x00" + c.Request().UserAgent()))

	return base64.RawURLEncoding.EncodeToString(sum[:16])
}

// LocalPath returns raw when it is a path on this site ("/items?page=2"),
// else "/": never another host ("//evil.example", "/\evil.example",
// "https://evil.example"), so a return parameter cannot redirect away.
func LocalPath(raw string) string {
	if raw == "/" {
		return raw
	}

	if len(raw) < 2 || len(raw) > 2048 || raw[0] != '/' || raw[1] == '/' || raw[1] == '\\' ||
		strings.ContainsAny(raw, "\\\x00\r\n\t") {
		return "/"
	}

	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "" || u.Host != "" || u.User != nil {
		return "/"
	}

	return raw
}

var interstitialTmpl = template.Must(template.New("interstitial").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="robots" content="noindex, nofollow">
<title>Checking your browser</title>
<script src="{{.Src}}" async defer{{with .Nonce}} nonce="{{.}}"{{end}}></script>
<script{{with .Nonce}} nonce="{{.}}"{{end}}>function vukemCleared(){document.getElementById("vukem-challenge").submit()}</script>
</head>
<body>
<main>
<h1>One moment</h1>
<p>Please confirm you are human to continue.</p>
<form id="vukem-challenge" method="post" action="{{.Action}}">
<input type="hidden" name="return" value="{{.Return}}">
{{with .CSRF}}<input type="hidden" name="_token" value="{{.}}">{{end}}
<div class="cf-turnstile" data-sitekey="{{.SiteKey}}" data-action="{{.WidgetAction}}" data-callback="vukemCleared"></div>
<button type="submit">Continue</button>
</form>
</main>
</body>
</html>
`))

func (s *StepUp) interstitial(c *web.Ctx, status int, ret string) {
	h := c.Writer().Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Robots-Tag", "noindex, nofollow")
	c.Writer().WriteHeader(status)

	ctx := c.Context()
	_ = interstitialTmpl.Execute(c.Writer(), map[string]string{
		"Src":          TurnstileScriptURL,
		"Nonce":        web.CSPNonce(ctx),
		"Action":       s.Path,
		"Return":       ret,
		"CSRF":         csrf.Token(ctx),
		"SiteKey":      s.Turnstile.SiteKey,
		"WidgetAction": s.Action,
	})
}
