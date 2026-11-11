package loadtest

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func scenarioConfig(srv *httptest.Server, cfg Config) ScenarioConfig {
	return ScenarioConfig{
		Config: cfg, BaseURL: srv.URL,
		Route: func(name string, params ...any) (string, error) {
			if name == "posts.show" && len(params) == 2 {
				return fmt.Sprintf("/posts/%v", params[1]), nil
			}

			return "", fmt.Errorf("no route %q", name)
		},
	}
}

func TestScenarioWeights(t *testing.T) {
	t.Parallel()

	srv := okServer(t, nil)

	var heavy, light atomic.Int64

	sum, err := RunScenarios(t.Context(), scenarioConfig(srv, Config{Concurrency: 8, Requests: 4000}), []Scenario{
		{Name: "heavy", Weight: 3, Do: func(context.Context, *Client) error { heavy.Add(1); return nil }},
		{Name: "light", Do: func(context.Context, *Client) error { light.Add(1); return nil }},
	})
	if err != nil {
		t.Fatal(err)
	}

	total := heavy.Load() + light.Load()
	if total != 4000 || sum.Iterations != 4000 {
		t.Fatalf("iterations: heavy=%d light=%d summary=%d, want 4000", heavy.Load(), light.Load(), sum.Iterations)
	}

	if share := float64(heavy.Load()) / float64(total); share < 0.70 || share > 0.80 {
		t.Fatalf("the weight 3 scenario ran %.1f%% of the iterations, want 70..80%%", share*100)
	}
}

func TestScenarioWeightsAreDeterministic(t *testing.T) {
	t.Parallel()

	set := newScenarioSet([]Scenario{{Name: "a", Weight: 3}, {Name: "b"}})

	for seq := range uint64(200) {
		first, second := set.pick(seq), set.pick(seq)
		if first.Name != second.Name {
			t.Fatal("the same iteration must pick the same scenario")
		}
	}
}

func TestScenarioClientKeepsCookies(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/login" {
			http.SetCookie(w, &http.Cookie{Name: "sid", Value: "abc", Path: "/"})

			return
		}

		if c, err := r.Cookie("sid"); err != nil || c.Value != "abc" {
			http.Error(w, "no session", http.StatusUnauthorized)
		}
	}))
	t.Cleanup(srv.Close)

	sum, err := RunScenarios(t.Context(), scenarioConfig(srv, Config{Concurrency: 1, Requests: 5}), []Scenario{{
		Name: "session", Do: func(ctx context.Context, c *Client) error {
			if _, err := c.Get(ctx, "/login"); err != nil {
				return err
			}

			resp, err := c.Get(ctx, "/me")
			if err != nil {
				return err
			}

			if resp.Status != http.StatusOK {
				return fmt.Errorf("status %d: the cookie was not sent back", resp.Status)
			}

			return nil
		},
	}})
	if err != nil {
		t.Fatal(err)
	}

	if sum.IterationsFailed != 0 || sum.HTTPReqs != 10 || sum.HTTPReqFailed != 0 {
		t.Fatalf("iterations failed=%d reqs=%d failed=%v", sum.IterationsFailed, sum.HTTPReqs, sum.HTTPReqFailed)
	}
}

func TestScenarioClientIsolatedPerUser(t *testing.T) {
	t.Parallel()

	var issued atomic.Int64

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := r.Cookie("sid"); err != nil {
			http.SetCookie(w, &http.Cookie{Name: "sid", Value: strconv.FormatInt(issued.Add(1), 10), Path: "/"})
		}
	}))
	t.Cleanup(srv.Close)

	_, err := RunScenarios(t.Context(), scenarioConfig(srv, Config{Concurrency: 3, Requests: 60}), []Scenario{{
		Name: "visit", Do: func(ctx context.Context, c *Client) error {
			_, err := c.Get(ctx, "/")

			return err
		},
	}})
	if err != nil {
		t.Fatal(err)
	}

	// Each of the 3 virtual users got a cookie on its first request and kept it; none shared a jar.
	if issued.Load() != 3 {
		t.Fatalf("the server issued %d cookies, want 3 (one per virtual user)", issued.Load())
	}
}

