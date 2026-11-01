package bingwmt_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/AlexGalhardo/vukem/bingwmt"
	"github.com/AlexGalhardo/vukem/config"
)

const (
	apiKey = "SECRETKEY0123456789"
	site   = "https://example.com/"
)

type call struct {
	method, path string
	query        map[string]string
	body         map[string]any
}

// fake answers every API method with responses[path] (a JSON body, 200)
// and records the calls.
func fake(t *testing.T, status int, responses map[string]string) (*bingwmt.Client, *[]call) {
	t.Helper()

	var calls []call

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c := call{method: r.Method, path: r.URL.Path, query: map[string]string{}}
		for k := range r.URL.Query() {
			c.query[k] = r.URL.Query().Get(k)
		}

		if b, _ := io.ReadAll(r.Body); len(b) > 0 {
			_ = json.Unmarshal(b, &c.body)
		}

		calls = append(calls, c)

		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, responses[r.URL.Path])
	}))
	t.Cleanup(srv.Close)

	c, err := bingwmt.New(
		config.NewSecret(apiKey),
		bingwmt.Options{Endpoint: srv.URL + "/json", HTTPClient: srv.Client()},
	)
	if err != nil {
		t.Fatal(err)
	}

	return c, &calls
}

func TestNewOffWithoutKey(t *testing.T) {
	t.Parallel()

	cfg, err := config.Load[bingwmt.Config](config.MapSource(nil))
	if err != nil {
		t.Fatal(err)
	}

	if _, err := bingwmt.New(cfg.APIKey, bingwmt.Options{}); !errors.Is(err, bingwmt.ErrNoAPIKey) {
		t.Errorf("New(empty key) error = %v, want ErrNoAPIKey", err)
	}
}

func TestReads(t *testing.T) {
	t.Parallel()

	c, calls := fake(t, http.StatusOK, map[string]string{
		"/json/GetQueryStats": `{"d":[{"__type":"QueryStats:#Microsoft.Bing.Webmaster.Api.Interfaces",` +
			`"AvgClickPosition":3,"AvgImpressionPosition":5,"Clicks":12,"Date":"\/Date(1700000000000-0800)\/",` +
			`"Impressions":340,"Query":"vukem"}]}`,
		"/json/GetLinkCounts":         `{"d":{"Links":[{"Count":7,"Url":"https://example.com/a"}],"TotalPages":1}}`,
		"/json/GetUrlLinks":           `{"d":{"Details":[{"AnchorText":"docs","Url":"https://ref.example/x"}],"TotalPages":2}}`,
		"/json/GetUrlInfo":            `{"d":{"Url":"https://example.com/a","HttpStatus":200,"IsPage":true,"LastCrawledDate":"\/Date(0)\/"}}`,
		"/json/GetUrlSubmissionQuota": `{"d":{"DailyQuota":95,"MonthlyQuota":2800}}`,
	})
	ctx := t.Context()

	stats, err := c.QueryStats(ctx, site)
	if err != nil {
		t.Fatal(err)
	}

	wantStats := []bingwmt.QueryStats{{
		Query: "vukem", Date: bingwmt.Date{Time: time.UnixMilli(1700000000000).UTC()},
		Clicks: 12, Impressions: 340, AvgClickPosition: 3, AvgImpressionPosition: 5,
	}}
	if diff := cmp.Diff(wantStats, stats); diff != "" {
		t.Errorf("QueryStats (-want +got):\n%s", diff)
	}

	links, err := c.LinkCounts(ctx, site, 0)
	if err != nil || links.TotalPages != 1 || len(links.Links) != 1 || links.Links[0].Count != 7 {
		t.Errorf("LinkCounts = %+v, %v", links, err)
	}

	details, err := c.URLLinks(ctx, site, "https://example.com/a", 1)
	if err != nil || details.TotalPages != 2 || details.Details[0].AnchorText != "docs" {
		t.Errorf("URLLinks = %+v, %v", details, err)
	}

	info, err := c.URLInfo(ctx, site, "https://example.com/a")
	if err != nil || info.HTTPStatus != 200 || !info.IsPage || !info.LastCrawledDate.Equal(time.UnixMilli(0)) {
		t.Errorf("URLInfo = %+v, %v", info, err)
	}

	quota, err := c.SubmissionQuota(ctx, site)
	if err != nil || quota != (bingwmt.SubmissionQuota{DailyQuota: 95, MonthlyQuota: 2800}) {
		t.Errorf("SubmissionQuota = %+v, %v", quota, err)
	}

	want := []call{
		{method: "GET", path: "/json/GetQueryStats", query: map[string]string{"apikey": apiKey, "siteUrl": site}},
		{
			method: "GET",
			path:   "/json/GetLinkCounts",
			query:  map[string]string{"apikey": apiKey, "siteUrl": site, "page": "0"},
		},
		{method: "GET", path: "/json/GetUrlLinks", query: map[string]string{
			"apikey": apiKey, "siteUrl": site, "link": "https://example.com/a", "page": "1",
		}},
		{method: "GET", path: "/json/GetUrlInfo", query: map[string]string{
			"apikey": apiKey, "siteUrl": site, "url": "https://example.com/a",
		}},
		{
			method: "GET",
			path:   "/json/GetUrlSubmissionQuota",
			query:  map[string]string{"apikey": apiKey, "siteUrl": site},
		},
	}
	if diff := cmp.Diff(want, *calls, cmp.AllowUnexported(call{})); diff != "" {
		t.Errorf("calls (-want +got):\n%s", diff)
	}
}

