package s3

import (
	"bytes"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/AlexGalhardo/vukem/config"
)

// testdata/sigv4 holds cases of the AWS Signature Version 4 test suite
// (Apache-2.0, see testdata/sigv4/NOTICE), copied from botocore's
// tests/unit/auth/aws4_testsuite.
func TestSigV4TestSuite(t *testing.T) {
	t.Parallel()

	s := signer{
		accessKey: "AKIDEXAMPLE",
		secret:    config.NewSecret("wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY"),
		region:    "us-east-1",
		service:   "service",
	}
	now := time.Date(2015, 8, 30, 12, 36, 0, 0, time.UTC)

	dirs, err := os.ReadDir(filepath.Join("testdata", "sigv4"))
	if err != nil {
		t.Fatal(err)
	}

	cases := 0

	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}

		cases++
		name := d.Name()

		t.Run(name, func(t *testing.T) {
			t.Parallel()

			read := func(ext string) string {
				b, err := os.ReadFile(filepath.Join("testdata", "sigv4", name, name+"."+ext))
				if err != nil {
					t.Fatal(err)
				}

				return strings.ReplaceAll(string(b), "\r\n", "\n")
			}

			req, body := parseVectorRequest(t, read("req"))
			hash := sha256Hex(body)

			creq, _ := canonicalRequest(req, hash)
			if want := read("creq"); creq != want {
				t.Errorf("canonical request = %q, want %q", creq, want)
			}

			sts := stringToSign(now, s.scope(now), creq)
			if want := read("sts"); sts != want {
				t.Errorf("string to sign = %q, want %q", sts, want)
			}

			// The suite's requests already carry X-Amz-Date; sign sets the same value.
			s.sign(req, hash, now)

			if got, want := req.Header.Get("Authorization"), read("authz"); got != want {
				t.Errorf("Authorization = %q, want %q", got, want)
			}
		})
	}

	if cases < 10 {
		t.Fatalf("found %d vectors, want at least 10", cases)
	}
}

// parseVectorRequest reads the suite's raw HTTP format. http.ReadRequest
// cannot be used: request lines hold raw UTF-8 and unencoded queries.
func parseVectorRequest(t *testing.T, raw string) (*http.Request, []byte) {
	t.Helper()

	head, body, _ := strings.Cut(raw, "\n\n")
	lines := strings.Split(head, "\n")

	method, rest, _ := strings.Cut(lines[0], " ")
	target := rest[:strings.LastIndex(rest, " ")]
	p, rawQuery, _ := strings.Cut(target, "?")

	req := &http.Request{
		Method: method,
		URL:    &url.URL{Path: p, RawQuery: rawQuery},
		Header: http.Header{},
	}

	for _, l := range lines[1:] {
		k, v, ok := strings.Cut(l, ":")
		if !ok {
			t.Fatalf("bad header line %q", l)
		}

		if strings.EqualFold(k, "host") {
			req.Host = v
			continue
		}

		req.Header.Add(k, v)
	}

	return req, []byte(body)
}

