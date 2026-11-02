package crawlers_test

import (
	"context"
	"errors"
	"net/netip"
	"sync/atomic"
	"testing"
	"time"

	"github.com/AlexGalhardo/vukem/security/crawlers"
)

type fakeResolver struct {
	ptr     map[string][]string
	forward map[string][]netip.Addr
	err     error
	calls   atomic.Int32
}

func (f *fakeResolver) LookupAddr(_ context.Context, addr string) ([]string, error) {
	f.calls.Add(1)

	if f.err != nil {
		return nil, f.err
	}

	return f.ptr[addr], nil
}

func (f *fakeResolver) LookupNetIP(_ context.Context, _, host string) ([]netip.Addr, error) {
	return f.forward[host], nil
}

func TestSecurity_A07_CrawlerNeedsForwardConfirmedReverseDNS(t *testing.T) {
	t.Parallel()

	res := &fakeResolver{
		ptr: map[string][]string{
			"66.249.66.1":  {"crawl-66-249-66-1.googlebot.com."},
			"2001:db8::1":  {"msnbot-1.search.msn.com."},
			"203.0.113.9":  {"crawl-1.googlebot.com.evil.test."},
			"203.0.113.10": {"crawl-66-249-66-1.googlebot.com."}, // PTR the attacker controls
			"203.0.113.11": {"notgooglebot.com."},
		},
		forward: map[string][]netip.Addr{
			"crawl-66-249-66-1.googlebot.com": {netip.MustParseAddr("66.249.66.1")},
			"msnbot-1.search.msn.com":         {netip.MustParseAddr("2001:db8::1")},
		},
	}
	v := crawlers.New(crawlers.Config{Resolver: res})

	for _, tc := range []struct {
		ip, want string
		ok       bool
	}{
		{ip: "66.249.66.1", want: "google", ok: true},
		{ip: "::ffff:66.249.66.1", want: "google", ok: true},
		{ip: "2001:db8::1", want: "bing", ok: true},
		{ip: "203.0.113.9"},  // domain only as a prefix
		{ip: "203.0.113.10"}, // forward lookup does not give the IP back
		{ip: "203.0.113.11"}, // suffix without the dot
		{ip: "198.51.100.1"}, // no PTR
		{ip: "not-an-ip"},
	} {
		got, ok := v.Verify(t.Context(), tc.ip)
		if got != tc.want || ok != tc.ok {
			t.Errorf("Verify(%s) = %q, %v; want %q, %v", tc.ip, got, ok, tc.want, tc.ok)
		}
	}
}

func TestVerifierCaches(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
	res := &fakeResolver{err: errors.New("timeout")}
	v := crawlers.New(crawlers.Config{Resolver: res, Now: func() time.Time { return now }})

	v.Verify(t.Context(), "66.249.66.1")
	v.Verify(t.Context(), "66.249.66.1")

	if got := res.calls.Load(); got != 1 {
		t.Fatalf("lookups = %d, want 1 (cached)", got)
	}

	// A DNS error is cached only for ErrorTTL.
	now = now.Add(crawlers.DefaultErrorTTL + time.Second)
	v.Verify(t.Context(), "66.249.66.1")

	if got := res.calls.Load(); got != 2 {
		t.Fatalf("lookups after ErrorTTL = %d, want 2", got)
	}

	// The cache stays bounded.
	small := crawlers.New(crawlers.Config{Resolver: res, MaxEntries: 2})
	for _, ip := range []string{"192.0.2.1", "192.0.2.2", "192.0.2.3", "192.0.2.1"} {
		small.Verify(t.Context(), ip)
	}
}