func TestSubmit(t *testing.T) {
	t.Parallel()

	c, calls := fake(
		t,
		http.StatusOK,
		map[string]string{"/json/SubmitUrl": `{"d":null}`, "/json/SubmitUrlBatch": `{"d":null}`},
	)

	if err := c.SubmitURL(t.Context(), site, "https://example.com/new"); err != nil {
		t.Fatal(err)
	}

	if err := c.SubmitURLBatch(
		t.Context(),
		site,
		[]string{"https://example.com/a", "https://example.com/b"},
	); err != nil {
		t.Fatal(err)
	}

	want := []call{
		{
			method: "POST", path: "/json/SubmitUrl", query: map[string]string{"apikey": apiKey},
			body: map[string]any{"siteUrl": site, "url": "https://example.com/new"},
		},
		{
			method: "POST", path: "/json/SubmitUrlBatch", query: map[string]string{"apikey": apiKey},
			body: map[string]any{"siteUrl": site, "urlList": []any{"https://example.com/a", "https://example.com/b"}},
		},
	}
	if diff := cmp.Diff(want, *calls, cmp.AllowUnexported(call{})); diff != "" {
		t.Errorf("calls (-want +got):\n%s", diff)
	}
}

func TestErrors(t *testing.T) {
	t.Parallel()

	c, _ := fake(
		t,
		http.StatusBadRequest,
		map[string]string{"/json/GetCrawlStats": `{"ErrorCode":3,"Message":"InvalidApiKey"}`},
	)

	_, err := c.CrawlStats(t.Context(), site)

	var apiErr *bingwmt.Error
	if !errors.As(err, &apiErr) || apiErr.Code != 3 || apiErr.Message != "InvalidApiKey" || apiErr.Status != 400 {
		t.Errorf("CrawlStats error = %#v, want the API error", err)
	}

	limited, _ := fake(t, http.StatusTooManyRequests, nil)
	if _, err := limited.CrawlIssues(t.Context(), site); !errors.Is(err, bingwmt.ErrRateLimited) {
		t.Errorf("429 error = %v, want ErrRateLimited", err)
	}
}

// TestSecurity_A09_BingKeyNeverLogged: the API key travels only in the
// request; errors (transport, API echoes) and logged config redact it.
func TestSecurity_A09_BingKeyNeverLogged(t *testing.T) {
	t.Parallel()

	echo, _ := fake(t, http.StatusBadRequest, map[string]string{
		"/json/GetPageStats": `{"ErrorCode":1,"Message":"bad key ` + apiKey + `"}`,
	})
	_, echoErr := echo.PageStats(t.Context(), site)

	down, err := bingwmt.New(config.NewSecret(apiKey), bingwmt.Options{
		Endpoint:   "http://127.0.0.1:1/json",
		HTTPClient: &http.Client{Timeout: time.Second},
	})
	if err != nil {
		t.Fatal(err)
	}

	_, downErr := down.QueryStats(t.Context(), site)

	var buf bytes.Buffer
	slog.New(slog.NewTextHandler(&buf, nil)).Info("config", "bing", bingwmt.Config{APIKey: config.NewSecret(apiKey)})

	for name, s := range map[string]string{
		"api error":       fmt.Sprint(echoErr),
		"transport error": fmt.Sprint(downErr),
		"log":             buf.String(),
		"%+v":             fmt.Sprintf("%+v %#v", bingwmt.Config{APIKey: config.NewSecret(apiKey)}, downErr),
	} {
		if strings.Contains(s, apiKey) {
			t.Errorf("%s leaks the API key: %s", name, s)
		}
	}

	if downErr == nil || echoErr == nil {
		t.Errorf("errors = %v, %v, want both", echoErr, downErr)
	}
}

func TestDate(t *testing.T) {
	t.Parallel()

	tests := map[string]time.Time{
		`"\/Date(1700000000000)\/"`:      time.UnixMilli(1700000000000).UTC(),
		`"\/Date(1700000000000+0200)\/"`: time.UnixMilli(1700000000000).UTC(),
		`"\/Date(-1000)\/"`:              time.UnixMilli(-1000).UTC(),
		`null`:                           {},
		`""`:                             {},
	}

	for in, want := range tests {
		var d bingwmt.Date
		if err := json.Unmarshal([]byte(in), &d); err != nil || !d.Equal(want) {
			t.Errorf("Unmarshal(%s) = %v, %v, want %v", in, d.Time, err, want)
		}
	}

	var d bingwmt.Date
	if err := json.Unmarshal([]byte(`"2024-01-01"`), &d); err == nil {
		t.Error(`Unmarshal("2024-01-01") = nil, want an error`)
	}
}
