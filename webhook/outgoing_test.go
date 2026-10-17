package webhook_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/AlexGalhardo/vukem/database"
	"github.com/AlexGalhardo/vukem/httpclient"
	"github.com/AlexGalhardo/vukem/internal/testutil"
	"github.com/AlexGalhardo/vukem/queue"
	"github.com/AlexGalhardo/vukem/webhook"
)

const testSecret = "whsec_MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY="

// loopback lets tests reach httptest servers; production code never sets it.
var loopback = []netip.Prefix{
	netip.MustParsePrefix("127.0.0.0/8"),
	netip.MustParsePrefix("::1/128"),
} //nolint:gochecknoglobals // read-only test table

type memLog struct {
	mu       sync.Mutex
	attempts []webhook.Attempt
}

func (l *memLog) Record(_ context.Context, a webhook.Attempt) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.attempts = append(l.attempts, a)

	return nil
}

func (l *memLog) all() []webhook.Attempt {
	l.mu.Lock()
	defer l.mu.Unlock()

	return append([]webhook.Attempt(nil), l.attempts...)
}

// stepDriver hands out messages regardless of AvailableAt so a test can run
// every retry at once and inspect the backoff the queue scheduled.
type stepDriver struct {
	mu       sync.Mutex
	ready    []*queue.Message
	released []queue.Message
	acked    int
	failed   int
}

func (d *stepDriver) Push(_ context.Context, m *queue.Message) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	cp := *m
	d.ready = append(d.ready, &cp)

	return nil
}

func (d *stepDriver) Pop(context.Context, []string) (*queue.Message, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if len(d.ready) == 0 {
		return nil, nil
	}

	m := d.ready[0]
	d.ready = d.ready[1:]

	return m, nil
}

func (d *stepDriver) Ack(context.Context, *queue.Message) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.acked++

	return nil
}

func (d *stepDriver) Release(_ context.Context, m *queue.Message) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.released = append(d.released, *m)
	cp := *m
	d.ready = append(d.ready, &cp)

	return nil
}

func (d *stepDriver) Fail(context.Context, *queue.Message) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.failed++

	return nil
}

func endpoints(url string) func(context.Context, string) (webhook.Endpoint, error) {
	return func(_ context.Context, id string) (webhook.Endpoint, error) {
		if id != "seller-1" {
			return webhook.Endpoint{}, errors.New("unknown endpoint")
		}

		return webhook.Endpoint{URL: url, Secret: testSecret}, nil
	}
}

func newSender(t *testing.T, o webhook.SenderOptions) *webhook.Sender {
	t.Helper()

	if o.Logger == nil {
		o.Logger = slog.New(slog.DiscardHandler)
	}

	s, err := webhook.NewSender(o)
	if err != nil {
		t.Fatal(err)
	}

	return s
}

func TestNewSecret(t *testing.T) {
	t.Parallel()

	a, err := webhook.NewSecret()
	if err != nil {
		t.Fatal(err)
	}

	b, _ := webhook.NewSecret()

	key, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(a, "whsec_"))
	if !strings.HasPrefix(a, "whsec_") || err != nil || len(key) != 32 {
		t.Fatalf("NewSecret() = %q (%d bytes, %v), want whsec_ and 32 base64 bytes", a, len(key), err)
	}

	if a == b {
		t.Fatalf("NewSecret() returned %q twice", a)
	}

	if _, err := webhook.Standard(a, webhook.StandardOptions{}); err != nil {
		t.Fatalf("Standard(NewSecret()) = %v, want nil", err)
	}
}

