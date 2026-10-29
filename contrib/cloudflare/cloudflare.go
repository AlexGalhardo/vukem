// Package cloudflare integrates an app served behind Cloudflare: the real
// client IP from CF-Connecting-IP, trusted only from Cloudflare's published
// ranges, and an origin lock that refuses requests that bypass Cloudflare.
//
// Real client IP, with CLIENT_IP_HEADER=CF-Connecting-IP in the
// environment:
//
//	app, err := vukem.New(vukem.WithTrustedProxies(cloudflare.Ranges()...), ...)
//
// The ranges are embedded (https://www.cloudflare.com/ips-v4 and
// https://www.cloudflare.com/ips-v6, read 2026-10-29) and change with module
// upgrades; FetchRanges lets a scheduled job check that they are current.
// This only works when the TCP peer of the app is Cloudflare itself: behind
// another proxy (a platform load balancer), the peer is that proxy.
//
// See https://github.com/AlexGalhardo/vukem/blob/main/docs/cloudflare.md.
package cloudflare

import (
	"bufio"
	"bytes"
	"context"
	_ "embed"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"slices"
)

// ClientIPHeader is the header Cloudflare sets to the visitor's IP; set
// CLIENT_IP_HEADER to it.
const ClientIPHeader = "CF-Connecting-IP"

// Cloudflare's published range lists.
const (
	IPv4URL = "https://www.cloudflare.com/ips-v4"
	IPv6URL = "https://www.cloudflare.com/ips-v6"
)

var (
	//go:embed ips-v4.txt
	ipsV4 []byte
	//go:embed ips-v6.txt
	ipsV6 []byte
)

// maxListSize bounds a downloaded range list.
const maxListSize = 64 << 10

// Ranges returns Cloudflare's IPv4 and IPv6 ranges embedded in this module.
func Ranges() []netip.Prefix {
	v4, err := parseRanges(ipsV4)
	if err != nil {
		panic(err) // the embedded files are tested
	}

	v6, err := parseRanges(ipsV6)
	if err != nil {
		panic(err)
	}

	return append(v4, v6...)
}

// FetchRanges downloads the current ranges from IPv4URL and IPv6URL (or
// urls, when given) with client, so a scheduled job can report when the
// embedded list is out of date. A list that does not parse is an error.
func FetchRanges(ctx context.Context, client *http.Client, urls ...string) ([]netip.Prefix, error) {
	if len(urls) == 0 {
		urls = []string{IPv4URL, IPv6URL}
	}

	var out []netip.Prefix

	for _, u := range urls {
		got, err := fetchList(ctx, client, u)
		if err != nil {
			return nil, err
		}

		out = append(out, got...)
	}

	return out, nil
}

func fetchList(ctx context.Context, client *http.Client, url string) ([]netip.Prefix, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return nil, err
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("cloudflare: fetch %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("cloudflare: fetch %s: status %d", url, resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxListSize+1))
	if err != nil {
		return nil, fmt.Errorf("cloudflare: fetch %s: %w", url, err)
	}

	if len(body) > maxListSize {
		return nil, fmt.Errorf("cloudflare: fetch %s: list over %d bytes", url, maxListSize)
	}

	ranges, err := parseRanges(body)
	if err != nil {
		return nil, fmt.Errorf("cloudflare: fetch %s: %w", url, err)
	}

	return ranges, nil
}

// parseRanges reads one CIDR per line; blank lines are skipped and an
// empty list is an error, so a broken download never trusts nothing or
// everything silently.
func parseRanges(b []byte) ([]netip.Prefix, error) {
	var out []netip.Prefix

	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		line := string(bytes.TrimSpace(sc.Bytes()))
		if line == "" {
			continue
		}

		p, err := netip.ParsePrefix(line)
		if err != nil {
			return nil, fmt.Errorf("cloudflare: range %q: %w", line, err)
		}

		if p.Bits() == 0 {
			return nil, fmt.Errorf("cloudflare: range %q covers every address", line)
		}

		out = append(out, p.Masked())
	}

	if err := sc.Err(); err != nil {
		return nil, err
	}

	if len(out) == 0 {
		return nil, errors.New("cloudflare: empty range list")
	}

	return out, nil
}

// OriginLock returns middleware that answers 403 to every request whose
// TCP peer is not in Cloudflare's ranges or in allow (for example the
// loopback or platform network a health check comes from), so visitors
// cannot bypass Cloudflare's WAF and rate limits by calling the origin
// directly. It reads only the peer address, never a header.
func OriginLock(allow ...netip.Prefix) func(http.Handler) http.Handler {
	trusted := append(Ranges(), allow...)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !peerIn(r, trusted) {
				http.Error(w, http.StatusText(http.StatusForbidden), http.StatusForbidden)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// FromCloudflare reports whether the TCP peer of r is in Cloudflare's
// ranges.
func FromCloudflare(r *http.Request) bool { return peerIn(r, Ranges()) }

func peerIn(r *http.Request, ranges []netip.Prefix) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}

	addr, err := netip.ParseAddr(host)
	if err != nil {
		return false
	}

	addr = addr.Unmap().WithZone("")

	return slices.ContainsFunc(ranges, func(p netip.Prefix) bool { return p.Contains(addr) })
}
