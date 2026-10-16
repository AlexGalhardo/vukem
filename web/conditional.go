package web

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/AlexGalhardo/vukem/errs"
)

// ETagOf returns a strong ETag ("\"...\"") for v: the SHA-256 of its JSON
// encoding (or of the bytes when v is []byte or string).
func ETagOf(v any) (string, error) {
	var b []byte

	switch x := v.(type) {
	case []byte:
		b = x
	case string:
		b = []byte(x)
	default:
		var err error
		if b, err = json.Marshal(v); err != nil {
			return "", fmt.Errorf("web: etag: %w", err)
		}
	}

	sum := sha256.Sum256(b)

	return `"` + base64.RawURLEncoding.EncodeToString(sum[:16]) + `"`, nil
}

// NotModified sets the ETag (and Last-Modified when non-zero) headers and
// reports whether the client's cached copy is current. When it returns true
// a 304 was written and the handler must return without a body:
//
//	if c.NotModified(etag, post.UpdatedAt) { return nil }
func (c *Ctx) NotModified(etag string, lastModified time.Time) bool {
	h := c.w.Header()
	if etag != "" {
		h.Set("ETag", etag)
	}

	if !lastModified.IsZero() {
		h.Set("Last-Modified", lastModified.UTC().Format(http.TimeFormat))
	}

	if c.r.Method != http.MethodGet && c.r.Method != http.MethodHead {
		return false
	}

	fresh := false

	if inm := c.r.Header.Get("If-None-Match"); inm != "" {
		fresh = etag != "" && matchETag(inm, etag, true)
	} else if ims := c.r.Header.Get("If-Modified-Since"); ims != "" && !lastModified.IsZero() {
		t, err := http.ParseTime(ims)
		fresh = err == nil && !lastModified.Truncate(time.Second).After(t)
	}

	if fresh {
		c.w.WriteHeader(http.StatusNotModified)
	}

	return fresh
}

// CheckIfMatch enforces optimistic concurrency for writes: when the client
// sent If-Match and it does not match the current ETag, it returns a 412
// error. Without If-Match it returns nil.
func (c *Ctx) CheckIfMatch(current string) error {
	im := c.r.Header.Get("If-Match")
	if im == "" || im == "*" || matchETag(im, current, false) {
		return nil
	}

	return errs.ErrPreconditionFailed
}

// matchETag compares a header list of ETags with tag. Weak comparison (for
// If-None-Match) ignores the W/ prefix; strong comparison rejects weak tags.
func matchETag(header, tag string, weak bool) bool {
	if strings.TrimSpace(header) == "*" {
		return true
	}

	for candidate := range strings.SplitSeq(header, ",") {
		candidate = strings.TrimSpace(candidate)

		if weak {
			if strings.TrimPrefix(candidate, "W/") == strings.TrimPrefix(tag, "W/") {
				return true
			}

			continue
		}

		if !strings.HasPrefix(candidate, "W/") && candidate == tag {
			return true
		}
	}

	return false
}