func TestSignStandardVerifiesWithStandard(t *testing.T) {
	t.Parallel()

	now := time.Unix(1_800_000_000, 0)
	body := []byte(`{"type":"order.paid"}`)

	sig, err := webhook.SignStandard(testSecret, "msg_1", now, body)
	if err != nil {
		t.Fatal(err)
	}

	verify, err := webhook.Standard(testSecret, webhook.StandardOptions{Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}

	h := verify(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))

	for _, tc := range []struct {
		name string
		sig  string
		body []byte
		want int
	}{
		{"valid", sig, body, http.StatusNoContent},
		{"tampered body", sig, []byte(`{"type":"order.refunded"}`), http.StatusUnauthorized},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/hook", bytes.NewReader(tc.body))
			r.Header.Set("webhook-id", "msg_1")
			r.Header.Set("webhook-timestamp", "1800000000")
			r.Header.Set("webhook-signature", tc.sig)

			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, r)

			if rec.Code != tc.want {
				t.Fatalf("Standard(SignStandard(...)) = %d, want %d", rec.Code, tc.want)
			}
		})
	}

	if _, err := webhook.SignStandard("whsec_!!", "msg_1", now, body); err == nil {
		t.Fatal("SignStandard(malformed secret) = nil, want error")
	}
}

func TestNewSenderRequiresEndpoint(t *testing.T) {
	t.Parallel()

	if _, err := webhook.NewSender(webhook.SenderOptions{}); err == nil {
		t.Fatal("NewSender(no Endpoint) = nil, want error")
	}
}

// receiver verifies every delivery with webhook.Standard and answers the
// statuses in order (the last one repeats).
type receiver struct {
	mu       sync.Mutex
	statuses []int
	ids      []string
	bodies   []string
}

func (rc *receiver) server(t *testing.T) *httptest.Server {
	t.Helper()

	verify, err := webhook.Standard(testSecret, webhook.StandardOptions{})
	if err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(verify(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rc.mu.Lock()
		defer rc.mu.Unlock()

		b, _ := io.ReadAll(r.Body)
		rc.ids = append(rc.ids, r.Header.Get("webhook-id"))
		rc.bodies = append(rc.bodies, string(b))

		status := rc.statuses[min(len(rc.ids), len(rc.statuses))-1]
		w.WriteHeader(status)
	})))
	t.Cleanup(srv.Close)

	return srv
}

func TestSenderRetriesWithExponentialBackoff(t *testing.T) {
	t.Parallel()

	rc := &receiver{statuses: []int{http.StatusInternalServerError, http.StatusTooManyRequests, http.StatusNoContent}}
	srv := rc.server(t)
	log := &memLog{}

	s := newSender(t, webhook.SenderOptions{
		Endpoint: endpoints(srv.URL), Log: log, InsecureAllowHTTP: true, InsecureAllow: loopback,
	})

	d := &stepDriver{}

	q := queue.New(d, slog.New(slog.DiscardHandler))
	if err := q.Register(s.Job()); err != nil {
		t.Fatal(err)
	}

	payload := json.RawMessage(`{"type":"order.paid","data":{"order":42}}`)
	if err := s.Send(t.Context(), q, webhook.Delivery{Endpoint: "seller-1", Payload: payload}); err != nil {
		t.Fatal(err)
	}

	var gaps []time.Duration

	for range 3 {
		m, _ := d.Pop(t.Context(), nil)
		if m == nil {
			t.Fatal("Pop() = nil, want a pending delivery")
		}

		before := time.Now()
		released := len(d.released)

		if err := q.Process(t.Context(), m); err != nil {
			t.Fatal(err)
		}

		if len(d.released) > released {
			// Round down to the second: the backoff is whole seconds and
			// Process takes milliseconds.
			gaps = append(gaps, d.released[released].AvailableAt.Sub(before).Truncate(time.Second))
		}
	}

	if diff := cmp.Diff([]time.Duration{30 * time.Second, time.Minute}, gaps); diff != "" {
		t.Errorf("retry delays (-want +got):\n%s", diff)
	}

	if d.acked != 1 || d.failed != 0 || len(d.ready) != 0 {
		t.Errorf("acked, failed, ready = %d, %d, %d, want 1, 0, 0", d.acked, d.failed, len(d.ready))
	}

	if len(rc.ids) != 3 || rc.ids[0] == "" || rc.ids[0] != rc.ids[1] || rc.ids[1] != rc.ids[2] {
		t.Errorf("webhook-id per attempt = %q, want one stable id", rc.ids)
	}

	if rc.bodies[2] != string(payload) {
		t.Errorf("body = %s, want %s", rc.bodies[2], payload)
	}

	var (
		statuses []int
		cats     []string
	)

	for _, a := range log.all() {
		if a.Endpoint != "seller-1" || a.EventID != rc.ids[0] || a.At.IsZero() || a.Duration < 0 {
			t.Errorf("attempt = %+v, want endpoint, event id, time and duration", a)
		}

		statuses = append(statuses, a.Status)
		cats = append(cats, a.Error)
	}

	if diff := cmp.Diff([]string{"http_status", "http_status", ""}, cats); diff != "" {
		t.Errorf("logged error categories (-want +got):\n%s", diff)
	}

	if diff := cmp.Diff([]int{500, 429, 204}, statuses); diff != "" {
		t.Errorf("logged statuses (-want +got):\n%s", diff)
	}
}

