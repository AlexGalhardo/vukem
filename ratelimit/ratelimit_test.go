package ratelimit

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"testing/synctest"
	"time"

	"github.com/AlexGalhardo/vukem/web"
)

func TestMemoryGCRA(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		m := NewMemory(3, time.Minute) // one token every 20s, burst 3
		ctx := t.Context()

		for i, wantRemaining := range []int{2, 1, 0} {
			res, _ := m.Allow(ctx, "a")
			if !res.Allowed || res.Remaining != wantRemaining || res.Limit != 3 {
				t.Fatalf("request %d = %+v", i, res)
			}
		}

		res, _ := m.Allow(ctx, "a")
		if res.Allowed || res.RetryAfter != 20*time.Second || res.ResetAfter != time.Minute {
			t.Fatalf("4th = %+v", res)
		}

		// Other keys have their own bucket.
		if res, _ := m.Allow(ctx, "b"); !res.Allowed {
			t.Fatal("key b limited")
		}

		time.Sleep(20 * time.Second)

		if res, _ := m.Allow(ctx, "a"); !res.Allowed || res.Remaining != 0 {
			t.Fatalf("after one interval = %+v", res)
		}

		time.Sleep(time.Minute)

		if res, _ := m.Allow(ctx, "a"); !res.Allowed || res.Remaining != 2 {
			t.Fatalf("after a full window = %+v", res)
		}
	})
}

func TestMemoryPrunesFullBuckets(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		m := NewMemory(0, time.Second) // limit is at least 1

		for i := range 1023 {
			_, _ = m.Allow(t.Context(), strconv.Itoa(i))
		}

		time.Sleep(2 * time.Second)
		_, _ = m.Allow(t.Context(), "last") // 1024th call prunes

		if n := len(m.tat); n != 1 {
			t.Fatalf("keys after prune = %d", n)
		}
	})
}

type failing struct{}

func (failing) Allow(context.Context, string) (Result, error) {
	return Result{}, errors.New("store down")
}

func serve(h http.Handler, r *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r.WithContext(web.WithServices(r.Context(), &web.Services{Log: slog.New(slog.DiscardHandler)})))

	return rec
}

func TestSecurity_A06_RateLimitMiddlewareFailsClosed(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
		h := Middleware(NewMemory(2, time.Minute), ByToken)(ok)

		req := func(token, ip string) *http.Request {
			r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api", http.NoBody)
			r.RemoteAddr = ip + ":1234"
			r.Header.Set("Accept", "application/json")

			if token != "" {
				r.Header.Set("Authorization", "Bearer "+token)
			}

			return r
		}

		rec := serve(h, req("t1", "10.0.0.1"))
		if rec.Code != 204 || rec.Header().Get("RateLimit-Limit") != "2" ||
			rec.Header().Get("RateLimit-Remaining") != "1" ||
			rec.Header().Get("RateLimit-Reset") != "30" {
			t.Fatalf("first = %d %v", rec.Code, rec.Header())
		}

		serve(h, req("t1", "10.0.0.2"))

		rec = serve(h, req("t1", "10.0.0.3")) // same token, other IP: still limited
		if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") != "30" ||
			rec.Header().Get("Content-Type") != "application/problem+json" {
			t.Fatalf("limited = %d %v %s", rec.Code, rec.Header(), rec.Body.String())
		}

		if rec := serve(h, req("t2", "10.0.0.3")); rec.Code != 204 {
			t.Fatalf("other token = %d", rec.Code)
		}

		// Without a token the IP is the key.
		serve(h, req("", "10.0.0.9"))
		serve(h, req("", "10.0.0.9"))

		if rec := serve(h, req("", "10.0.0.9")); rec.Code != http.StatusTooManyRequests {
			t.Fatalf("by IP = %d", rec.Code)
		}

		// An empty key skips limiting; a failing store denies.
		skip := Middleware(NewMemory(1, time.Minute), func(*http.Request) string { return "" })(ok)
		for range 3 {
			if rec := serve(skip, req("", "10.0.0.1")); rec.Code != 204 {
				t.Fatalf("skipped = %d", rec.Code)
			}
		}

		if rec := serve(
			Middleware(failing{}, ByIP)(ok),
			req("", "10.0.0.1"),
		); rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("failing store = %d, want fail closed", rec.Code)
		}
	})
}

// The default key must not trust bearer tokens nobody verified yet: a
// client rotating made-up tokens would get a fresh bucket per request.
func TestSecurity_A06_RateLimitIgnoresUnverifiedTokens(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
		h := Middleware(NewMemory(2, time.Minute), nil)(ok)

		codes := make([]int, 0, 3)

		for i := range 3 {
			r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api", http.NoBody)
			r.RemoteAddr = "192.0.2.7:1234"
			r.Header.Set("Authorization", "Bearer random-"+strconv.Itoa(i))
			codes = append(codes, serve(h, r).Code)
		}

		if codes[2] != http.StatusTooManyRequests {
			t.Fatalf("Middleware(nil) with rotating tokens = %v, want the third request limited by IP", codes)
		}
	})
}

