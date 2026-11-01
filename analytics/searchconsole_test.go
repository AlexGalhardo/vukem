package analytics_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/AlexGalhardo/vukem/analytics"
	"github.com/AlexGalhardo/vukem/config"
)

func TestSiteVerificationMeta(t *testing.T) {
	t.Parallel()

	cfg := analytics.Config{GoogleSiteVerification: []string{"tok1", "tok2"}}

	got := string(cfg.Tags("n0nce"))
	want := `<meta name="google-site-verification" content="tok1">` + "\n" +
		`<meta name="google-site-verification" content="tok2">`

	if got != want {
		t.Errorf("Tags()\n got %s\nwant %s", got, want)
	}

	if got := cfg.CSPSources(); got != nil {
		t.Errorf("CSPSources() = %v, want nil (no script)", got)
	}
}

func TestVerificationFileHandler(t *testing.T) {
	t.Parallel()

	h := analytics.Config{GoogleSiteVerificationFiles: []string{"google1234abcd.html"}}.VerificationFileHandler()

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/google1234abcd.html", http.NoBody))

	if rec.Code != http.StatusOK || rec.Body.String() != "google-site-verification: google1234abcd.html" {
		t.Errorf("GET /google1234abcd.html = %d %q", rec.Code, rec.Body.String())
	}

	if ct := rec.Header().Get("Content-Type"); ct != "text/html; charset=utf-8" {
		t.Errorf("Content-Type = %q, want text/html; charset=utf-8", ct)
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/google0000.html", http.NoBody))

	if rec.Code != http.StatusNotFound {
		t.Errorf("GET /google0000.html = %d, want 404", rec.Code)
	}
}

func TestSearchConsoleValidate(t *testing.T) {
	t.Parallel()

	checkRefused(t, map[string]analytics.Config{
		"token":          {GoogleSiteVerification: []string{`a"><script>`}},
		"file traversal": {GoogleSiteVerificationFiles: []string{"../etc/passwd"}},
		"file extension": {GoogleSiteVerificationFiles: []string{"google12.txt"}},
	})
}

func TestFromEnv(t *testing.T) {
	t.Parallel()

	cfg, err := config.Load[analytics.Config](config.MapSource(map[string]string{
		"UMAMI_WEBSITE_ID":               websiteID,
		"UMAMI_DOMAINS":                  "example.com, www.example.com",
		"GA_MEASUREMENT_ID":              gaID,
		"GOOGLE_SITE_VERIFICATION":       "tok1,tok2",
		"GOOGLE_SITE_VERIFICATION_FILES": "google1234abcd.html",
	}))
	if err != nil {
		t.Fatal(err)
	}

	want := analytics.Config{
		UmamiWebsiteID:              websiteID,
		UmamiScriptURL:              analytics.DefaultUmamiScriptURL,
		UmamiDomains:                []string{"example.com", "www.example.com"},
		GAMeasurementID:             gaID,
		GoogleSiteVerification:      []string{"tok1", "tok2"},
		GoogleSiteVerificationFiles: []string{"google1234abcd.html"},
	}
	if diff := cmp.Diff(want, cfg); diff != "" {
		t.Errorf("config (-want +got):\n%s", diff)
	}

	if err := cfg.Validate(); err != nil {
		t.Errorf("Validate() = %v, want nil", err)
	}
}

// TestSecurity_A05_AnalyticsEscapesConfig: Tags escapes every value even when
// Validate was skipped, so a hostile env value never becomes markup or code.
func TestSecurity_A05_AnalyticsEscapesConfig(t *testing.T) {
	t.Parallel()

	evil := `"></script><script>alert(1)</script>`
	cfg := analytics.Config{
		UmamiWebsiteID:         evil,
		UmamiScriptURL:         evil,
		UmamiDomains:           []string{evil},
		UmamiHostURL:           evil,
		UmamiTag:               evil,
		GAMeasurementID:        evil,
		GoogleSiteVerification: []string{evil},
	}

	got := string(cfg.Tags(evil))
	if strings.Contains(got, "alert(1)<") || strings.Count(got, "</script>") != 2 {
		t.Errorf("Tags() renders unescaped config:\n%s", got)
	}
}
