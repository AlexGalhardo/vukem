package analytics

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"slices"
	"strings"
)

var (
	safeToken        = regexp.MustCompile(`^[A-Za-z0-9_-]{1,100}$`)
	verificationFile = regexp.MustCompile(`^google[A-Za-z0-9]{1,64}\.html$`)
)

func (c Config) validateSearchConsole() error {
	var errs []error

	for _, t := range c.GoogleSiteVerification {
		if !safeToken.MatchString(t) {
			errs = append(errs, fmt.Errorf("analytics: invalid GOOGLE_SITE_VERIFICATION token %q", t))
		}
	}

	for _, f := range c.GoogleSiteVerificationFiles {
		if !verificationFile.MatchString(f) {
			errs = append(
				errs,
				fmt.Errorf("analytics: GOOGLE_SITE_VERIFICATION_FILES entry %q is not google<token>.html", f),
			)
		}
	}

	return errors.Join(errs...)
}

func (c Config) searchConsoleTags() []string {
	tags := make([]string, 0, len(c.GoogleSiteVerification))
	for _, t := range c.GoogleSiteVerification {
		tags = append(tags, `<meta name="google-site-verification" content="`+esc(t)+`">`)
	}

	return tags
}

// VerificationFileHandler serves the GoogleSiteVerificationFiles at the
// site root ("GET /google<token>.html") with the content Search Console
// checks, and 404 for any other path. vukem registers it for each file.
func (c Config) VerificationFileHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		i := slices.Index(c.GoogleSiteVerificationFiles, strings.TrimPrefix(r.URL.Path, "/"))
		if i < 0 {
			http.NotFound(w, r)
			return
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, "google-site-verification: "+c.GoogleSiteVerificationFiles[i])
	})
}
