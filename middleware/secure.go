package middleware

import (
	"cmp"
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/AlexGalhardo/vukem/web"
)

// SecureHeadersConfig configures SecureHeaders. Zero values use the secure
// defaults documented in docs/security.md.
type SecureHeadersConfig struct {
	// CSP is the Content-Security-Policy; "{nonce}" is replaced with a
	// per-request nonce. Empty uses DefaultCSP; "-" disables the header.
	CSP string
	// HSTSMaxAge enables Strict-Transport-Security on HTTPS requests.
	// Zero uses two years; negative disables it.
	HSTSMaxAge time.Duration
	// FrameOptions defaults to DENY; "-" disables it.
	FrameOptions string
	// ReferrerPolicy defaults to strict-origin-when-cross-origin.
	ReferrerPolicy string
	// PermissionsPolicy defaults to camera=(), microphone=(), geolocation=().
	PermissionsPolicy string
	// TrustForwardedProto treats X-Forwarded-Proto: https as HTTPS (set it
	// only behind a trusted proxy).
	TrustForwardedProto bool
	// DevServer returns the origin of a running asset dev server (the Vite
	// hot URL) or "". When set, the origin is allowed in script-src,
	// style-src, img-src, font-src and connect-src (plus its ws:// form for
	// hot reload). Wire it only outside production.
	DevServer func() string
	// CSPSources adds sources to directives of the policy, e.g. an
	// analytics origin in script-src and connect-src, or the 'sha256-...'
	// hash of a style a library injects. A "-src" directive missing from
	// the policy starts from the default-src sources. Unsafe sources
	// ('unsafe-*' keywords, "*", nonces, scheme-only sources in script
	// directives) are refused: write a full CSP to opt into them.
	CSPSources map[string][]string
	// ReportURI receives CSP violation reports: an absolute http(s) URL or
	// a path. It adds report-uri and report-to directives and a
	// Reporting-Endpoints header naming the "csp-endpoint" group.
	ReportURI string
}

// Validate reports CSPSources and ReportURI values that SecureHeaders
// would drop. Call it at boot: SecureHeaders never fails, it serves the
// policy without the invalid extras (stricter, never weaker).
func (cfg SecureHeadersConfig) Validate() error {
	_, err := cfg.policy()
	return err
}

// DefaultCSP is a strict policy suitable for server-rendered pages with
// nonce'd scripts and styles.
const DefaultCSP = "default-src 'self'; script-src 'self' 'nonce-{nonce}'; style-src 'self' 'nonce-{nonce}'; " +
	"img-src 'self' data:; font-src 'self'; connect-src 'self'; object-src 'none'; base-uri 'self'; " +
	"frame-ancestors 'none'; form-action 'self'"

// CSPNonce returns the per-request CSP nonce, or "" when SecureHeaders did
// not run. Templates expose it as {{ cspNonce }}.
func CSPNonce(ctx context.Context) string { return web.CSPNonce(ctx) }

// SecureHeaders sets security headers on every response (A02).
func SecureHeaders(cfg SecureHeadersConfig) Middleware {
	csp, err := cfg.policy()
	if err != nil {
		cfg.CSPSources, cfg.ReportURI = nil, ""
		csp, _ = cfg.policy()
	}

	reporting := ""
	if cfg.ReportURI != "" {
		reporting = reportGroup + `="` + cfg.ReportURI + `"`
	}

	hsts := ""
	if cfg.HSTSMaxAge >= 0 {
		age := cfg.HSTSMaxAge
		if age == 0 {
			age = 2 * 365 * 24 * time.Hour
		}

		hsts = "max-age=" + strconv.Itoa(int(age.Seconds())) + "; includeSubDomains"
	}

	frame := orDefault(cfg.FrameOptions, "DENY")
	referrer := orDefault(cfg.ReferrerPolicy, "strict-origin-when-cross-origin")
	permissions := orDefault(cfg.PermissionsPolicy, "camera=(), microphone=(), geolocation=()")

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := w.Header()
			h.Set("X-Content-Type-Options", "nosniff")
			h.Set("Referrer-Policy", referrer)
			h.Set("Cross-Origin-Opener-Policy", "same-origin")
			h.Set("Permissions-Policy", permissions)

			if frame != "-" {
				h.Set("X-Frame-Options", frame)
			}

			if reporting != "" {
				h.Set("Reporting-Endpoints", reporting)
			}

			https := r.TLS != nil || (cfg.TrustForwardedProto && r.Header.Get("X-Forwarded-Proto") == "https")
			if hsts != "" && https {
				h.Set("Strict-Transport-Security", hsts)
			}

			if csp != "-" {
				nonce := newNonce()
				policy := strings.ReplaceAll(csp, "{nonce}", nonce)

				if cfg.DevServer != nil {
					policy = allowDevServer(policy, cfg.DevServer())
				}

				h.Set("Content-Security-Policy", policy)
				r = r.WithContext(web.WithCSPNonce(r.Context(), nonce))
			}

			next.ServeHTTP(w, r)
		})
	}
}

// reportGroup is the Reporting-Endpoints group named by report-to.
const reportGroup = "csp-endpoint"

