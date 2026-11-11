package web

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
)

func TestPeerIP(t *testing.T) {
	t.Parallel()

	tests := []struct {
		remote string
		want   netip.Addr
	}{
		{"203.0.113.7:1234", netip.MustParseAddr("203.0.113.7")},
		{"[2001:db8::1]:443", netip.MustParseAddr("2001:db8::1")},
		{"[::ffff:203.0.113.7]:80", netip.MustParseAddr("203.0.113.7")},
		{"203.0.113.7", netip.MustParseAddr("203.0.113.7")},
		{"@", netip.Addr{}},
		{"", netip.Addr{}},
		{"[fe80::1%eth0]:80", netip.Addr{}},
		{"example.com:80", netip.Addr{}},
	}

	for _, tt := range tests {
		r := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
		r.RemoteAddr = tt.remote

		if got := PeerIP(r); got != tt.want {
			t.Errorf("PeerIP(%q) = %v, want %v", tt.remote, got, tt.want)
		}
	}
}
