package analytics_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/AlexGalhardo/vukem/analytics"
)

func TestBingSiteVerification(t *testing.T) {
	t.Parallel()

	cfg := analytics.Config{BingSiteVerification: []string{"ABC123", "DEF456"}}

	got := string(cfg.Tags("n0nce"))
	want := `<meta name="msvalidate.01" content="ABC123">` + "\n" + `<meta name="msvalidate.01" content="DEF456">`

	if got != want {
		t.Errorf("Tags()\n got %s\nwant %s", got, want)
	}

	if cfg.CSPSources() != nil || !cfg.Enabled() {
		t.Error("Bing verification: want Enabled() and no CSP sources")
	}

	rec := httptest.NewRecorder()
	cfg.BingSiteAuthHandler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/BingSiteAuth.xml", http.NoBody))

	wantXML := "<?xml version=\"1.0\"?>\n<users>\n\t<user>ABC123</user>\n\t<user>DEF456</user>\n</users>\n"
	if rec.Body.String() != wantXML || rec.Header().Get("Content-Type") != "application/xml; charset=utf-8" {
		t.Errorf("BingSiteAuth.xml = %q (%s), want %q", rec.Body.String(), rec.Header().Get("Content-Type"), wantXML)
	}

	checkRefused(t, map[string]analytics.Config{
		"token": {BingSiteVerification: []string{`a"><script>`}},
	})
}
