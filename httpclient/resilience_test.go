package httpclient_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/AlexGalhardo/vukem/httpclient"
)

// reply is a canned response: a status and an optional Retry-After.
type reply struct {
	status     int
	retryAfter string
}

func (r reply) fake() (*http.Response, error) {
	h := http.Header{}
	if r.retryAfter != "" {
		h.Set("Retry-After", r.retryAfter)
	}

	return &http.Response{StatusCode: r.status, Header: h, Body: io.NopCloser(strings.NewReader("body"))}, nil
}

// do sends one request through rt and returns the status and the body,
// closing it.
func do(t *testing.T, rt http.RoundTripper, method, url string) (int, string, error) {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), method, url, strings.NewReader("payload"))
	if err != nil {
		t.Fatal(err)
	}

	resp, err := rt.RoundTrip(req)
	if err != nil {
		return 0, "", err
	}

	defer resp.Body.Close()

	b, _ := io.ReadAll(resp.Body)

	return resp.StatusCode, string(b), nil
}

func TestThrottleSpacesRequestsPerHost(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		start := time.Now()

		var (
			mu  sync.Mutex
			got []string
		)

		rt := httpclient.Throttle(httpclient.Limit{PerSecond: 4})(
			httpclient.RoundTripperFunc(func(req *http.Request) (*http.Response, error) {
				mu.Lock()
				got = append(got, req.URL.Host+" "+time.Since(start).String())
				mu.Unlock()

				return reply{200, ""}.fake()
			}))

		for _, u := range []string{"https://a.test/1", "https://a.test/2", "https://b.test/1", "https://a.test/3"} {
			if _, _, err := do(t, rt, "GET", u); err != nil {
				t.Fatal(err)
			}
		}

		want := []string{"a.test 0s", "a.test 250ms", "b.test 250ms", "a.test 500ms"}
		if diff := cmp.Diff(want, got); diff != "" {
			t.Errorf("request times (-want +got):\n%s", diff)
		}
	})
}

// blockingBody blocks reads until release is closed.
type blockingBody struct{ release chan struct{} }

func (b blockingBody) Read([]byte) (int, error) { <-b.release; return 0, io.EOF }
func (blockingBody) Close() error               { return nil }

func TestThrottleBoundsInFlightUntilBodyClose(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var calls atomic.Int32

		release := make(chan struct{})
		rt := httpclient.Throttle(httpclient.Limit{InFlight: 2})(
			httpclient.RoundTripperFunc(func(*http.Request) (*http.Response, error) {
				calls.Add(1)
				return &http.Response{StatusCode: http.StatusOK, Body: blockingBody{release}}, nil
			}))

		var wg sync.WaitGroup
		for range 3 {
			wg.Go(func() { _, _, _ = do(t, rt, "GET", "https://a.test/") })
		}

		synctest.Wait()

		if n := calls.Load(); n != 2 {
			t.Fatalf("calls with two open bodies = %d, want 2", n)
		}

		close(release)
		wg.Wait()

		if n := calls.Load(); n != 3 {
			t.Errorf("calls after the bodies closed = %d, want 3", n)
		}
	})
}

