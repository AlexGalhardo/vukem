package analytics_test

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/AlexGalhardo/vukem/analytics"
)

const gaID = "G-ABC123XYZ"

func TestGAConsentDefaultsDenied(t *testing.T) {
	t.Parallel()

	got := string(analytics.Config{GAMeasurementID: gaID}.Tags("n0nce"))

	if !strings.HasPrefix(got, `<script nonce="n0nce">`) {
		t.Errorf("GA bootstrap is not nonce'd: %s", got)
	}

	for _, s := range []string{
		`gtag("consent","default",consent(granted?"granted":"denied"))`,
		`"analytics_storage","ad_storage","ad_user_data","ad_personalization"`,
		`send_page_view:false`,
		`inertia:navigate`,
		`window.vukemAnalytics=`,
	} {
		if !strings.Contains(got, s) {
			t.Errorf("GA bootstrap misses %s:\n%s", s, got)
		}
	}

	// gtag.js is only injected by the loader once consent is granted.
	if strings.Contains(got, `<script async src=`) || strings.Contains(got, `<script src=`) {
		t.Errorf("gtag.js loads before consent:\n%s", got)
	}

	granted := string(analytics.Config{GAMeasurementID: gaID, GAConsentDefaultGranted: true}.Tags(""))
	if !strings.Contains(granted, `"G-ABC123XYZ",true`) {
		t.Errorf("GAConsentDefaultGranted not applied:\n%s", granted)
	}
}

// TestSecurity_A02_AnalyticsConsentDeniedByDefault: GA4 starts with every
// Consent Mode v2 signal denied and loads gtag.js only on consent.
func TestSecurity_A02_AnalyticsConsentDeniedByDefault(t *testing.T) {
	t.Parallel()

	got := string(analytics.Config{GAMeasurementID: gaID}.Tags(""))
	if !strings.Contains(got, `"G-ABC123XYZ",false`) {
		t.Errorf("GA does not default to denied consent:\n%s", got)
	}
}

func TestGACSPSources(t *testing.T) {
	t.Parallel()

	want := map[string][]string{
		"script-src": {"https://www.googletagmanager.com"},
		"connect-src": {
			"https://*.analytics.google.com", "https://*.google-analytics.com", "https://www.googletagmanager.com",
		},
		"img-src": {"https://*.google-analytics.com", "https://www.googletagmanager.com"},
	}
	if diff := cmp.Diff(want, analytics.Config{GAMeasurementID: gaID}.CSPSources()); diff != "" {
		t.Errorf("CSPSources() (-want +got):\n%s", diff)
	}
}

func TestGAValidate(t *testing.T) {
	t.Parallel()

	checkRefused(t, map[string]analytics.Config{
		"injection":    {GAMeasurementID: `G-1");alert(1);("`},
		"universal id": {GAMeasurementID: "UA-12345-1"},
		"lowercase":    {GAMeasurementID: "g-abc123"},
	})
}