func TestSenderStopsOnGone(t *testing.T) {
	t.Parallel()

	rc := &receiver{statuses: []int{http.StatusGone}}
	srv := rc.server(t)

	var gone []string

	s := newSender(t, webhook.SenderOptions{
		Endpoint: endpoints(srv.URL), InsecureAllowHTTP: true, InsecureAllow: loopback,
		OnGone: func(_ context.Context, endpoint string) error {
			gone = append(gone, endpoint)
			return nil
		},
	})

	d := &stepDriver{}

	q := queue.New(d, slog.New(slog.DiscardHandler))
	if err := q.Register(s.Job()); err != nil {
		t.Fatal(err)
	}

	if err := s.Send(
		t.Context(),
		q,
		webhook.Delivery{ID: "msg_gone", Endpoint: "seller-1", Payload: json.RawMessage(`{}`)},
	); err != nil {
		t.Fatal(err)
	}

	m, _ := d.Pop(t.Context(), nil)
	if err := q.Process(t.Context(), m); err != nil {
		t.Fatal(err)
	}

	if d.acked != 1 || len(d.released) != 0 || len(d.ready) != 0 {
		t.Errorf("acked, released, ready = %d, %d, %d, want 1, 0, 0", d.acked, len(d.released), len(d.ready))
	}

	if diff := cmp.Diff([]string{"seller-1"}, gone); diff != "" {
		t.Errorf("OnGone calls (-want +got):\n%s", diff)
	}
}

func TestSenderSkipsDisabledEndpoint(t *testing.T) {
	t.Parallel()

	rc := &receiver{statuses: []int{http.StatusNoContent}}
	srv := rc.server(t)

	s := newSender(t, webhook.SenderOptions{
		Endpoint: func(context.Context, string) (webhook.Endpoint, error) {
			return webhook.Endpoint{URL: srv.URL, Secret: testSecret}, webhook.ErrEndpointDisabled
		},
		InsecureAllowHTTP: true, InsecureAllow: loopback,
	})

	err := s.Deliver(t.Context(), webhook.Delivery{ID: "msg_1", Endpoint: "seller-1", Payload: json.RawMessage(`{}`)})
	if err != nil || len(rc.ids) != 0 {
		t.Fatalf("Deliver(disabled) = %v with %d requests, want nil and none", err, len(rc.ids))
	}
}

func TestSenderRefusesUnsignedDelivery(t *testing.T) {
	t.Parallel()

	rc := &receiver{statuses: []int{http.StatusNoContent}}
	srv := rc.server(t)

	s := newSender(t, webhook.SenderOptions{
		Endpoint: func(context.Context, string) (webhook.Endpoint, error) {
			return webhook.Endpoint{URL: srv.URL, Secret: ""}, nil
		},
		InsecureAllowHTTP: true, InsecureAllow: loopback,
	})

	err := s.Deliver(t.Context(), webhook.Delivery{ID: "msg_1", Endpoint: "seller-1", Payload: json.RawMessage(`{}`)})
	if err == nil || len(rc.ids) != 0 {
		t.Fatalf("Deliver(no secret) = %v with %d requests, want error and none", err, len(rc.ids))
	}
}