func TestRetry(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		method    string
		responses []reply
		wantCalls int
		wantCode  int
		wantWait  time.Duration
	}{
		{
			"retry-after then ok",
			"GET",
			[]reply{{503, "2"}, {200, ""}},
			2,
			200,
			2 * time.Second,
		},
		{"429 retried", "PUT", []reply{{429, "1"}, {201, ""}}, 2, 201, time.Second},
		{"POST not retried", "POST", []reply{{503, "1"}}, 1, 503, 0},
		{"404 not retried", "GET", []reply{{404, ""}}, 1, 404, 0},
		{"retry-after beyond Max returned", "GET", []reply{{503, "3600"}}, 1, 503, 0},
		{"attempts exhausted", "GET", []reply{
			{502, "0"}, {502, "0"}, {502, "0"},
		}, 3, 502, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			synctest.Test(t, func(t *testing.T) {
				var calls int

				rt := httpclient.Retry(httpclient.RetryPolicy{Max: time.Minute})(
					httpclient.RoundTripperFunc(func(req *http.Request) (*http.Response, error) {
						if b, _ := io.ReadAll(req.Body); string(b) != "payload" {
							t.Errorf("attempt %d body = %q, want the replayed payload", calls+1, b)
						}

						calls++

						return tt.responses[calls-1].fake()
					}))

				start := time.Now()

				code, body, err := do(t, rt, tt.method, "https://a.test/")
				if err != nil {
					t.Fatal(err)
				}

				if calls != tt.wantCalls || code != tt.wantCode || time.Since(start) != tt.wantWait {
					t.Errorf("Retry = %d calls, status %d after %v; want %d, %d after %v",
						calls, code, time.Since(start), tt.wantCalls, tt.wantCode, tt.wantWait)
				}

				if body != "body" {
					t.Errorf("final body = %q, want it unread", body)
				}
			})
		})
	}
}

func TestRetryStopsOnContextAndBlockedAddress(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var calls int

		rt := httpclient.Retry(httpclient.RetryPolicy{})(
			httpclient.RoundTripperFunc(func(*http.Request) (*http.Response, error) {
				calls++
				return nil, httpclient.ErrBlockedAddress
			}))

		if _, _, err := do(
			t,
			rt,
			"GET",
			"https://a.test/",
		); !errors.Is(err, httpclient.ErrBlockedAddress) ||
			calls != 1 {
			t.Errorf("blocked address: err %v after %d calls, want no retry", err, calls)
		}

		ctx, cancel := context.WithCancel(t.Context())
		slow := httpclient.Retry(httpclient.RetryPolicy{})(
			httpclient.RoundTripperFunc(func(*http.Request) (*http.Response, error) {
				cancel()
				return reply{503, "5"}.fake()
			}))

		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://a.test/", http.NoBody)
		if resp, err := slow.RoundTrip(req); !errors.Is(err, context.Canceled) {
			_ = resp.Body.Close()
			t.Errorf("cancelled during backoff: err = %v, want context.Canceled", err)
		}
	})
}

func TestBreakerOpensAndRecovers(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var (
			calls int
			fail  = true
		)

		rt := httpclient.Breaker(httpclient.BreakerOptions{Failures: 3, Cooldown: 10 * time.Second})(
			httpclient.RoundTripperFunc(func(*http.Request) (*http.Response, error) {
				calls++
				if fail {
					return reply{500, ""}.fake()
				}

				return reply{200, ""}.fake()
			}))

		for range 3 {
			_, _, _ = do(t, rt, "GET", "https://a.test/")
		}

		if _, _, err := do(t, rt, "GET", "https://a.test/"); !errors.Is(err, httpclient.ErrCircuitOpen) || calls != 3 {
			t.Fatalf("after 3 failures: err %v, %d calls; want ErrCircuitOpen without a call", err, calls)
		}

		if _, _, err := do(t, rt, "GET", "https://b.test/"); err != nil {
			t.Fatalf("other host: %v, want its own circuit", err)
		}

		time.Sleep(10 * time.Second)

		if code, _, err := do(t, rt, "GET", "https://a.test/"); err != nil || code != 500 {
			t.Fatalf("trial after cooldown = %d, %v; want the failing response", code, err)
		}

		if _, _, err := do(t, rt, "GET", "https://a.test/"); !errors.Is(err, httpclient.ErrCircuitOpen) {
			t.Fatalf("after a failed trial: err %v, want ErrCircuitOpen", err)
		}

		time.Sleep(10 * time.Second)

		fail = false

		for range 2 {
			if code, _, err := do(t, rt, "GET", "https://a.test/"); err != nil || code != 200 {
				t.Fatalf("after a good trial = %d, %v; want the circuit closed", code, err)
			}
		}
	})
}
