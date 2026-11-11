package loadtest

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"math/rand/v2"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"slices"
	"strings"
	"time"
)

// maxResponseBody is how much of a response body a Response keeps.
const maxResponseBody = 1 << 20

// maxTemplates bounds the route paths a Client remembers: a user that asks for a million distinct paths must not
// grow without limit.
const maxTemplates = 1000

// Scenario is one user journey. Do runs once per iteration and may send several requests through its Client.
type Scenario struct {
	// Name labels the scenario in reports and selects it in ScenarioConfig.Only. It must be unique.
	Name string
	// Weight is the relative share of the iterations; 0 means 1.
	Weight int
	// Do performs one iteration. A returned error, or a panic, marks the iteration failed. An HTTP status of 400 or
	// more is not an error here: inspect Response.Status.
	Do func(ctx context.Context, c *Client) error
}

// ScenarioConfig is Config plus the settings of a scenario run. In Config, Requests counts iterations, Rate is
// iterations per second in the open model and Concurrency is the number of virtual users in the closed model.
type ScenarioConfig struct {
	Config
	// BaseURL is the server under test, such as "http://127.0.0.1:41873". The loopback guard applies to it.
	BaseURL string
	// Route resolves a named route to a path, with params as name/value pairs. Nil makes Client.Route fail.
	Route func(name string, params ...any) (string, error)
	// Stages ramp the virtual users (closed model) or the arrival rate (open model, with Open). They replace
	// Duration, Rate and Concurrency, and cannot be combined with Requests.
	Stages []Stage
	// Open selects the open model for Stages.
	Open bool
	// Only runs just the named scenarios; empty means all. An unknown name is an error.
	Only []string
}

// scenarioSet picks scenarios by weight.
type scenarioSet struct {
	items []Scenario
	cum   []int
	total int
}

func newScenarioSet(items []Scenario) *scenarioSet {
	s := &scenarioSet{items: items}

	for _, it := range items {
		s.total += max(it.Weight, 1)
		s.cum = append(s.cum, s.total)
	}

	return s
}

// pick chooses the scenario of an iteration. The draw comes from a generator seeded by the iteration number, so two
// runs distribute alike whatever the concurrency. It is not a security use of randomness.
func (s *scenarioSet) pick(seq uint64) Scenario {
	n := rand.New(rand.NewPCG(1, seq)).IntN(s.total) //nolint:gosec // G404: a repeatable spread, not a secret

	i, _ := slices.BinarySearch(s.cum, n+1)

	return s.items[i]
}

func selectScenarios(all []Scenario, only []string) ([]Scenario, error) {
	if len(all) == 0 {
		return nil, errors.New("loadtest: no scenarios")
	}

	seen := map[string]bool{}

	for _, sc := range all {
		switch {
		case sc.Name == "":
			return nil, errors.New("loadtest: a scenario needs a name")
		case sc.Do == nil:
			return nil, fmt.Errorf("loadtest: scenario %q has no Do function", sc.Name)
		case sc.Weight < 0:
			return nil, fmt.Errorf("loadtest: scenario %q has a negative weight", sc.Name)
		case seen[sc.Name]:
			return nil, fmt.Errorf("loadtest: two scenarios are named %q", sc.Name)
		}

		seen[sc.Name] = true
	}

	for _, name := range only {
		if !seen[name] {
			return nil, fmt.Errorf("loadtest: no scenario named %q", name)
		}
	}

	if len(only) == 0 {
		return all, nil
	}

	var chosen []Scenario

	for _, sc := range all {
		if slices.Contains(only, sc.Name) {
			chosen = append(chosen, sc)
		}
	}

	return chosen, nil
}