func TestSendValidatesDelivery(t *testing.T) {
	t.Parallel()

	s := newSender(t, webhook.SenderOptions{Endpoint: endpoints("https://example.com/hook")})
	q := queue.New(queue.NewFake(), slog.New(slog.DiscardHandler))

	if err := q.Register(s.Job()); err != nil {
		t.Fatal(err)
	}

	for _, d := range []webhook.Delivery{
		{Payload: json.RawMessage(`{}`)},
		{Endpoint: "seller-1", Payload: json.RawMessage(`{bad`)},
		{Endpoint: "seller-1"},
		{ID: "evt.1", Endpoint: "seller-1", Payload: json.RawMessage(`{}`)},
		{ID: "evt 1", Endpoint: "seller-1", Payload: json.RawMessage(`{}`)},
		{ID: strings.Repeat("a", 256), Endpoint: "seller-1", Payload: json.RawMessage(`{}`)},
		{Endpoint: strings.Repeat("e", 256), Payload: json.RawMessage(`{}`)},
	} {
		if err := s.Send(t.Context(), q, d); err == nil {
			t.Errorf("Send(%+v) = nil, want error", d)
		}
	}
}

// SSRF: endpoint URLs come from users (sellers), so deliveries go through
// httpclient.Safe and never reach loopback, private or metadata addresses.
func TestSecurity_A01_OutgoingWebhookSSRF(t *testing.T) {
	t.Parallel()

	rc := &receiver{statuses: []int{http.StatusNoContent}}
	srv := rc.server(t)

	for _, target := range []string{srv.URL, "http://169.254.169.254/latest/meta-data", "http://10.0.0.1/hook"} {
		t.Run(target, func(t *testing.T) {
			t.Parallel()

			log := &memLog{}
			s := newSender(t, webhook.SenderOptions{Endpoint: endpoints(target), Log: log, InsecureAllowHTTP: true})

			err := s.Deliver(
				t.Context(),
				webhook.Delivery{ID: "msg_1", Endpoint: "seller-1", Payload: json.RawMessage(`{}`)},
			)
			if !errors.Is(err, httpclient.ErrBlockedAddress) {
				t.Fatalf("Deliver(%s) = %v, want ErrBlockedAddress", target, err)
			}

			if a := log.all(); len(a) != 1 || a[0].Status != 0 || a[0].Error != webhook.AttemptBlockedAddress {
				t.Fatalf("logged attempts = %+v, want one blocked attempt", a)
			}
		})
	}

	if len(rc.ids) != 0 {
		t.Fatalf("loopback server got %d requests, want 0", len(rc.ids))
	}
}

// Signed payloads stay off plaintext HTTP unless a test opts in.
func TestSecurity_A04_OutgoingWebhookRequiresHTTPS(t *testing.T) {
	t.Parallel()

	s := newSender(t, webhook.SenderOptions{Endpoint: endpoints("http://example.com/hook")})

	err := s.Deliver(t.Context(), webhook.Delivery{ID: "msg_1", Endpoint: "seller-1", Payload: json.RawMessage(`{}`)})
	if err == nil || !strings.Contains(err.Error(), "https") {
		t.Fatalf("Deliver(http URL) = %v, want an https error", err)
	}
}

