package analytics

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

func (c Config) validateBing() error {
	var errs []error

	for _, t := range c.BingSiteVerification {
		if !safeToken.MatchString(t) {
			errs = append(errs, fmt.Errorf("analytics: invalid BING_SITE_VERIFICATION token %q", t))
		}
	}

	return errors.Join(errs...)
}

func (c Config) bingTags() []string {
	tags := make([]string, 0, len(c.BingSiteVerification))
	for _, t := range c.BingSiteVerification {
		tags = append(tags, `<meta name="msvalidate.01" content="`+esc(t)+`">`)
	}

	return tags
}

// BingSiteAuthHandler serves /BingSiteAuth.xml, the Bing Webmaster Tools
// "XML file" verification listing every BingSiteVerification token. vukem
// registers it when BING_SITE_VERIFICATION is set.
func (c Config) BingSiteAuthHandler() http.Handler {
	var b strings.Builder

	b.WriteString("<?xml version=\"1.0\"?>\n<users>\n")

	for _, t := range c.BingSiteVerification {
		b.WriteString("\t<user>" + esc(t) + "</user>\n")
	}

	b.WriteString("</users>\n")
	body := b.String()

	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/xml; charset=utf-8")
		_, _ = io.WriteString(w, body)
	})
}
