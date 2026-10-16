package s3

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/AlexGalhardo/vukem/config"
)

// AWS Signature Version 4, implemented from the published specification:
// https://docs.aws.amazon.com/IAM/latest/UserGuide/reference_sigv-create-signed-request.html
// S3 differs from the generic algorithm in two ways handled here: paths
// are encoded once (never normalized or double-encoded) and the payload
// hash travels in x-amz-content-sha256.

const (
	algorithm       = "AWS4-HMAC-SHA256"
	unsignedPayload = "UNSIGNED-PAYLOAD"
	amzDateFormat   = "20060102T150405Z"
	scopeDateFormat = "20060102"
	emptySHA256     = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
)

// signer holds the credentials; the secret stays a config.Secret so
// printing a Disk never reveals it.
type signer struct {
	accessKey string
	secret    config.Secret
	token     config.Secret
	region    string
	service   string
}

// unsignedHeaders are added or rewritten by proxies and the transport, so
// signing them would break requests in transit.
var unsignedHeaders = map[string]bool{ //nolint:gochecknoglobals // read-only lookup table
	"authorization":   true,
	"user-agent":      true,
	"expect":          true,
	"x-amzn-trace-id": true,
	"accept-encoding": true,
	"connection":      true,
	"content-length":  true,
}

// sign adds the x-amz-date, x-amz-security-token and Authorization headers.
// payloadHash is the hex SHA-256 of the body or unsignedPayload; the caller
// sets x-amz-content-sha256 for S3 (the generic test suite does not).
func (s signer) sign(req *http.Request, payloadHash string, now time.Time) {
	t := now.UTC()
	req.Header.Set("X-Amz-Date", t.Format(amzDateFormat))

	if !s.token.IsZero() {
		req.Header.Set("X-Amz-Security-Token", s.token.Reveal())
	}

	creq, signed := canonicalRequest(req, payloadHash)
	scope := s.scope(t)
	sig := s.signature(t, stringToSign(t, scope, creq))

	req.Header.Set("Authorization", algorithm+" Credential="+s.accessKey+"/"+scope+
		", SignedHeaders="+signed+", Signature="+sig)
}

// presign returns u with the query-string signature for method, valid for
// expires. Only the host header is signed, so any client can use the URL.
func (s signer) presign(method string, u *url.URL, now time.Time, expires time.Duration) string {
	t := now.UTC()
	scope := s.scope(t)

	q := u.Query()
	q.Set("X-Amz-Algorithm", algorithm)
	q.Set("X-Amz-Credential", s.accessKey+"/"+scope)
	q.Set("X-Amz-Date", t.Format(amzDateFormat))
	q.Set("X-Amz-Expires", strconv.FormatInt(int64(expires/time.Second), 10))
	q.Set("X-Amz-SignedHeaders", "host")

	if !s.token.IsZero() {
		q.Set("X-Amz-Security-Token", s.token.Reveal())
	}

	signedURL := *u
	signedURL.RawQuery = canonicalQuery(q)

	req := &http.Request{Method: method, URL: &signedURL, Host: u.Host, Header: http.Header{}}
	creq, _ := canonicalRequest(req, unsignedPayload)
	sig := s.signature(t, stringToSign(t, scope, creq))

	signedURL.RawQuery += "&X-Amz-Signature=" + sig

	return signedURL.String()
}

func (s signer) scope(t time.Time) string {
	return t.Format(scopeDateFormat) + "/" + s.region + "/" + s.service + "/aws4_request"
}

func (s signer) signature(t time.Time, sts string) string {
	k := hmacSHA256([]byte("AWS4"+s.secret.Reveal()), t.Format(scopeDateFormat))
	k = hmacSHA256(k, s.region)
	k = hmacSHA256(k, s.service)
	k = hmacSHA256(k, "aws4_request")

	return hex.EncodeToString(hmacSHA256(k, sts))
}

func stringToSign(t time.Time, scope, creq string) string {
	return algorithm + "\n" + t.Format(amzDateFormat) + "\n" + scope + "\n" + sha256Hex([]byte(creq))
}

// canonicalRequest returns the canonical request and the signed header
// list (lowercase, sorted, ';'-joined).
func canonicalRequest(req *http.Request, payloadHash string) (creq, signed string) {
	host := req.Host
	if host == "" {
		host = req.URL.Host
	}

	headers := map[string]string{"host": trimHeader(host)}
	for k, vs := range req.Header {
		lk := strings.ToLower(k)
		if unsignedHeaders[lk] {
			continue
		}

		vals := make([]string, len(vs))
		for i, v := range vs {
			vals[i] = trimHeader(v)
		}

		headers[lk] = strings.Join(vals, ",")
	}

	names := make([]string, 0, len(headers))
	for k := range headers {
		names = append(names, k)
	}

	slices.Sort(names)

	var ch strings.Builder
	for _, k := range names {
		ch.WriteString(k + ":" + headers[k] + "\n")
	}

	signed = strings.Join(names, ";")

	q, _ := url.ParseQuery(req.URL.RawQuery)

	creq = req.Method + "\n" +
		canonicalURI(req.URL.Path) + "\n" +
		canonicalQuery(q) + "\n" +
		ch.String() + "\n" +
		signed + "\n" +
		payloadHash

	return creq, signed
}

// canonicalURI encodes every path segment once; "/" separators are kept.
func canonicalURI(p string) string {
	if p == "" {
		return "/"
	}

	return encodePath(p)
}

func encodePath(p string) string {
	segs := strings.Split(p, "/")
	for i, s := range segs {
		segs[i] = uriEncode(s)
	}

	return strings.Join(segs, "/")
}

// canonicalQuery sorts by key, then value, with every key and value
// URI-encoded (spaces as %20, never '+').
func canonicalQuery(q url.Values) string {
	type pair struct{ k, v string }

	var pairs []pair

	for k, vs := range q {
		for _, v := range vs {
			pairs = append(pairs, pair{uriEncode(k), uriEncode(v)})
		}
	}

	slices.SortFunc(pairs, func(a, b pair) int {
		if c := strings.Compare(a.k, b.k); c != 0 {
			return c
		}

		return strings.Compare(a.v, b.v)
	})

	parts := make([]string, len(pairs))
	for i, p := range pairs {
		parts[i] = p.k + "=" + p.v
	}

	return strings.Join(parts, "&")
}

// uriEncode is the SigV4 UriEncode: every byte except the unreserved
// A-Z a-z 0-9 - . _ ~ becomes %XX (uppercase hex).
func uriEncode(s string) string {
	const hexDigits = "0123456789ABCDEF"

	var b strings.Builder

	b.Grow(len(s))

	for i := range len(s) {
		c := s[i]
		if isUnreserved(c) {
			b.WriteByte(c)
			continue
		}

		b.WriteByte('%')
		b.WriteByte(hexDigits[c>>4])
		b.WriteByte(hexDigits[c&0x0f])
	}

	return b.String()
}

func isUnreserved(c byte) bool {
	return 'A' <= c && c <= 'Z' || 'a' <= c && c <= 'z' || '0' <= c && c <= '9' ||
		c == '-' || c == '.' || c == '_' || c == '~'
}

// trimHeader trims and collapses runs of spaces, as the spec requires.
func trimHeader(v string) string { return strings.Join(strings.Fields(v), " ") }

func hmacSHA256(key []byte, data string) []byte {
	h := hmac.New(sha256.New, key)
	h.Write([]byte(data))

	return h.Sum(nil)
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