func TestScenarioResultNames(t *testing.T) {
	t.Parallel()

	srv := okServer(t, nil)

	r, err := StartScenarios(t.Context(), scenarioConfig(srv, Config{Concurrency: 1, Requests: 1}), []Scenario{{
		Name: "browse", Do: func(ctx context.Context, c *Client) error {
			u, err := c.Route("posts.show", "post", 7)
			if err != nil {
				return err
			}

			if _, err := c.Get(ctx, u); err != nil {
				return err
			}

			_, err = c.Get(ctx, "/plain?x=1&token=secret")

			return err
		},
	}})
	if err != nil {
		t.Fatal(err)
	}

	var names []string
	for _, res := range collect(t, r) {
		names = append(names, res.Name)
	}

	if len(names) != 2 || names[0] != "browse: GET posts.show" || names[1] != "browse: GET /plain" {
		t.Fatalf("names = %q", names)
	}
}

func TestScenarioErrorCountsIteration(t *testing.T) {
	t.Parallel()

	srv := okServer(t, nil)

	sum, err := RunScenarios(t.Context(), scenarioConfig(srv, Config{Concurrency: 2, Requests: 10}), []Scenario{{
		Name: "bad", Do: func(context.Context, *Client) error { return errors.New("boom") },
	}})
	if err != nil {
		t.Fatal(err)
	}

	if sum.Iterations != 10 || sum.IterationsFailed != 10 {
		t.Fatalf("iterations=%d failed=%d, want 10 and 10", sum.Iterations, sum.IterationsFailed)
	}
}

func TestScenarioPanicIsRecovered(t *testing.T) {
	t.Parallel()

	srv := okServer(t, nil)

	var calls atomic.Int64

	sum, err := RunScenarios(t.Context(), scenarioConfig(srv, Config{Concurrency: 2, Requests: 10}), []Scenario{{
		Name: "panics", Do: func(context.Context, *Client) error {
			calls.Add(1)
			panic("nope")
		},
	}})
	if err != nil {
		t.Fatal(err)
	}

	if calls.Load() != 10 || sum.IterationsFailed != 10 || sum.Errors["panic"] != 10 {
		t.Fatalf(
			"calls=%d failed=%d errors=%v: a panic must fail the iteration and let the user go on",
			calls.Load(),
			sum.IterationsFailed,
			sum.Errors,
		)
	}
}

func TestScenarioOnlyFilter(t *testing.T) {
	t.Parallel()

	srv := okServer(t, nil)

	var a, b atomic.Int64

	scenarios := []Scenario{
		{Name: "a", Do: func(context.Context, *Client) error { a.Add(1); return nil }},
		{Name: "b", Do: func(context.Context, *Client) error { b.Add(1); return nil }},
	}

	cfg := scenarioConfig(srv, Config{Concurrency: 1, Requests: 20})
	cfg.Only = []string{"a"}

	if _, err := RunScenarios(t.Context(), cfg, scenarios); err != nil {
		t.Fatal(err)
	}

	if a.Load() != 20 || b.Load() != 0 {
		t.Fatalf("a=%d b=%d, want only a", a.Load(), b.Load())
	}
}

func TestScenarioUnknownNameIsError(t *testing.T) {
	t.Parallel()

	srv := okServer(t, nil)

	cfg := scenarioConfig(srv, Config{Concurrency: 1, Requests: 1})
	cfg.Only = []string{"missing"}

	if _, err := StartScenarios(
		t.Context(),
		cfg,
		[]Scenario{{Name: "a", Do: func(context.Context, *Client) error { return nil }}},
	); err == nil {
		t.Fatal("an unknown scenario name was accepted")
	}
}

