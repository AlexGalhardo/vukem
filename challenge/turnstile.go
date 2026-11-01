package challenge

import (
	"errors"
	"html/template"
	"net/http"
	"strings"
	"time"

	"github.com/AlexGalhardo/vukem/config"
	"github.com/AlexGalhardo/vukem/httpclient"
	"github.com/AlexGalhardo/vukem/web"
)

// TurnstileOrigin serves the Turnstile script and widget iframe; the CSP
// must allow it in script-src and frame-src.
const TurnstileOrigin = "https://challenges.cloudflare.com"

// TurnstileScriptURL is the Turnstile client script. Append
// "?render=explicit" to render widgets from your own JavaScript.
const TurnstileScriptURL = TurnstileOrigin + "/turnstile/v0/api.js"

// TurnstileConfig is the Cloudflare Turnstile setup, read from the
// environment with config.Load[challenge.TurnstileConfig]. Turnstile is
// off until both keys are set: every helper then renders nothing, adds no
// CSP source and lets requests through.
type TurnstileConfig struct {
	// SiteKey is the public widget key (Cloudflare dashboard > Turnstile).
	SiteKey string `env:"TURNSTILE_SITE_KEY"`
	// SecretKey is the server-side key; it prints as "[redacted]".
	SecretKey config.Secret `env:"TURNSTILE_SECRET_KEY"`
	// Hostname, when set, must equal the hostname the widget was solved
	// on, so a token solved on another site is refused.
	Hostname string `env:"TURNSTILE_HOSTNAME"`
	// Timeout bounds each siteverify call (one retry on a network error
	// or a 5xx answer).
	Timeout time.Duration `env:"TURNSTILE_TIMEOUT" default:"5s"`
	// InsecureFailOpen lets requests through while Cloudflare cannot
	// answer; see SiteVerify.InsecureFailOpen. Off: an outage denies (503).
	InsecureFailOpen bool `env:"TURNSTILE_INSECURE_FAIL_OPEN"`
}

// Enabled reports whether both keys are set.
func (t TurnstileConfig) Enabled() bool { return t.SiteKey != "" && !t.SecretKey.IsZero() }

// Validate refuses a half-configured Turnstile (one key without the
// other), which would otherwise be silently off.
func (t TurnstileConfig) Validate() error {
	if (t.SiteKey == "") != t.SecretKey.IsZero() {
		return errors.New("challenge: set both TURNSTILE_SITE_KEY and TURNSTILE_SECRET_KEY, or neither")
	}

	return nil
}

// Verifier returns a SiteVerify for widgets rendered with action (empty:
// no action check), or nil when Turnstile is off.
func (t TurnstileConfig) Verifier(action string) *SiteVerify {
	if !t.Enabled() {
		return nil
	}

	v := Turnstile(t.SecretKey)
	v.Hostname, v.Action, v.InsecureFailOpen = t.Hostname, action, t.InsecureFailOpen

	if t.Timeout > 0 {
		v.Client = httpclient.Safe(httpclient.Options{Timeout: t.Timeout})
	}

	return v
}

// Protect is Middleware with a Verifier for action, or a pass-through when
// Turnstile is off. Put it on the routes that receive the form.
func (t TurnstileConfig) Protect(action string, when func(*web.Ctx) bool) func(http.Handler) http.Handler {
	if !t.Enabled() {
		return func(next http.Handler) http.Handler { return next }
	}

	return Middleware(t.Verifier(action), when)
}

// CSPSources returns the sources Turnstile needs, for
// middleware.SecureHeadersConfig.CSPSources, or nil when it is off.
func (t TurnstileConfig) CSPSources() map[string][]string {
	if !t.Enabled() {
		return nil
	}

	return map[string][]string{"script-src": {TurnstileOrigin}, "frame-src": {TurnstileOrigin}}
}

var (
	scriptTmpl = template.Must(template.New("script").Parse(
		`<script src="{{.Src}}" async defer{{with .Nonce}} nonce="{{.}}"{{end}}></script>`,
	))
	widgetTmpl = template.Must(template.New("widget").Parse(
		`<div class="cf-turnstile" data-sitekey="{{.SiteKey}}"{{with .Action}} data-action="{{.}}"{{end}}></div>`,
	))
)

// Script returns the Turnstile script tag carrying the request's CSP nonce
// ({{ turnstileScript cspNonce }}), or "" when Turnstile is off.
func (t TurnstileConfig) Script(nonce string) template.HTML {
	if !t.Enabled() {
		return ""
	}

	return render(scriptTmpl, struct{ Src, Nonce string }{TurnstileScriptURL, nonce})
}

// Widget returns the implicit-render widget markup for a form, with
// data-action set when action is not empty, or "" when Turnstile is off.
// The widget adds the cf-turnstile-response field to the form.
func (t TurnstileConfig) Widget(action string) template.HTML {
	if !t.Enabled() {
		return ""
	}

	return render(widgetTmpl, struct{ SiteKey, Action string }{t.SiteKey, action})
}

func render(t *template.Template, data any) template.HTML {
	var b strings.Builder
	if err := t.Execute(&b, data); err != nil {
		return "" // unreachable: fixed templates over string fields
	}

	return template.HTML(b.String()) //nolint:gosec // G203: html/template output, attributes escaped
}