func TestSecurity_A09_OutgoingWebhookLogsNoSecrets(t *testing.T) {
	t.Parallel()

	rc := &receiver{statuses: []int{http.StatusInternalServerError, http.StatusNoContent}}
	srv := rc.server(t)

	var buf bytes.Buffer

	secretURL := srv.URL + "/hook?token=tok3n"
	s := newSender(t, webhook.SenderOptions{
		Endpoint: endpoints(secretURL), InsecureAllowHTTP: true, InsecureAllow: loopback,
		Logger: slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})),
	})

	payload := json.RawMessage(`{"card":"4242424242424242"}`)

	var errs []string

	for range 2 {
		if err := s.Deliver(
			t.Context(),
			webhook.Delivery{ID: "msg_1", Endpoint: "seller-1", Payload: payload},
		); err != nil {
			errs = append(errs, err.Error())
		}
	}

	out := buf.String() + strings.Join(errs, "\n")
	for _, leak := range []string{"MDEyMzQ1Njc4", "4242424242424242", "tok3n"} {
		if strings.Contains(out, leak) {
			t.Errorf("logs and errors contain %q:\n%s", leak, out)
		}
	}

	if !strings.Contains(buf.String(), "seller-1") || len(errs) != 1 {
		t.Errorf("logs = %s errors = %q, want the endpoint id and one retryable error", buf.String(), errs)
	}
}

func openSQLite(t *testing.T) *database.DB {
	t.Helper()

	db, err := database.Open(t.Context(), "file:"+filepath.Join(testutil.TempDir(t), "w.db"))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = db.Close() })

	return db
}

func TestSQLDeliveryLog(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"bad;name", strings.Repeat("t", 52)} {
		if _, err := webhook.NewSQLDeliveryLog(openSQLite(t), name); err == nil {
			t.Fatalf("NewSQLDeliveryLog(%q) = nil, want error", name)
		}
	}

	l, err := webhook.NewSQLDeliveryLog(openSQLite(t), "")
	if err != nil {
		t.Fatal(err)
	}

	for range 2 {
		if err := l.CreateTable(t.Context()); err != nil {
			t.Fatal(err)
		}
	}

	at := time.UnixMilli(1_800_000_000_000).UTC()
	want := []webhook.Attempt{
		{
			Endpoint: "seller-1",
			EventID:  "msg_1",
			Status:   204,
			Duration: 120 * time.Millisecond,
			At:       at.Add(time.Minute),
		},
		{Endpoint: "seller-1", EventID: "msg_1", Error: "connection refused", Duration: 5 * time.Millisecond, At: at},
	}

	for _, a := range []webhook.Attempt{want[1], want[0], {Endpoint: "seller-2", EventID: "msg_2", Status: 200, At: at}} {
		if err := l.Record(t.Context(), a); err != nil {
			t.Fatal(err)
		}
	}

	if err := l.Record(t.Context(), webhook.Attempt{Endpoint: strings.Repeat("e", 256), EventID: "msg_1"}); err == nil {
		t.Error("Record(256-byte endpoint) = nil, want error")
	}

	got, err := l.Recent(t.Context(), "seller-1", 10)
	if err != nil {
		t.Fatal(err)
	}

	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("Recent(seller-1) (-want +got):\n%s", diff)
	}
}

func TestSenderTreatsLargeResponseBodyByStatus(t *testing.T) {
	t.Parallel()

	for _, status := range []int{http.StatusOK, http.StatusGone} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			t.Parallel()

			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Length", strconv.Itoa(20<<20))
				w.WriteHeader(status)
				_, _ = w.Write(bytes.Repeat([]byte("x"), 1<<20))
			}))
			t.Cleanup(srv.Close)

			log := &memLog{}
			s := newSender(t, webhook.SenderOptions{
				Endpoint: endpoints(srv.URL), Log: log, InsecureAllowHTTP: true, InsecureAllow: loopback,
			})

			err := s.Deliver(
				t.Context(),
				webhook.Delivery{ID: "msg_1", Endpoint: "seller-1", Payload: json.RawMessage(`{}`)},
			)
			if a := log.all(); err != nil || len(a) != 1 || a[0].Status != status {
				t.Fatalf("Deliver(large %d body) = %v, attempts %+v, want nil and status %d", status, err, a, status)
			}
		})
	}
}

