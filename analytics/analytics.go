// Package analytics renders privacy-first web analytics and search engine
// verification tags: Umami (cookieless), Google Analytics 4 with Consent
// Mode v2 denied by default, and Google Search Console verification.
//
// Every provider is off until its env var is set: an empty [Config]
// renders nothing and adds nothing to the Content-Security-Policy.
// [Config.Tags] goes in the <head> of the root view (the "analytics" view
// function does it with the request's CSP nonce), and [Config.CSPSources]
// lists the exact origins each enabled provider needs.
package analytics

import (
	"errors"
	"html/template"
	"net"
	"net/url"
	"slices"
	"strings"
)

// Config selects the providers. Load it from the environment with
// config.Load; the zero value disables everything.
type Config struct {
	// UmamiWebsiteID enables Umami (the website ID from its dashboard).
	UmamiWebsiteID string `env:"UMAMI_WEBSITE_ID"`
	// UmamiScriptURL is the tracker script, Umami Cloud by default; point
	// it at a self-hosted instance's /script.js. Empty uses
	// DefaultUmamiScriptURL.
	UmamiScriptURL string `env:"UMAMI_SCRIPT_URL" default:"https://cloud.umami.is/script.js"`
	// UmamiHostURL sends the events to another origin than the script's
	// (data-host-url).
	UmamiHostURL string `env:"UMAMI_HOST_URL"`
	// UmamiDomains limits tracking to these hostnames (data-domains), so
	// staging and localhost stay out of the stats.
	UmamiDomains []string `env:"UMAMI_DOMAINS"`
	// UmamiTag groups the events under a tag (data-tag), e.g. an A/B
	// variant.
	UmamiTag string `env:"UMAMI_TAG"`
	// UmamiIgnoreDoNotTrack tracks visitors whose browser sends Do Not
	// Track; by default they are not tracked (data-do-not-track).
	UmamiIgnoreDoNotTrack bool `env:"UMAMI_IGNORE_DO_NOT_TRACK"`

	// GAMeasurementID enables Google Analytics 4 (G-XXXXXXX).
	GAMeasurementID string `env:"GA_MEASUREMENT_ID"`
	// GAConsentDefaultGranted starts with every Consent Mode signal
	// granted and loads gtag.js on the first page. Leave it off where
	// LGPD/GDPR apply: the app must ask first (window.vukemAnalytics.consent).
	GAConsentDefaultGranted bool `env:"GA_CONSENT_DEFAULT_GRANTED"`

	// GoogleSiteVerification renders one google-site-verification meta tag
	// per token (Search Console, "HTML tag" method).
	GoogleSiteVerification []string `env:"GOOGLE_SITE_VERIFICATION"`
	// GoogleSiteVerificationFiles serves each file name (google<token>.html,
	// Search Console "HTML file" method) at the site root.
	GoogleSiteVerificationFiles []string `env:"GOOGLE_SITE_VERIFICATION_FILES"`
}

// DefaultUmamiScriptURL is the Umami Cloud tracker.
const DefaultUmamiScriptURL = "https://cloud.umami.is/script.js"

// Enabled reports whether any provider or verification is configured.
func (c Config) Enabled() bool {
	return c.UmamiWebsiteID != "" || c.GAMeasurementID != "" ||
		len(c.GoogleSiteVerification) > 0 || len(c.GoogleSiteVerificationFiles) > 0
}

// Validate refuses values that are not what the providers issue. Call it at
// boot (vukem.New does): a typo fails the deploy instead of silently
// dropping the stats.
func (c Config) Validate() error {
	return errors.Join(c.validateUmami(), c.validateGA(), c.validateSearchConsole())
}

// Tags returns the head elements of the enabled providers, with nonce on
// every script ("" omits it). Every value is escaped, validated or not.
func (c Config) Tags(nonce string) template.HTML {
	var tags []string

	tags = append(tags, c.searchConsoleTags()...)

	if t := c.umamiTag(nonce); t != "" {
		tags = append(tags, t)
	}

	if t := c.gaTag(nonce); t != "" {
		tags = append(tags, t)
	}

	return template.HTML(strings.Join(tags, "\n")) //nolint:gosec // G203: every dynamic part is escaped
}

// CSPSources returns the directive sources the enabled providers need, for
// middleware.SecureHeadersConfig.CSPSources (vukem's stacks add them), or
// nil when no script provider is on.
func (c Config) CSPSources() map[string][]string {
	out := map[string][]string{}

	for _, m := range []map[string][]string{c.umamiCSP(), c.gaCSP()} {
		for k, v := range m {
			out[k] = append(out[k], v...)
		}
	}

	if len(out) == 0 {
		return nil
	}

	for k, v := range out {
		slices.Sort(v)
		out[k] = slices.Compact(v)
	}

	return out
}

// origin returns scheme://host[:port] of an absolute URL.
func origin(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return ""
	}

	return u.Scheme + "://" + u.Host
}

// checkURL accepts https URLs, and http ones on loopback hosts (a local
// Umami during development).
func checkURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || u.Opaque != "" {
		return false
	}

	switch u.Scheme {
	case "https":
		return true
	case "http":
		host := u.Hostname()
		ip := net.ParseIP(host)

		return host == "localhost" || (ip != nil && ip.IsLoopback())
	default:
		return false
	}
}

func esc(s string) string { return template.HTMLEscapeString(s) }

func nonceAttr(nonce string) string {
	if nonce == "" {
		return ""
	}

	return ` nonce="` + esc(nonce) + `"`
}
