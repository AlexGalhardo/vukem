package middleware

import (
	"net/http"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

// cspDirectives runs h once and returns the CSP directives by name, with
// the request nonce replaced by "N".
func cspDirectives(t *testing.T, cfg SecureHeadersConfig) (map[string]string, http.Header) {
	t.Helper()

	var nonce string
	h := SecureHeaders(cfg)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		nonce = CSPNonce(r.Context())
	}))

	rec := do(t, h, get(t, "/"))
	csp := rec.Header().Get("Content-Security-Policy")

	if nonce != "" {
		csp = strings.ReplaceAll(csp, nonce, "N")
	}

	dirs := map[string]string{}
	for part := range strings.SplitSeq(csp, ";") {
		name, value, _ := strings.Cut(strings.TrimSpace(part), " ")
		dirs[name] = value
	}

	return dirs, rec.Header()
}

func TestSecurity_A02_DefaultCSPNonceOnly(t *testing.T) {
	t.Parallel()

	dirs, _ := cspDirectives(t, SecureHeadersConfig{})

	want := map[string]string{
		"default-src":     "'self'",
		"script-src":      "'self' 'nonce-N'",
		"style-src":       "'self' 'nonce-N'",
		"img-src":         "'self' data:",
		"font-src":        "'self'",
		"connect-src":     "'self'",
		"object-src":      "'none'",
		"base-uri":        "'self'",
		"frame-ancestors": "'none'",
		"form-action":     "'self'",
	}
	if diff := cmp.Diff(want, dirs); diff != "" {
		t.Errorf("default CSP directives (-want +got):\n%s", diff)
	}
}

func TestSecureHeadersCSPSources(t *testing.T) {
	t.Parallel()

	dirs, h := cspDirectives(t, SecureHeadersConfig{
		CSPSources: map[string][]string{
			"script-src":  {"https://plausible.io"},
			"connect-src": {"https://plausible.io", "'self'"},
			"style-src":   {"'sha256-47DEQpj8HBSa+/TImW+5JCeuQeRkm5NMpJWZG3hSuFU='"},
			"frame-src":   {"https://www.youtube-nocookie.com"},
		},
		ReportURI: "/csp-report",
	})

	want := map[string]string{
		"script-src":  "'self' 'nonce-N' https://plausible.io",
		"connect-src": "'self' https://plausible.io",
		"style-src":   "'self' 'nonce-N' 'sha256-47DEQpj8HBSa+/TImW+5JCeuQeRkm5NMpJWZG3hSuFU='",
		"frame-src":   "'self' https://www.youtube-nocookie.com",
		"report-uri":  "/csp-report",
		"report-to":   "csp-endpoint",
		"object-src":  "'none'",
	}
	for name, v := range want {
		if dirs[name] != v {
			t.Errorf("CSP %s = %q, want %q", name, dirs[name], v)
		}
	}

	if got := h.Get("Reporting-Endpoints"); got != `csp-endpoint="/csp-report"` {
		t.Errorf("Reporting-Endpoints = %q, want csp-endpoint=\"/csp-report\"", got)
	}

	if _, h := cspDirectives(t, SecureHeadersConfig{}); h.Get("Reporting-Endpoints") != "" {
		t.Errorf("Reporting-Endpoints without ReportURI = %q", h.Get("Reporting-Endpoints"))
	}
}

func TestSecureHeadersCSPSourcesKeepCustomPolicy(t *testing.T) {
	t.Parallel()

	const custom = "default-src 'none'; img-src 'self'"

	dirs, _ := cspDirectives(t, SecureHeadersConfig{
		CSP: custom,
		CSPSources: map[string][]string{
			"img-src":  {"https://cdn.example.com"},
			"font-src": {"https://fonts.example.com"},
		},
	})

	want := map[string]string{
		"default-src": "'none'",
		"img-src":     "'self' https://cdn.example.com",
		"font-src":    "https://fonts.example.com", // 'none' is not carried into the new directive
	}
	if diff := cmp.Diff(want, dirs); diff != "" {
		t.Errorf("custom CSP directives (-want +got):\n%s", diff)
	}
}

func TestSecurity_A02_CSPSourcesRefuseUnsafe(t *testing.T) {
	t.Parallel()

	tests := map[string]SecureHeadersConfig{
		"unsafe-inline":        {CSPSources: map[string][]string{"style-src": {"'unsafe-inline'"}}},
		"unsafe-eval":          {CSPSources: map[string][]string{"script-src": {"'UNSAFE-EVAL'"}}},
		"unsafe-hashes":        {CSPSources: map[string][]string{"style-src": {"'unsafe-hashes'"}}},
		"wasm-unsafe-eval":     {CSPSources: map[string][]string{"script-src": {"'wasm-unsafe-eval'"}}},
		"wildcard":             {CSPSources: map[string][]string{"img-src": {"*"}}},
		"static nonce":         {CSPSources: map[string][]string{"script-src": {"'nonce-abc'"}}},
		"scheme-only script":   {CSPSources: map[string][]string{"script-src": {"https:"}}},
		"data script":          {CSPSources: map[string][]string{"default-src": {"data:"}}},
		"directive injection":  {CSPSources: map[string][]string{"img-src": {"x; script-src *"}}},
		"list injection":       {CSPSources: map[string][]string{"img-src": {"a,b"}}},
		"space in source":      {CSPSources: map[string][]string{"img-src": {"a b"}}},
		"empty source":         {CSPSources: map[string][]string{"img-src": {""}}},
		"bad directive name":   {CSPSources: map[string][]string{"script-src; x": {"https://a.example"}}},
		"unknown non-fetch":    {CSPSources: map[string][]string{"sandbox": {"allow-scripts"}}},
		"sources without CSP":  {CSP: "-", CSPSources: map[string][]string{"img-src": {"https://a.example"}}},
		"report without CSP":   {CSP: "-", ReportURI: "/csp"},
		"report javascript":    {ReportURI: "javascript:alert(1)"},
		"report relative":      {ReportURI: "csp-report"},
		"report protocol-rel":  {ReportURI: "//evil.example/csp"},
		"report quote":         {ReportURI: `/csp"; script-src *`},
		"report already in":    {CSP: "default-src 'self'; report-uri /a", ReportURI: "/b"},
		"report-to in sources": {CSPSources: map[string][]string{"report-uri": {"/a"}}},
	}

	for name, cfg := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if err := cfg.Validate(); err == nil {
				t.Fatalf("Validate(%+v) = nil, want an error", cfg)
			}

			// Without Validate, the extras are dropped: the policy only
			// ever gets stricter.
			base := cfg
			base.CSPSources, base.ReportURI = nil, ""

			got, gotH := cspDirectives(t, cfg)
			want, _ := cspDirectives(t, base)

			if diff := cmp.Diff(want, got); diff != "" {
				t.Errorf("SecureHeaders(invalid) CSP differs from the base policy (-want +got):\n%s", diff)
			}

			if gotH.Get("Reporting-Endpoints") != "" {
				t.Errorf("SecureHeaders(invalid) Reporting-Endpoints = %q, want none", gotH.Get("Reporting-Endpoints"))
			}
		})
	}

	valid := SecureHeadersConfig{
		CSPSources: map[string][]string{
			"script-src": {"https://*.example.com", "'strict-dynamic'", "'sha384-abc+/='"},
			"img-src":    {"blob:", "https:"},
		},
		ReportURI: "https://reports.example.com/csp",
	}
	if err := valid.Validate(); err != nil {
		t.Errorf("Validate(valid) = %v, want nil", err)
	}
}
