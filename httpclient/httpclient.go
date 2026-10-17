// Package httpclient builds outbound HTTP clients for URLs that users
// control (webhooks, link previews, avatars by URL). Safe refuses to
// connect to loopback, private, link-local (cloud metadata), multicast and
// other internal addresses. The check runs on the resolved IP at connect
// time, so DNS rebinding and redirects to internal hosts are blocked too.
//
//	resp, err := httpclient.Safe(httpclient.Options{}).Get(userURL)
//
// [Options] bound the timeout, redirects and body size and may allow extra
// prefixes; blocked destinations fail with [ErrBlockedAddress]. [Get] is
// the one-call form and [Allowed] the address check itself.
//
// See https://github.com/AlexGalhardo/vukem/blob/main/docs/security.md.
package httpclient

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"syscall"
	"time"
)

// Options tune Safe.
type Options struct {
	// Timeout bounds the whole request (default 10s).
	Timeout time.Duration
	// MaxBody bounds response bodies; reads past it fail (default 10 MiB).
	MaxBody int64
	// MaxRedirects (default 5).
	MaxRedirects int
	// Allow lists extra prefixes that may be reached (e.g. an internal
	// service network). Use sparingly.
	Allow []netip.Prefix
}

// ErrBlockedAddress is returned when the destination is an internal address.
var ErrBlockedAddress = errors.New("httpclient: destination address is not allowed")

// ErrBodyTooLarge is returned by reads past Options.MaxBody.
var ErrBodyTooLarge = errors.New("httpclient: response body too large")

var blocked = mustPrefixes( //nolint:gochecknoglobals // read-only table
	"0.0.0.0/8", "10.0.0.0/8", "100.64.0.0/10", "127.0.0.0/8", "169.254.0.0/16", "172.16.0.0/12",
	"192.0.0.0/24", "192.0.2.0/24", "192.168.0.0/16", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24",
	"224.0.0.0/4", "240.0.0.0/4", "255.255.255.255/32",
	"192.88.99.0/24",
	"::/128", "::1/128", "64:ff9b::/96", "100::/64", "2001:db8::/32", "fc00::/7", "fe80::/10", "ff00::/8",
	// IPv6 forms that embed or translate an IPv4 address, which a NAT64,
	// 6to4 or Teredo gateway may route to internal hosts: IPv4-compatible,
	// IPv4-translated, local-use NAT64, Teredo and 6to4; plus deprecated
	// site-local.
	"::/96", "::ffff:0:0:0/96", "64:ff9b:1::/48", "2001::/32", "2002::/16", "fec0::/10",
)

func mustPrefixes(s ...string) []netip.Prefix {
	out := make([]netip.Prefix, len(s))
	for i, p := range s {
		out[i] = netip.MustParsePrefix(p)
	}

	return out
}

// Allowed reports whether ip may be reached with allow as extra prefixes.
func Allowed(ip netip.Addr, allow []netip.Prefix) bool {
	ip = ip.Unmap() // ::ffff:127.0.0.1 is 127.0.0.1

	for _, p := range allow {
		if p.Contains(ip) {
			return true
		}
	}

	if !ip.IsValid() || ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsMulticast() || ip.IsUnspecified() || ip.IsInterfaceLocalMulticast() {
		return false
	}

	for _, p := range blocked {
		if p.Contains(ip) {
			return false
		}
	}

	return true
}

// Safe returns a client that only reaches public addresses over http(s).
func Safe(o Options) *http.Client {
	if o.Timeout <= 0 {
		o.Timeout = 10 * time.Second
	}

	if o.MaxBody <= 0 {
		o.MaxBody = 10 << 20
	}

	if o.MaxRedirects <= 0 {
		o.MaxRedirects = 5
	}

	dialer := &net.Dialer{
		Timeout: o.Timeout,
		// Control sees the resolved address of every connection attempt.
		Control: func(_, address string, _ syscall.RawConn) error {
			ap, err := netip.ParseAddrPort(address)
			if err != nil || !Allowed(ap.Addr(), o.Allow) {
				return fmt.Errorf("%w: %s", ErrBlockedAddress, address)
			}

			return nil
		},
	}

	transport := &http.Transport{
		Proxy:                 nil, // a proxy would hide the real destination
		DialContext:           dialer.DialContext,
		ForceAttemptHTTP2:     true,
		TLSHandshakeTimeout:   o.Timeout,
		ResponseHeaderTimeout: o.Timeout,
		MaxIdleConns:          10,
		IdleConnTimeout:       30 * time.Second,
	}

	return &http.Client{
		Timeout:   o.Timeout,
		Transport: &limitTransport{rt: transport, max: o.MaxBody},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= o.MaxRedirects {
				return fmt.Errorf("httpclient: stopped after %d redirects", o.MaxRedirects)
			}

			if req.URL.Scheme != "http" && req.URL.Scheme != "https" {
				return fmt.Errorf("httpclient: redirect to %q scheme refused", req.URL.Scheme)
			}

			return nil
		},
	}
}

type limitTransport struct {
	rt  http.RoundTripper
	max int64
}

func (t *limitTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Scheme != "http" && req.URL.Scheme != "https" {
		return nil, fmt.Errorf("httpclient: scheme %q refused", req.URL.Scheme)
	}

	resp, err := t.rt.RoundTrip(req)
	if err != nil {
		return nil, err
	}

	if resp.ContentLength > t.max {
		_ = resp.Body.Close()
		return nil, ErrBodyTooLarge
	}

	resp.Body = &limitedBody{rc: resp.Body, left: t.max}

	return resp, nil
}

type limitedBody struct {
	rc   io.ReadCloser
	left int64
}

func (b *limitedBody) Read(p []byte) (int, error) {
	if b.left <= 0 {
		// One more byte decides between a clean EOF and an oversize body.
		var one [1]byte
		if n, _ := b.rc.Read(one[:]); n > 0 {
			return 0, ErrBodyTooLarge
		}

		return 0, io.EOF
	}

	if int64(len(p)) > b.left {
		p = p[:b.left]
	}

	n, err := b.rc.Read(p)
	b.left -= int64(n)

	return n, err
}

func (b *limitedBody) Close() error { return b.rc.Close() }

// Get is a convenience for Safe(Options{}).Get with a context.
func Get(ctx context.Context, rawURL string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, http.NoBody)
	if err != nil {
		return nil, err
	}

	return Safe(Options{}).Do(req)
}