// The S3 examples from "Signature Calculations for the Authorization
// Header" and "Authenticating Requests: Using Query Parameters" in the
// Amazon S3 API reference.
func TestSigV4S3Examples(t *testing.T) {
	t.Parallel()

	s := signer{
		accessKey: "AKIAIOSFODNN7EXAMPLE",
		secret:    config.NewSecret("wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"),
		region:    "us-east-1",
		service:   "s3",
	}
	now := time.Date(2013, 5, 24, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		name   string
		method string
		path   string
		query  string
		header http.Header
		body   []byte
		sig    string
	}{
		{
			name: "GET object", method: http.MethodGet, path: "/test.txt",
			header: http.Header{"Range": {"bytes=0-9"}},
			sig:    "f0e8bdb87c964420e857bd35b5d6ed310bd44f0170aba48dd91039c6036bdb41",
		},
		{
			name: "PUT object", method: http.MethodPut, path: "/test$file.text",
			header: http.Header{
				"Date":                {"Fri, 24 May 2013 00:00:00 GMT"},
				"X-Amz-Storage-Class": {"REDUCED_REDUNDANCY"},
			},
			body: []byte("Welcome to Amazon S3."),
			sig:  "98ad721746da40c64f1a55b78f14c238d841ea1380cd77a1b5971af0ece108bd",
		},
		{
			name: "GET bucket lifecycle", method: http.MethodGet, path: "/", query: "lifecycle",
			sig: "fea454ca298b7da1c68078a5d1bdbfbbe0d65c699e0f91ac7a200a0136783543",
		},
		{
			name: "list objects", method: http.MethodGet, path: "/", query: "max-keys=2&prefix=J",
			sig: "34b48302e7b5fa45bde8084f4b7868a86f0a534bc59db6670ed5711ef69dc6f7",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			req := &http.Request{
				Method: tt.method,
				URL:    &url.URL{Path: tt.path, RawQuery: tt.query},
				Host:   "examplebucket.s3.amazonaws.com",
				Header: tt.header.Clone(),
			}
			if req.Header == nil {
				req.Header = http.Header{}
			}

			hash := sha256Hex(tt.body)
			req.Header.Set("X-Amz-Content-Sha256", hash)
			s.sign(req, hash, now)

			got := req.Header.Get("Authorization")
			if !strings.HasSuffix(got, "Signature="+tt.sig) {
				t.Errorf("Authorization = %q, want signature %s", got, tt.sig)
			}
		})
	}

	t.Run("presigned GET", func(t *testing.T) {
		t.Parallel()

		u := &url.URL{Scheme: "https", Host: "examplebucket.s3.amazonaws.com", Path: "/test.txt"}
		got := s.presign(http.MethodGet, u, now, 24*time.Hour)

		want := "https://examplebucket.s3.amazonaws.com/test.txt" +
			"?X-Amz-Algorithm=AWS4-HMAC-SHA256" +
			"&X-Amz-Credential=AKIAIOSFODNN7EXAMPLE%2F20130524%2Fus-east-1%2Fs3%2Faws4_request" +
			"&X-Amz-Date=20130524T000000Z&X-Amz-Expires=86400&X-Amz-SignedHeaders=host" +
			"&X-Amz-Signature=aeeed9bbccd4d02ee5c0109b86d86835f995330da4c265957d157751f604d404"
		if got != want {
			t.Errorf("presign = %s, want %s", got, want)
		}
	})
}

func TestURIEncode(t *testing.T) {
	t.Parallel()

	tests := []struct{ in, want string }{
		{"abcXYZ019-._~", "abcXYZ019-._~"},
		{"a b+c", "a%20b%2Bc"},
		{"ação/é", "a%C3%A7%C3%A3o%2F%C3%A9"},
		{"*$&=", "%2A%24%26%3D"},
	}

	for _, tt := range tests {
		if got := uriEncode(tt.in); got != tt.want {
			t.Errorf("uriEncode(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}

	if got := encodePath("/bucket/a b/c+d.txt"); got != "/bucket/a%20b/c%2Bd.txt" {
		t.Errorf("encodePath = %q", got)
	}
}

func TestSignAddsSessionToken(t *testing.T) {
	t.Parallel()

	s := signer{
		accessKey: "AK", secret: config.NewSecret("SK"), token: config.NewSecret("TOKEN"),
		region: "us-east-1", service: "s3",
	}
	req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, "https://b.s3.amazonaws.com/k", http.NoBody)
	s.sign(req, emptySHA256, time.Now())

	if req.Header.Get("X-Amz-Security-Token") != "TOKEN" ||
		!strings.Contains(req.Header.Get("Authorization"), "x-amz-security-token") {
		t.Errorf("session token not signed: %v", req.Header)
	}

	u := s.presign(http.MethodGet, req.URL, time.Now(), time.Minute)
	if !strings.Contains(u, "X-Amz-Security-Token=TOKEN") {
		t.Errorf("presign without token: %s", u)
	}

	if !bytes.Contains([]byte(u), []byte("X-Amz-Signature=")) {
		t.Errorf("presign without signature: %s", u)
	}
}