// StartScenarios validates cfg and starts a scenario run.
func StartScenarios(ctx context.Context, cfg ScenarioConfig, scenarios []Scenario) (*Runner, error) {
	chosen, err := selectScenarios(scenarios, cfg.Only)
	if err != nil {
		return nil, err
	}

	c := cfg.Config
	open := c.Rate.Freq > 0

	if len(cfg.Stages) > 0 {
		if c.Requests > 0 {
			return nil, errors.New("loadtest: stages and a request count cannot be combined")
		}

		if err := checkStages(cfg.Stages, cfg.Open); err != nil {
			return nil, err
		}

		c.Duration, c.Rate, c.Concurrency, open = stagesDuration(cfg.Stages), Rate{}, 0, cfg.Open
	}

	if err := checkConfig(&c); err != nil {
		return nil, err
	}

	givenConnections := c.Connections
	c = c.withDefaults()

	if open && givenConnections == 0 {
		c.Connections = max(c.MaxWorkers, defaultConnections)
	}

	base := strings.TrimRight(cfg.BaseURL, "/")

	r, err := newRunner(ctx, c, Request{Method: http.MethodGet, URL: base + "/"})
	if err != nil {
		return nil, err
	}

	r.open, r.stages = open, cfg.Stages
	r.scenarios = make([]string, len(chosen))

	for i, sc := range chosen {
		r.scenarios[i] = sc.Name
	}

	if open && len(cfg.Stages) > 0 {
		paused := Rate{Freq: 0, Per: time.Second}
		r.rate.Store(&paused)
	}

	r.newWork = scenarioWork(r, newScenarioSet(chosen), base, cfg.Route)
	r.launch()

	return r, nil
}

// RunScenarios is StartScenarios plus collecting every Result into a Summary.
func RunScenarios(ctx context.Context, cfg ScenarioConfig, scenarios []Scenario) (Summary, error) {
	r, err := StartScenarios(ctx, cfg, scenarios)
	if err != nil {
		return Summary{}, err
	}

	var m Metrics

	for res := range r.Results() {
		m.Add(res)
	}

	return r.Summarize(&m), nil
}

var errPanicked = errors.New("loadtest: the scenario panicked")

func scenarioWork(
	r *Runner,
	set *scenarioSet,
	base string,
	route func(string, ...any) (string, error),
) func(int) workFn {
	return func(worker int) workFn {
		// A closed-model virtual user keeps its cookies for every iteration; an open-model arrival is a new visitor.
		var user *Client
		if !r.open {
			id := uint64(worker) //nolint:gosec // G115: worker is a small non-negative index
			user = newUserClient(r, base, route, id)
		}

		return func(seq uint64, scheduled time.Time) {
			c := user
			if c == nil {
				c = newUserClient(r, base, route, seq)
			}

			sc := set.pick(seq)
			c.scenario, c.scheduled = sc.Name, scheduled

			err := runIteration(r.reqCtx, sc, c)

			// An iteration cut short by the cancellation of the run says nothing about the app.
			if r.reqCtx.Err() != nil {
				return
			}

			r.iterations.Add(1)

			if err != nil {
				r.iterFailed.Add(1)

				if errors.Is(err, errPanicked) {
					r.panics.Add(1)
				}
			}
		}
	}
}

func hostOf(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}

	return u.Host
}

// runIteration runs a scenario and turns a panic into an error, so one bad iteration does not end the run. The first
// panic message is kept for the report.
func runIteration(ctx context.Context, sc Scenario, c *Client) (err error) {
	defer func() {
		if v := recover(); v != nil {
			c.r.notePanic(sc.Name, v)

			err = errPanicked
		}
	}()

	return sc.Do(ctx, c)
}

// Client sends the requests of one virtual user. It keeps cookies between calls and records every request as a
// Result. It is not safe for concurrent use: each virtual user has its own.
type Client struct {
	r         *Runner
	c         *client
	base      string
	route     func(string, ...any) (string, error)
	seq       uint64
	scenario  string
	scheduled time.Time
	header    http.Header
	// baseHost is the host of the base URL: the only one the user's headers and token go to.
	baseHost string
	// templates maps a path produced by Route to the route name, which names the request in reports.
	templates map[string]string
}

func newUserClient(r *Runner, base string, route func(string, ...any) (string, error), seq uint64) *Client {
	jar, _ := cookiejar.New(nil) // New never fails with nil options

	uc := *r.c
	uc.http = &http.Client{
		Transport:     r.c.http.Transport,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		Jar:           jar,
	}

	baseHost := ""
	if u, err := url.Parse(base); err == nil {
		baseHost = u.Host
	}

	return &Client{
		r: r, c: &uc, base: base, baseHost: baseHost, route: route, seq: seq, header: http.Header{},
		templates: map[string]string{},
	}
}

// Seq is the 0-based number of this virtual user in the closed model, or of the iteration in the open model. Use it
// to pick distinct test data.
func (c *Client) Seq() uint64 { return c.seq }

// SetHeader sets a header sent with every later request of this user.
func (c *Client) SetHeader(name, value string) { c.header.Set(name, value) }