func TestKeys(t *testing.T) {
	t.Parallel()

	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody)
	r.RemoteAddr = "192.0.2.1:80"

	// Never "" (which skips limiting): no or an empty token keys by IP.
	for _, h := range []string{"", "Bearer ", "Bearer", "Basic abc"} {
		r.Header.Set("Authorization", h)
		if got := ByToken(r); got != "ip:192.0.2.1" {
			t.Fatalf("ByToken with Authorization %q = %q, want the IP key", h, got)
		}
	}

	r.Header.Set("Authorization", "Bearer secret-token")
	if got := ByToken(r); got == "" || got == "token:secret-token" || len(got) != len("token:")+32 {
		t.Fatalf("ByToken = %q (token must be hashed)", got)
	}
}

func TestSecurity_A06_ByIPGroupsIPv6ByPrefix(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		prefix int // web.Services.IPv6Prefix; 0 is the default /64
		peer   string
		want   string
	}{
		{name: "ipv4 unchanged", peer: "203.0.113.5:1", want: "ip:203.0.113.5"},
		{name: "ipv4-mapped is ipv4", peer: "[::ffff:203.0.113.5]:1", want: "ip:203.0.113.5"},
		{name: "ipv6 default /64", peer: "[2001:db8:1:2:aaaa::1]:1", want: "ip:2001:db8:1:2::/64"},
		{name: "same /64 other host", peer: "[2001:db8:1:2:bbbb:cccc:dddd:eeee]:1", want: "ip:2001:db8:1:2::/64"},
		{name: "other /64", peer: "[2001:db8:1:3::1]:1", want: "ip:2001:db8:1:3::/64"},
		{name: "zone dropped", peer: "[fe80::1%eth0]:1", want: "ip:fe80::/64"},
		{name: "configured /48", prefix: 48, peer: "[2001:db8:1:2::1]:1", want: "ip:2001:db8:1::/48"},
		{name: "configured /128", prefix: 128, peer: "[2001:db8::1]:1", want: "ip:2001:db8::1/128"},
		{name: "prefix ignores ipv4", prefix: 48, peer: "203.0.113.5:1", want: "ip:203.0.113.5"},
		{name: "unparsable kept", peer: "pipe", want: "ip:pipe"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody)
			r.RemoteAddr = tt.peer
			r = r.WithContext(web.WithServices(r.Context(), &web.Services{IPv6Prefix: tt.prefix}))

			if got := ByIP(r); got != tt.want {
				t.Fatalf("ByIP = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSecurity_A06_RateLimitIPv6RotationSharesBucket(t *testing.T) {
	t.Parallel()

	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	h := Middleware(NewMemory(2, time.Minute), ByIP)(ok)

	codes := make([]int, 0, 3)

	for i := range 3 {
		r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody)
		r.Header.Set("Accept", "application/json")
		r.RemoteAddr = "[2001:db8:1:2::" + strconv.Itoa(i+1) + "]:1234"
		codes = append(codes, serve(h, r).Code)
	}

	if codes[2] != http.StatusTooManyRequests {
		t.Fatalf("codes = %v: rotating addresses inside one /64 escaped the limit", codes)
	}
}

func TestIPKey(t *testing.T) {
	t.Parallel()

	tests := []struct {
		ip     string
		prefix int
		want   string
	}{
		{ip: "203.0.113.5", prefix: 64, want: "203.0.113.5"},
		{ip: "::ffff:203.0.113.5", prefix: 64, want: "203.0.113.5"},
		{ip: "2001:db8:1:2:3::4", prefix: 64, want: "2001:db8:1:2::/64"},
		{ip: "2001:db8:1:2:3::4", prefix: 1, want: "::/1"},
		{ip: "ffff:db8::1", prefix: 1, want: "8000::/1"},
		{ip: "2001:db8:1:2:3::4", prefix: 0, want: "2001:db8:1:2::/64"},
		{ip: "2001:db8:1:2:3::4", prefix: 129, want: "2001:db8:1:2::/64"},
		{ip: "2001:db8:1:2:3::4", prefix: -1, want: "2001:db8:1:2::/64"},
		{ip: "not-an-ip", prefix: 64, want: "not-an-ip"},
	}

	for _, tt := range tests {
		if got := IPKey(tt.ip, tt.prefix); got != tt.want {
			t.Errorf("IPKey(%q, %d) = %q, want %q", tt.ip, tt.prefix, got, tt.want)
		}
	}
}

func TestIPKeyIPv4DoesNotAllocate(t *testing.T) {
	if n := testing.AllocsPerRun(100, func() { _ = IPKey("203.0.113.5", 64) }); n != 0 {
		t.Fatalf("IPKey(ipv4) allocates %v times", n)
	}
}