// policy returns the CSP template with CSPSources and ReportURI applied.
// Without either, a custom CSP is returned unchanged.
func (cfg SecureHeadersConfig) policy() (string, error) {
	csp := cmp.Or(cfg.CSP, DefaultCSP)
	if len(cfg.CSPSources) == 0 && cfg.ReportURI == "" {
		return csp, nil
	}

	if csp == "-" {
		return "", errors.New("middleware: CSPSources and ReportURI need a Content-Security-Policy (CSP is \"-\")")
	}

	dirs := parseCSP(csp)

	for _, name := range slices.Sorted(maps.Keys(cfg.CSPSources)) {
		if err := checkCSPSources(name, cfg.CSPSources[name]); err != nil {
			return "", err
		}

		i := slices.IndexFunc(dirs, func(d cspDirective) bool { return d.name == name })
		if i < 0 {
			if !strings.HasSuffix(name, "-src") {
				return "", fmt.Errorf("middleware: CSP directive %q is not in the policy; set CSP instead", name)
			}

			dirs = append(dirs, cspDirective{name: name, sources: defaultSources(dirs)})
			i = len(dirs) - 1
		}

		for _, src := range cfg.CSPSources[name] {
			if !slices.Contains(dirs[i].sources, src) {
				dirs[i].sources = append(dirs[i].sources, src)
			}
		}
	}

	if cfg.ReportURI != "" {
		if err := checkReportURI(cfg.ReportURI); err != nil {
			return "", err
		}

		if slices.ContainsFunc(
			dirs,
			func(d cspDirective) bool { return d.name == "report-uri" || d.name == "report-to" },
		) {
			return "", errors.New("middleware: the CSP already reports; drop ReportURI or its report-uri/report-to")
		}

		dirs = append(dirs,
			cspDirective{name: "report-uri", sources: []string{cfg.ReportURI}},
			cspDirective{name: "report-to", sources: []string{reportGroup}},
		)
	}

	parts := make([]string, len(dirs))
	for i, d := range dirs {
		parts[i] = strings.Join(append([]string{d.name}, d.sources...), " ")
	}

	return strings.Join(parts, "; "), nil
}

type cspDirective struct {
	name    string
	sources []string
}

func parseCSP(csp string) []cspDirective {
	var dirs []cspDirective

	for part := range strings.SplitSeq(csp, ";") {
		fields := strings.Fields(part)
		if len(fields) > 0 {
			dirs = append(dirs, cspDirective{name: strings.ToLower(fields[0]), sources: fields[1:]})
		}
	}

	return dirs
}

// defaultSources seeds a new fetch directive with default-src, which it
// would otherwise fall back to; 'none' is dropped as it cannot be combined.
func defaultSources(dirs []cspDirective) []string {
	for _, d := range dirs {
		if d.name == "default-src" {
			return slices.DeleteFunc(slices.Clone(d.sources), func(s string) bool { return s == "'none'" })
		}
	}

	return nil
}

var (
	cspDirectiveName = regexp.MustCompile(`^[a-z][a-z-]*$`)
	cspHash          = regexp.MustCompile(`^'sha(256|384|512)-[A-Za-z0-9+/_=-]+'$`)
	cspScheme        = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9+.-]*:$`)
)

// scriptDirectives run code: a scheme-only source ("https:", "data:")
// there would allow any script.
var scriptDirectives = []string{"default-src", "script-src", "script-src-elem", "object-src", "worker-src", "base-uri"}

func checkCSPSources(name string, sources []string) error {
	if !cspDirectiveName.MatchString(name) || name == "report-uri" || name == "report-to" {
		return fmt.Errorf("middleware: invalid CSP directive %q in CSPSources", name)
	}

	for _, src := range sources {
		lower := strings.ToLower(src)

		switch {
		case src == "" || strings.ContainsAny(src, ";,\"") || strings.ContainsFunc(src, spaceOrControl):
			return fmt.Errorf("middleware: invalid CSP source %q for %s", src, name)
		case src == "*" || strings.Contains(lower, "unsafe"):
			return fmt.Errorf("middleware: CSP source %s for %s is unsafe; set CSP to opt in", src, name)
		case strings.HasPrefix(src, "'") && !cspHash.MatchString(src) &&
			lower != "'self'" && lower != "'strict-dynamic'" && lower != "'report-sample'":
			return fmt.Errorf("middleware: CSP source %s for %s is not allowed (nonces are per request)", src, name)
		case cspScheme.MatchString(src) && slices.Contains(scriptDirectives, name):
			return fmt.Errorf("middleware: scheme-only CSP source %s would allow any %s", src, name)
		}
	}

	return nil
}

func spaceOrControl(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }

func checkReportURI(raw string) error {
	u, err := url.Parse(raw)

	switch {
	case err != nil || strings.ContainsAny(raw, ";,\"'") || strings.ContainsFunc(raw, spaceOrControl):
	case (u.Scheme == "http" || u.Scheme == "https") && u.Host != "":
		return nil
	case u.Scheme == "" && u.Host == "" && strings.HasPrefix(raw, "/") && !strings.HasPrefix(raw, "//"):
		return nil
	}

	return fmt.Errorf("middleware: ReportURI %q must be an http(s) URL or a path", raw)
}

// devOrigin matches a bare http(s) origin, so a dev server URL can never
// inject other CSP directives.
var devOrigin = regexp.MustCompile(`^https?://[A-Za-z0-9.\-\[\]:]+$`)

func allowDevServer(policy, origin string) string {
	origin = strings.TrimSuffix(origin, "/")
	if !devOrigin.MatchString(origin) {
		return policy
	}

	ws := "ws" + strings.TrimPrefix(origin, "http")
	extra := map[string]string{
		"script-src": origin, "style-src": origin, "img-src": origin, "font-src": origin,
		"connect-src": origin + " " + ws,
	}

	parts := strings.Split(policy, ";")
	for i, part := range parts {
		name, _, _ := strings.Cut(strings.TrimSpace(part), " ")
		if add, ok := extra[name]; ok {
			parts[i] = strings.TrimRight(part, " ") + " " + add
		}
	}

	return strings.Join(parts, ";")
}

func newNonce() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b) // never fails since Go 1.24

	return base64.RawURLEncoding.EncodeToString(b) // base64url: no characters that need HTML escaping
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}

	return v
}
