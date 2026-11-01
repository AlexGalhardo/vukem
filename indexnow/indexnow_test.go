package indexnow_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/AlexGalhardo/vukem/config"
	"github.com/AlexGalhardo/vukem/indexnow"
	"github.com/AlexGalhardo/vukem/queue"
)

const (
	key     = "example-key-1234"
	siteURL = "https://example.com"
)

type payload struct {
	Host        string   `json:"host"`
	Key         string   `json:"key"`
	KeyLocation string   `json:"keyLocation"`
	URLList     []string `json:"urlList"`
}

// engine is a fake IndexNow endpoint answering status and recording bodies.
func engine(t *testing.T, status int) (indexnow.Config, *http.Client, func() []payload) {
	t.Helper()

	var (
		mu   sync.Mutex
		got  []payload
		srvT = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost || r.Header.Get("Content-Type") != "application/json; charset=utf-8" {
				t.Errorf("got %s %s", r.Method, r.Header.Get("Content-Type"))
			}

			var p payload

			b, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(b, &p)

			mu.Lock()
			got = append(got, p)
			mu.Unlock()

			w.WriteHeader(status)
			_, _ = io.WriteString(w, "echo "+p.Key)
		}))
	)
	t.Cleanup(srvT.Close)

	return indexnow.Config{
		Key:      config.NewSecret(key),
		Endpoint: srvT.URL + "/indexnow",
	}, srvT.Client(), func() []payload {
		mu.Lock()
		defer mu.Unlock()

		return got
	}
}

func TestOffWhenUnset(t *testing.T) {
	t.Parallel()

	cfg, err := config.Load[indexnow.Config](config.MapSource(nil))
	if err != nil {
		t.Fatal(err)
	}

	if cfg.Enabled() || cfg.Validate(siteURL) != nil {
		t.Errorf("empty config: Enabled() = %v, Validate() = %v", cfg.Enabled(), cfg.Validate(siteURL))
	}

	if _, err := indexnow.New(cfg, siteURL, nil); !errors.Is(err, indexnow.ErrDisabled) {
		t.Errorf("New(empty) error = %v, want ErrDisabled", err)
	}
}

func TestValidate(t *testing.T) {
	t.Parallel()

	tests := map[string]indexnow.Config{
		"short key":        {Key: config.NewSecret("abc")},
		"long key":         {Key: config.NewSecret(strings.Repeat("a", 129))},
		"bad key chars":    {Key: config.NewSecret("abcd_efgh")},
		"foreign location": {Key: config.NewSecret(key), KeyLocation: "https://evil.example/key.txt"},
		"http endpoint":    {Key: config.NewSecret(key), Endpoint: "http://api.indexnow.org/indexnow"},
	}
	for name := range tests {
		if err := tests[name].Validate(siteURL); err == nil {
			t.Errorf("Validate(%s) = nil, want an error", name)
		}
	}

	if err := (indexnow.Config{Key: config.NewSecret(key), KeyLocation: siteURL + "/keys/in.txt"}).Validate(
		siteURL,
	); err != nil {
		t.Errorf("Validate(same-host location) = %v, want nil", err)
	}
}

func TestKeyFile(t *testing.T) {
	t.Parallel()

	cfg := indexnow.Config{Key: config.NewSecret(key)}
	if cfg.KeyPath() != "/"+key+".txt" {
		t.Errorf("KeyPath() = %q", cfg.KeyPath())
	}

	if p := (indexnow.Config{Key: config.NewSecret(key), KeyLocation: siteURL + "/keys/in.txt"}).KeyPath(); p != "/keys/in.txt" {
		t.Errorf("KeyPath(custom) = %q, want /keys/in.txt", p)
	}

	rec := httptest.NewRecorder()
	cfg.KeyFileHandler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, cfg.KeyPath(), http.NoBody))

	if rec.Body.String() != key || rec.Header().Get("Content-Type") != "text/plain; charset=utf-8" {
		t.Errorf("key file = %q (%s)", rec.Body.String(), rec.Header().Get("Content-Type"))
	}
}

func TestNotify(t *testing.T) {
	t.Parallel()

	cfg, hc, got := engine(t, http.StatusAccepted)

	n, err := indexnow.New(cfg, siteURL, hc)
	if err != nil {
		t.Fatal(err)
	}

	err = n.Notify(t.Context(), siteURL+"/a", siteURL+"/b", siteURL+"/a", "https://EXAMPLE.com/c")
	if err != nil {
		t.Fatal(err)
	}

	want := []payload{{
		Host: "example.com", Key: key, KeyLocation: siteURL + "/" + key + ".txt",
		URLList: []string{siteURL + "/a", siteURL + "/b", "https://EXAMPLE.com/c"},
	}}
	if diff := cmp.Diff(want, got()); diff != "" {
		t.Errorf("payloads (-want +got):\n%s", diff)
	}
}