func TestScenarioConfigIsValidated(t *testing.T) {
	t.Parallel()

	srv := okServer(t, nil)
	do := func(context.Context, *Client) error { return nil }

	for name, tc := range map[string]struct {
		cfg ScenarioConfig
		sc  []Scenario
	}{
		"none":            {scenarioConfig(srv, Config{Requests: 1}), nil},
		"no name":         {scenarioConfig(srv, Config{Requests: 1}), []Scenario{{Do: do}}},
		"no function":     {scenarioConfig(srv, Config{Requests: 1}), []Scenario{{Name: "a"}}},
		"duplicate":       {scenarioConfig(srv, Config{Requests: 1}), []Scenario{{Name: "a", Do: do}, {Name: "a", Do: do}}},
		"negative weight": {scenarioConfig(srv, Config{Requests: 1}), []Scenario{{Name: "a", Weight: -1, Do: do}}},
		"remote base URL": {ScenarioConfig{BaseURL: "http://example.com"}, []Scenario{{Name: "a", Do: do}}},
	} {
		if _, err := StartScenarios(t.Context(), tc.cfg, tc.sc); err == nil {
			t.Errorf("%s: StartScenarios accepted the configuration", name)
		}
	}
}

func TestSecurity_A10_BenchScenarioClientRefusesRemoteHost(t *testing.T) {
	t.Parallel()

	srv := okServer(t, nil)

	var outside atomic.Int64

	cfg := scenarioConfig(srv, Config{Concurrency: 1, Requests: 3})
	cfg.Client = &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			if host, _, _ := net.SplitHostPort(addr); !loopbackHost(host) {
				outside.Add(1)
			}

			return (&net.Dialer{}).DialContext(ctx, network, addr)
		},
	}}

	var refused atomic.Int64

	sum, err := RunScenarios(t.Context(), cfg, []Scenario{{
		Name: "escape", Do: func(ctx context.Context, c *Client) error {
			if _, err := c.Get(ctx, "http://example.com/"); err != nil {
				refused.Add(1)

				return err
			}

			return nil
		},
	}})
	if err != nil {
		t.Fatal(err)
	}

	if refused.Load() != 3 || outside.Load() != 0 || sum.IterationsFailed != 3 {
		t.Fatalf(
			"refused=%d connections outside loopback=%d failed iterations=%d",
			refused.Load(),
			outside.Load(),
			sum.IterationsFailed,
		)
	}
}

func TestResponseBodyCappedAt1MiB(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.Copy(w, strings.NewReader(strings.Repeat("x", 2<<20)))
	}))
	t.Cleanup(srv.Close)

	var kept atomic.Int64

	sum, err := RunScenarios(t.Context(), scenarioConfig(srv, Config{Concurrency: 1, Requests: 1}), []Scenario{{
		Name: "big", Do: func(ctx context.Context, c *Client) error {
			resp, err := c.Get(ctx, "/")
			if err != nil {
				return err
			}

			kept.Store(int64(len(resp.Body)))

			return nil
		},
	}})
	if err != nil {
		t.Fatal(err)
	}

	if kept.Load() != 1<<20 || sum.DataReceived != 2<<20 {
		t.Fatalf("kept %d bytes, received %d: want 1 MiB kept and all 2 MiB read", kept.Load(), sum.DataReceived)
	}
}

func TestClientHeadersTokenSeqAndJSON(t *testing.T) {
	t.Parallel()

	var (
		mu   sync.Mutex
		seen []string
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)

		mu.Lock()
		seen = append(
			seen,
			r.Method+"|"+r.Header.Get(
				"Authorization",
			)+"|"+r.Header.Get(
				"X-A",
			)+"|"+r.Header.Get(
				"Content-Type",
			)+"|"+string(
				b,
			),
		)
		mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":7}`))
	}))
	t.Cleanup(srv.Close)

	var (
		id  int
		seq uint64
	)

	_, err := RunScenarios(t.Context(), scenarioConfig(srv, Config{Concurrency: 1, Requests: 1}), []Scenario{{
		Name: "api", Do: func(ctx context.Context, c *Client) error {
			seq = c.Seq()
			c.SetToken("tok")
			c.SetHeader("X-A", "1")

			resp, err := c.PostJSON(ctx, "/things", map[string]int{"n": 1})
			if err != nil {
				return err
			}

			var out struct{ ID int }
			if err := resp.JSON(&out); err != nil {
				return err
			}

			id = out.ID

			_, err = c.Delete(ctx, "/things/1")

			return err
		},
	}})
	if err != nil {
		t.Fatal(err)
	}

	mu.Lock()
	defer mu.Unlock()

	if id != 7 || seq != 0 || len(seen) != 2 || seen[0] != `POST|Bearer tok|1|application/json|{"n":1}` ||
		!strings.HasPrefix(seen[1], "DELETE|Bearer tok|1|") {
		t.Fatalf("id=%d seq=%d seen=%q", id, seq, seen)
	}
}

