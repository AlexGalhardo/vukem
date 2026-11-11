package httpclient

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func okTransport() http.RoundTripper {
	return RoundTripperFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody}, nil
	})
}

func hostRequest(i int) *http.Request {
	return httptest.NewRequest(http.MethodGet, fmt.Sprintf("http://host%d.test/", i), http.NoBody)
}

func TestThrottleEvictsIdleHosts(t *testing.T) {
	t.Parallel()

	th := &throttle{next: okTransport(), limit: Limit{PerSecond: 1e9}, hosts: map[string]*hostLimit{}}

	for i := range maxHosts + 100 {
		resp, err := th.roundTrip(hostRequest(i))
		if err != nil {
			t.Fatal(err)
		}

		_ = resp.Body.Close()
	}

	if n := len(th.hosts); n > maxHosts {
		t.Errorf("throttle tracks %d hosts, want at most %d", n, maxHosts)
	}
}

func TestThrottleKeepsBusyHosts(t *testing.T) {
	t.Parallel()

	th := &throttle{next: okTransport(), limit: Limit{InFlight: 1}, hosts: map[string]*hostLimit{}}
	busy := th.host("busy.test")
	busy.sem <- struct{}{}

	for i := range maxHosts + 100 {
		th.mu.Lock()
		th.host(fmt.Sprintf("host%d.test", i))
		th.mu.Unlock()
	}

	if th.hosts["busy.test"] != busy {
		t.Error("a host with a request in flight was evicted")
	}
}

func TestBreakerEvictsIdleHosts(t *testing.T) {
	t.Parallel()

	b := &breaker{
		next:  okTransport(),
		opts:  BreakerOptions{Failures: 3, Cooldown: time.Minute},
		hosts: map[string]*circuit{},
	}

	for i := range maxHosts + 100 {
		resp, err := b.roundTrip(hostRequest(i))
		if err != nil {
			t.Fatal(err)
		}

		_ = resp.Body.Close()
	}

	if n := len(b.hosts); n > maxHosts {
		t.Errorf("breaker tracks %d hosts, want at most %d", n, maxHosts)
	}
}

func TestBreakerKeepsOpenCircuits(t *testing.T) {
	t.Parallel()

	b := &breaker{
		next:  okTransport(),
		opts:  BreakerOptions{Failures: 1, Cooldown: time.Minute},
		hosts: map[string]*circuit{},
	}
	open := &circuit{failures: 1, openUntil: time.Now().Add(time.Minute)}
	b.hosts["open.test"] = open

	for i := range maxHosts + 100 {
		resp, err := b.roundTrip(hostRequest(i))
		if err != nil {
			t.Fatal(err)
		}

		_ = resp.Body.Close()
	}

	if b.hosts["open.test"] != open {
		t.Error("an open circuit was evicted")
	}
}