func TestNotifyBatches(t *testing.T) {
	t.Parallel()

	cfg, hc, got := engine(t, http.StatusOK)
	n, _ := indexnow.New(cfg, siteURL, hc)

	urls := make([]string, indexnow.MaxURLs+5)
	for i := range urls {
		urls[i] = fmt.Sprintf("%s/p/%d", siteURL, i)
	}

	if err := n.Notify(t.Context(), urls...); err != nil {
		t.Fatal(err)
	}

	if p := got(); len(p) != 2 || len(p[0].URLList) != indexnow.MaxURLs || len(p[1].URLList) != 5 {
		t.Errorf("batches = %d, want 10000 + 5 URLs", len(p))
	}
}

func TestNotifyStatusErrors(t *testing.T) {
	t.Parallel()

	tests := map[int]error{
		http.StatusBadRequest:          indexnow.ErrBadRequest,
		http.StatusForbidden:           indexnow.ErrForbidden,
		http.StatusUnprocessableEntity: indexnow.ErrUnprocessable,
		http.StatusTooManyRequests:     indexnow.ErrTooManyRequests,
	}
	for status, want := range tests {
		cfg, hc, _ := engine(t, status)
		n, _ := indexnow.New(cfg, siteURL, hc)

		err := n.Notify(t.Context(), siteURL+"/a")

		var e *indexnow.Error
		if !errors.Is(err, want) || !errors.As(err, &e) || e.Status != status {
			t.Errorf("Notify(%d) error = %v, want %v", status, err, want)
		}
	}
}

// TestSecurity_A01_IndexNowRefusesForeignHosts: Notify sends nothing unless
// every URL is http(s) on the site's host, so a URL built from user input
// cannot make the app vouch for another host.
func TestSecurity_A01_IndexNowRefusesForeignHosts(t *testing.T) {
	t.Parallel()

	cfg, hc, got := engine(t, http.StatusOK)
	n, _ := indexnow.New(cfg, siteURL, hc)

	for _, u := range []string{
		"https://evil.example/a",
		"https://example.com.evil.example/a",
		"https://example.com@evil.example/a",
		"https://user@example.com/a",
		"javascript://example.com/a",
		"/relative",
		"https://example.com:8443/a",
	} {
		if err := n.Notify(t.Context(), siteURL+"/ok", u); !errors.Is(err, indexnow.ErrForeignURL) {
			t.Errorf("Notify(%q) error = %v, want ErrForeignURL", u, err)
		}
	}

	if p := got(); len(p) != 0 {
		t.Errorf("sent %d requests, want none", len(p))
	}
}

// TestSecurity_A09_IndexNowKeyNotInErrors: errors and logged config never
// carry the key, even when the engine echoes it.
func TestSecurity_A09_IndexNowKeyNotInErrors(t *testing.T) {
	t.Parallel()

	cfg, hc, _ := engine(t, http.StatusForbidden)
	n, _ := indexnow.New(cfg, siteURL, hc)

	err := n.Notify(t.Context(), siteURL+"/a")
	foreign := n.Notify(t.Context(), "https://evil.example/"+key)
	_, invalid := indexnow.New(
		indexnow.Config{Key: config.NewSecret(key), KeyLocation: "https://evil.example/" + key},
		siteURL,
		nil,
	)

	for _, e := range []error{err, invalid} {
		if e == nil || strings.Contains(e.Error(), key) {
			t.Errorf("error %v leaks the key or is nil", e)
		}
	}

	// The foreign URL is quoted back as given, so only its own text appears.
	if !errors.Is(foreign, indexnow.ErrForeignURL) {
		t.Errorf("foreign = %v", foreign)
	}

	var buf strings.Builder
	slog.New(slog.NewTextHandler(&buf, nil)).Info("config", "indexnow", cfg)

	if s := buf.String() + fmt.Sprintf("%+v %#v", cfg, cfg); strings.Contains(s, key) {
		t.Errorf("logged config leaks the key: %s", s)
	}
}

func TestHandler(t *testing.T) {
	t.Parallel()

	cfg, hc, got := engine(t, http.StatusOK)
	n, _ := indexnow.New(cfg, siteURL, hc)

	q := queue.New(queue.NewMemory(), slog.New(slog.DiscardHandler))
	if err := q.Register(n.Handler()); err != nil {
		t.Fatal(err)
	}

	m, err := q.Message(indexnow.Job{URLs: []string{siteURL + "/new"}})
	if err != nil {
		t.Fatal(err)
	}

	if err := q.Process(t.Context(), m); err != nil {
		t.Fatal(err)
	}

	if p := got(); len(p) != 1 || p[0].URLList[0] != siteURL+"/new" {
		t.Errorf("payloads = %+v, want the job's URL", p)
	}
}