func TestScenarioOpenModelGivesEachIterationItsOwnUser(t *testing.T) {
	t.Parallel()

	var issued atomic.Int64

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := r.Cookie("sid"); err != nil {
			http.SetCookie(w, &http.Cookie{Name: "sid", Value: strconv.FormatInt(issued.Add(1), 10), Path: "/"})
		}
	}))
	t.Cleanup(srv.Close)

	cfg := scenarioConfig(srv, Config{Rate: Rate{100, time.Second}, Requests: 8})

	sum, err := RunScenarios(t.Context(), cfg, []Scenario{{
		Name: "visit", Do: func(ctx context.Context, c *Client) error {
			_, err := c.Get(ctx, "/")

			return err
		},
	}})
	if err != nil {
		t.Fatal(err)
	}

	if issued.Load() != 8 || sum.Model != "open" || sum.Iterations != 8 {
		t.Fatalf(
			"issued=%d model=%s iterations=%d: every arrival is a new visitor",
			issued.Load(),
			sum.Model,
			sum.Iterations,
		)
	}
}

func TestClientTemplatesAreBounded(t *testing.T) {
	t.Parallel()

	srv := okServer(t, nil)

	var kept int

	cfg := scenarioConfig(srv, Config{Concurrency: 1, Requests: 1})
	cfg.Route = func(_ string, params ...any) (string, error) { return fmt.Sprintf("/posts/%v", params[0]), nil }

	_, err := RunScenarios(t.Context(), cfg, []Scenario{{
		Name: "many", Do: func(_ context.Context, c *Client) error {
			for i := range 3 * maxTemplates {
				if _, err := c.Route("posts.show", i); err != nil {
					return err
				}
			}

			kept = len(c.templates)

			return nil
		},
	}})
	if err != nil {
		t.Fatal(err)
	}

	if kept != maxTemplates {
		t.Fatalf("the client remembered %d route paths, want the cap of %d", kept, maxTemplates)
	}
}

func TestClientHeadersStayOnTheBaseHost(t *testing.T) {
	t.Parallel()

	var (
		mu   sync.Mutex
		seen = map[string]string{}
	)

	record := func(name string) *httptest.Server {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			seen[name] = r.Header.Get("Authorization") + "|" + r.Header.Get("X-A")
			mu.Unlock()

			_, _ = w.Write([]byte("ok"))
		}))
		t.Cleanup(srv.Close)

		return srv
	}

	base, other := record("base"), record("other")

	_, err := RunScenarios(t.Context(), scenarioConfig(base, Config{Concurrency: 1, Requests: 1}), []Scenario{{
		Name: "auth", Do: func(ctx context.Context, c *Client) error {
			c.SetToken("secret")
			c.SetHeader("X-A", "1")

			if _, err := c.Get(ctx, "/"); err != nil {
				return err
			}

			// Another loopback server: the token is not for it.
			_, err := c.Get(ctx, other.URL+"/")

			return err
		},
	}})
	if err != nil {
		t.Fatal(err)
	}

	mu.Lock()
	defer mu.Unlock()

	if seen["base"] != "Bearer secret|1" || seen["other"] != "|" {
		t.Fatalf(
			"base saw %q, other saw %q: the token and headers must not leave the base URL",
			seen["base"],
			seen["other"],
		)
	}
}

func TestScenarioPanicMessageIsAWarning(t *testing.T) {
	t.Parallel()

	srv := okServer(t, nil)

	sum, err := RunScenarios(t.Context(), scenarioConfig(srv, Config{Concurrency: 1, Requests: 5}), []Scenario{{
		Name: "boom", Do: func(context.Context, *Client) error { panic("kaboom 42") },
	}})
	if err != nil {
		t.Fatal(err)
	}

	found := false

	for _, w := range sum.Warnings {
		found = found || (strings.Contains(w, "kaboom 42") && strings.Contains(w, "boom"))
	}

	if !found {
		t.Fatalf("warnings = %q, want the first panic message", sum.Warnings)
	}
}
