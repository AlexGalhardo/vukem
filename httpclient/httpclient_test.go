package httpclient

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
)

func TestSecurity_A01_SSRFAddresses(t *testing.T) {
	t.Parallel()

	for _, s := range []string{
		"127.0.0.1", "127.1.2.3", "10.0.0.5", "172.16.3.4", "192.168.1.1", "169.254.169.254", // cloud metadata
		"100.64.0.1", "0.0.0.0", "224.0.0.1", "255.255.255.255", "198.18.0.1",
		"::1", "::", "fe80::1", "fd00::1", "ff02::1", "::ffff:127.0.0.1", "::ffff:169.254.169.254", "64:ff9b::7f00:1",
		// IPv6 forms that embed or translate an IPv4 address: 6to4, local NAT64,
		// Teredo, IPv4-compatible and IPv4-translated; site-local; 6to4 relay.
		"2002:7f00:1::", "2002:a9fe:a9fe::", "64:ff9b:1::7f00:1", "2001:0:4136:e378::1", "::7f00:1", "::ffff:0:7f00:1",
		"fec0::1", "192.88.99.1",
	} {
		if Allowed(netip.MustParseAddr(s), nil) {
			t.Errorf("%s allowed", s)
		}
	}

	for _, s := range []string{"8.8.8.8", "1.1.1.1", "2606:4700:4700::1111", "151.101.1.69"} {
		if !Allowed(netip.MustParseAddr(s), nil) {
			t.Errorf("%s blocked", s)
		}
	}

	if !Allowed(netip.MustParseAddr("10.1.2.3"), []netip.Prefix{netip.MustParsePrefix("10.1.0.0/16")}) {
		t.Error("explicit allow ignored")
	}

	if Allowed(netip.Addr{}, nil) {
		t.Error("invalid address allowed")
	}
}

func TestSecurity_A01_SSRFLoopbackAndRedirect(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("internal")) }),
	)
	defer srv.Close()

	// httptest listens on loopback: blocked.
	if err := fetch(
		func() (*http.Response, error) { return Safe(Options{}).Get(srv.URL) },
	); !errors.Is(
		err,
		ErrBlockedAddress,
	) {
		t.Fatalf("loopback = %v", err)
	}

	// A hostname resolving to loopback (DNS rebinding style) is blocked
	// at connect time too.
	if err := fetch(func() (*http.Response, error) {
		return Get(
			t.Context(),
			strings.Replace(srv.URL, "127.0.0.1", "localhost", 1),
		)
	}); !errors.Is(
		err,
		ErrBlockedAddress,
	) {
		t.Fatalf("localhost = %v", err)
	}

	// An allowed server redirecting to an internal one is stopped.
	loop := netip.MustParsePrefix("127.0.0.1/32")
	redir := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://[::1]:1/", http.StatusFound)
	}))
	defer redir.Close()

	if err := fetch(
		func() (*http.Response, error) { return Safe(Options{Allow: []netip.Prefix{loop}}).Get(redir.URL) },
	); !errors.Is(
		err,
		ErrBlockedAddress,
	) {
		t.Fatalf("redirect to internal = %v", err)
	}

	for _, u := range []string{"file:///etc/passwd", "gopher://x"} {
		if err := fetch(func() (*http.Response, error) { return Safe(Options{}).Get(u) }); err == nil {
			t.Errorf("%s accepted", u)
		}
	}
}

func TestLimitsAndAllowedServer(t *testing.T) {
	t.Parallel()

	big := strings.Repeat("x", 2000)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/small":
			_, _ = w.Write([]byte("ok"))
		case "/declared":
			_, _ = w.Write([]byte(big)) // Content-Length is set
		case "/chunked":
			w.(http.Flusher).Flush() // no Content-Length
			_, _ = w.Write([]byte(big))
		case "/loop":
			http.Redirect(w, r, "/loop", http.StatusFound)
		case "/scheme":
			http.Redirect(w, r, "ftp://example.com/", http.StatusFound)
		}
	}))
	defer srv.Close()

	c := Safe(Options{Allow: []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8")}, MaxBody: 1000})

	resp, err := c.Get(srv.URL + "/small")
	if err != nil {
		t.Fatal(err)
	}

	body, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()

	if err != nil || string(body) != "ok" {
		t.Fatalf("small = %q %v", body, err)
	}

	if err := fetch(
		func() (*http.Response, error) { return c.Get(srv.URL + "/declared") },
	); !errors.Is(
		err,
		ErrBodyTooLarge,
	) {
		t.Fatalf("declared = %v", err)
	}

	resp, err = c.Get(srv.URL + "/chunked")
	if err != nil {
		t.Fatal(err)
	}

	_, err = io.ReadAll(resp.Body)
	_ = resp.Body.Close()

	if !errors.Is(err, ErrBodyTooLarge) {
		t.Fatalf("chunked = %v", err)
	}

	if err := fetch(
		func() (*http.Response, error) { return c.Get(srv.URL + "/loop") },
	); err == nil ||
		!strings.Contains(err.Error(), "redirects") {
		t.Fatalf("redirect loop = %v", err)
	}

	if err := fetch(func() (*http.Response, error) { return c.Get(srv.URL + "/scheme") }); err == nil {
		t.Fatal("redirect to ftp accepted")
	}

	// Exactly MaxBody bytes is fine.
	exact := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.(http.Flusher).Flush()
		_, _ = w.Write([]byte(strings.Repeat("y", 1000)))
	}))
	defer exact.Close()

	resp, err = c.Get(exact.URL)
	if err != nil {
		t.Fatal(err)
	}

	data, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()

	if err != nil || len(data) != 1000 {
		t.Fatalf("exact = %d %v", len(data), err)
	}

	if err := fetch(func() (*http.Response, error) { return Get(t.Context(), "://bad") }); err == nil {
		t.Fatal("bad URL accepted")
	}
}

// fetch runs a request expected to fail, closing any body.
func fetch(do func() (*http.Response, error)) error {
	resp, err := do()
	if resp != nil {
		_ = resp.Body.Close()
	}

	return err
}

type countingTransport struct {
	next  http.RoundTripper
	calls *int
}

func (t countingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	*t.calls++
	return t.next.RoundTrip(req)
}

func TestSecurity_A01_SSRFWrappedTransport(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	var calls int

	wrap := func(rt http.RoundTripper) http.RoundTripper { return countingTransport{next: rt, calls: &calls} }

	// The wrapper sees the request, but the address check still runs below it.
	if err := fetch(func() (*http.Response, error) {
		return Safe(Options{WrapTransport: wrap}).Get(srv.URL)
	}); !errors.Is(err, ErrBlockedAddress) {
		t.Fatalf("wrapped Get(loopback) = %v, want ErrBlockedAddress", err)
	}

	resp, err := Safe(Options{WrapTransport: wrap, Allow: []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8")}}).
		Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}

	_ = resp.Body.Close()

	if calls != 2 {
		t.Fatalf("WrapTransport round trips = %d, want 2", calls)
	}
}