func TestSenderStopsOnRedirect(t *testing.T) {
	t.Parallel()

	rc := &receiver{statuses: []int{http.StatusFound}}
	srv := rc.server(t)

	s := newSender(
		t,
		webhook.SenderOptions{Endpoint: endpoints(srv.URL), InsecureAllowHTTP: true, InsecureAllow: loopback},
	)

	err := s.Deliver(t.Context(), webhook.Delivery{ID: "msg_1", Endpoint: "seller-1", Payload: json.RawMessage(`{}`)})
	if err != nil || len(rc.ids) != 1 {
		t.Fatalf("Deliver(302) = %v with %d requests, want nil (no retry) and 1", err, len(rc.ids))
	}
}

type ctxLog struct {
	memLog

	ctxErr error
}

func (l *ctxLog) Record(ctx context.Context, a webhook.Attempt) error {
	l.ctxErr = ctx.Err()
	return l.memLog.Record(ctx, a)
}

func TestSenderRecordsAttemptAfterCancel(t *testing.T) {
	t.Parallel()

	rc := &receiver{statuses: []int{http.StatusNoContent}}
	srv := rc.server(t)
	log := &ctxLog{}

	s := newSender(t, webhook.SenderOptions{
		Endpoint: endpoints(srv.URL), Log: log, InsecureAllowHTTP: true, InsecureAllow: loopback,
	})

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if err := s.Deliver(
		ctx,
		webhook.Delivery{ID: "msg_1", Endpoint: "seller-1", Payload: json.RawMessage(`{}`)},
	); err == nil {
		t.Fatal("Deliver(cancelled) = nil, want a retryable error")
	}

	if a := log.all(); len(a) != 1 || a[0].Error != webhook.AttemptTimeout || log.ctxErr != nil {
		t.Fatalf("attempts = %+v, record ctx err = %v, want one timeout recorded with a live context", a, log.ctxErr)
	}
}

func TestDeliverRejectsInvalidInput(t *testing.T) {
	t.Parallel()

	rc := &receiver{statuses: []int{http.StatusNoContent}}
	srv := rc.server(t)

	userinfo := strings.Replace(srv.URL, "http://", "http://user:pa55word@", 1)

	for _, tc := range []struct {
		name  string
		url   string
		id    string
		retry bool
	}{
		{"empty id", srv.URL, "", false},
		{"id with dot", srv.URL, "a.b", false},
		{"userinfo in URL", userinfo, "msg_1", true},
		{"no host", "http:///hook", "msg_1", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			log := &memLog{}
			s := newSender(t, webhook.SenderOptions{
				Endpoint: endpoints(tc.url), Log: log, InsecureAllowHTTP: true, InsecureAllow: loopback,
			})

			err := s.Deliver(
				t.Context(),
				webhook.Delivery{ID: tc.id, Endpoint: "seller-1", Payload: json.RawMessage(`{}`)},
			)
			if (err != nil) != tc.retry || (err != nil && strings.Contains(err.Error(), "pa55word")) {
				t.Fatalf("Deliver(%s) = %v, want retry %v without credentials", tc.name, err, tc.retry)
			}

			if a := log.all(); len(a) != 1 || a[0].Error != webhook.AttemptInvalid {
				t.Fatalf("attempts = %+v, want one invalid attempt", a)
			}
		})
	}

	if len(rc.ids) != 0 {
		t.Fatalf("server got %d requests, want 0", len(rc.ids))
	}
}

func TestSenderNoRetries(t *testing.T) {
	t.Parallel()

	fake := queue.NewFake()
	s := newSender(t, webhook.SenderOptions{Endpoint: endpoints("https://example.com"), Retries: -1})

	q := queue.New(fake, slog.New(slog.DiscardHandler))
	if err := q.Register(s.Job()); err != nil {
		t.Fatal(err)
	}

	if err := s.Send(
		t.Context(),
		q,
		webhook.Delivery{Endpoint: "seller-1", Payload: json.RawMessage(`{}`)},
	); err != nil {
		t.Fatal(err)
	}

	if m := fake.Messages(); len(m) != 1 || m[0].MaxRetries != 0 {
		t.Fatalf("messages = %+v, want one with MaxRetries 0", m)
	}
}