// SetToken sets "Authorization: Bearer token" for later requests.
func (c *Client) SetToken(token string) { c.header.Set("Authorization", "Bearer "+token) }

// Route returns the path of a named route; params are name/value pairs. Requests to that path are named after the
// route in the report.
func (c *Client) Route(name string, params ...any) (string, error) {
	if c.route == nil {
		return "", errors.New("loadtest: no route resolver is configured")
	}

	p, err := c.route(name, params...)
	if err != nil {
		return "", err
	}

	if _, known := c.templates[p]; !known && len(c.templates) < maxTemplates {
		c.templates[p] = name
	}

	return p, nil
}

// Response is the part of a response a scenario may inspect.
type Response struct {
	Status int
	Header http.Header
	// Body holds at most 1 MiB; the rest was read and discarded.
	Body []byte
}

// JSON decodes Body into v.
func (r *Response) JSON(v any) error { return json.Unmarshal(r.Body, v) }

// Get sends a GET to target, which is a path such as "/posts" (relative to the base URL) or the result of Route.
func (c *Client) Get(ctx context.Context, target string) (*Response, error) {
	return c.Do(ctx, Request{Method: http.MethodGet, URL: target})
}

// Delete sends a DELETE to target.
func (c *Client) Delete(ctx context.Context, target string) (*Response, error) {
	return c.Do(ctx, Request{Method: http.MethodDelete, URL: target})
}

// Post sends a POST with a body, which may be nil.
func (c *Client) Post(ctx context.Context, target, contentType string, body []byte) (*Response, error) {
	return c.withBody(ctx, http.MethodPost, target, contentType, body)
}

// Put sends a PUT with a body, which may be nil.
func (c *Client) Put(ctx context.Context, target, contentType string, body []byte) (*Response, error) {
	return c.withBody(ctx, http.MethodPut, target, contentType, body)
}

// Patch sends a PATCH with a body, which may be nil.
func (c *Client) Patch(ctx context.Context, target, contentType string, body []byte) (*Response, error) {
	return c.withBody(ctx, http.MethodPatch, target, contentType, body)
}

// PostJSON sends v encoded as JSON.
func (c *Client) PostJSON(ctx context.Context, target string, v any) (*Response, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}

	return c.Post(ctx, target, "application/json", b)
}

// PostForm sends form url-encoded.
func (c *Client) PostForm(ctx context.Context, target string, form url.Values) (*Response, error) {
	return c.Post(ctx, target, "application/x-www-form-urlencoded", []byte(form.Encode()))
}

func (c *Client) withBody(ctx context.Context, method, target, contentType string, body []byte) (*Response, error) {
	h := http.Header{}
	if contentType != "" {
		h.Set("Content-Type", contentType)
	}

	return c.Do(ctx, Request{Method: method, URL: target, Header: h, Body: body})
}

// Do sends r. A URL that starts with "/" is relative to the base URL; any other URL goes through the loopback guard
// like every request of a run, and a host that is not allowed is refused without a connection. The error is a
// transport or guard failure; a status of 400 or more is a Response, not an error.
func (c *Client) Do(ctx context.Context, r Request) (*Response, error) {
	method := cmp.Or(r.Method, http.MethodGet)

	path, _, _ := strings.Cut(r.URL, "?")

	target := r.URL
	if strings.HasPrefix(r.URL, "/") {
		target = c.base + r.URL
	} else if u, err := url.Parse(r.URL); err == nil {
		path = u.Path
	}

	if r.Name == "" {
		label := path
		if name, ok := c.templates[path]; ok {
			label = name
		}

		r.Name = c.scenario + ": " + method + " " + label
	}

	// The headers and the token are for the app under test: another host, even an allowed one, never gets them.
	header := http.Header{}
	if strings.HasPrefix(r.URL, "/") || hostOf(target) == c.baseHost {
		header = c.header.Clone()
	}

	maps.Copy(header, r.Header)

	req := Request{Name: r.Name, Method: method, URL: target, Header: header, Body: r.Body}

	res, resp := c.c.doBody(ctx, req, c.seq, c.scheduled, c.r.now, maxResponseBody)
	c.scheduled = time.Time{} // later requests of the iteration are due when they are sent

	c.r.publish(res)

	if res.Err != "" {
		return resp, fmt.Errorf("loadtest: the request failed: %s", res.Err)
	}

	return resp, nil
}
