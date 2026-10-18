package webhook

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/AlexGalhardo/vukem/httpclient"
	"github.com/AlexGalhardo/vukem/queue"
)

// DeliverKind is the queue job kind of outgoing webhook deliveries.
const DeliverKind = "webhook.deliver"

// NewSecret returns a random signing secret in the Standard Webhooks
// format, "whsec_" followed by 32 base64 bytes. Give it to the receiver
// once and store it with the endpoint.
func NewSecret() (string, error) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return "", fmt.Errorf("webhook: new secret: %w", err)
	}

	return "whsec_" + base64.StdEncoding.EncodeToString(key), nil
}

// SignStandard returns the webhook-signature value ("v1,<base64>") for a
// delivery with id sent at at, as [Standard] verifies it.
func SignStandard(secret, id string, at time.Time, body []byte) (string, error) {
	key, err := decodeSecret(secret)
	if err != nil {
		return "", err
	}

	return signStandard(key, id, strconv.FormatInt(at.Unix(), 10), body), nil
}

func decodeSecret(secret string) ([]byte, error) {
	key, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(secret, "whsec_"))
	if err != nil || len(key) == 0 {
		return nil, errors.New("webhook: secret must be base64, optionally prefixed with whsec_")
	}

	return key, nil
}

func signStandard(key []byte, id, ts string, body []byte) string {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(id + "." + ts + "."))
	m.Write(body)

	return "v1," + base64.StdEncoding.EncodeToString(m.Sum(nil))
}

// ErrEndpointDisabled is returned by SenderOptions.Endpoint for an
// endpoint that no longer receives deliveries; pending deliveries to it
// are dropped instead of retried.
var ErrEndpointDisabled = errors.New("webhook: endpoint disabled")

// Endpoint is where a delivery goes. URL is usually user supplied (a
// seller's webhook URL); Secret is its "whsec_..." signing secret.
type Endpoint struct {
	URL    string
	Secret string
}

// Delivery is one event for one endpoint, the payload of the queue job.
// It carries the endpoint id, never its URL or secret, so neither is
// stored in the jobs tables.
type Delivery struct {
	// ID is the webhook-id header, stable across retries so receivers can
	// drop duplicates; Send generates "msg_<random>" when empty.
	ID string `json:"id"`
	// Endpoint identifies the endpoint for SenderOptions.Endpoint.
	Endpoint string `json:"endpoint"`
	// Payload is the JSON body. It is stored inside the job JSON, so
	// insignificant whitespace may be compacted; the signature covers the
	// bytes actually sent.
	Payload json.RawMessage `json:"payload"`
}

// SenderOptions configure a Sender.
type SenderOptions struct {
	// Endpoint looks up an endpoint by id at each attempt (required), so a
	// rotated secret or a changed URL applies to pending retries. Return
	// ErrEndpointDisabled to drop deliveries to it.
	Endpoint func(ctx context.Context, id string) (Endpoint, error)
	// Log records every attempt (optional); see SQLDeliveryLog.
	Log DeliveryLog
	// OnGone runs when an endpoint answers 410 Gone, for example to
	// disable it. Deliveries answered with 410 are not retried; an OnGone
	// error is logged and not retried either.
	OnGone func(ctx context.Context, endpoint string) error
	// Logger receives delivery outcomes without URLs, secrets or payloads
	// (default slog.Default()).
	Logger *slog.Logger
	// Timeout bounds one HTTP attempt (default 15s).
	Timeout time.Duration
	// Retries is how many times a failed delivery is retried (0 means the
	// default, 8; a negative value means no retries).
	Retries int
	// Backoff is the delay before retry n (default exponential from 30s,
	// capped at one hour: 30s, 1m, 2m … about two hours in total).
	Backoff func(attempt int) time.Duration
	// Now is the clock for webhook-timestamp (default time.Now).
	Now func() time.Time
	// InsecureAllowHTTP permits plain http:// endpoints, which expose
	// payloads and signatures on the wire. For tests and local tools.
	InsecureAllowHTTP bool
	// InsecureAllow lists internal prefixes deliveries may reach (for
	// example 127.0.0.0/8 for an httptest server). Endpoints are user
	// supplied, so leave it empty in production.
	InsecureAllow []netip.Prefix
}

// Sender delivers signed outgoing webhooks from queue jobs: register
// [Sender.Job] with the queue and call [Sender.Send]. Each attempt is
// signed with the Standard Webhooks scheme (webhook-id, webhook-timestamp,
// webhook-signature) and sent through [httpclient.Safe], so endpoints on
// loopback, private or metadata addresses are refused. A 2xx answer is a
// success, 410 Gone stops for good, and anything else is retried with
// backoff until Retries runs out and the job moves to the failed jobs.
type Sender struct {
	o      SenderOptions
	client *http.Client
}

// NewSender returns a Sender; a missing Endpoint lookup is an error.
func NewSender(o SenderOptions) (*Sender, error) {
	if o.Endpoint == nil {
		return nil, errors.New("webhook: SenderOptions.Endpoint is required")
	}

	if o.Logger == nil {
		o.Logger = slog.Default()
	}

	if o.Timeout <= 0 {
		o.Timeout = 15 * time.Second
	}

	switch {
	case o.Retries == 0:
		o.Retries = 8
	case o.Retries < 0:
		o.Retries = 0
	}

	if o.Backoff == nil {
		o.Backoff = queue.Exponential(30 * time.Second)
	}

	if o.Now == nil {
		o.Now = time.Now
	}

	// Only the status matters, so the body is not bounded here (a large
	// Content-Length must not turn a 2xx into a retry); Deliver reads at
	// most maxResponseRead of it.
	client := httpclient.Safe(httpclient.Options{Timeout: o.Timeout, MaxBody: math.MaxInt64, Allow: o.InsecureAllow})
	// A redirect would re-send the signed body elsewhere (or drop it on a
	// 302), so 3xx is a final answer.
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

	return &Sender{o: o, client: client}, nil
}

// Job is the queue registration for deliveries; pass it to Queue.Register
// (or vukem.WithJobs).
func (s *Sender) Job() queue.Registration {
	return queue.HandleKind(DeliverKind, s.Deliver,
		queue.Retries(s.o.Retries), queue.Backoff(s.o.Backoff), queue.Timeout(s.o.Timeout+5*time.Second))
}

// Send enqueues d on q. Options such as queue.OnQueue apply to this
// dispatch.
func (s *Sender) Send(ctx context.Context, q *queue.Queue, d Delivery, opts ...queue.Option) error {
	if d.Endpoint == "" || len(d.Endpoint) > maxIDLen {
		return errors.New("webhook: delivery needs an endpoint id of at most 255 bytes")
	}

	if !json.Valid(d.Payload) {
		return errors.New("webhook: delivery payload is not valid JSON")
	}

	if d.ID == "" {
		b := make([]byte, 16)
		if _, err := rand.Read(b); err != nil {
			return fmt.Errorf("webhook: delivery id: %w", err)
		}

		d.ID = "msg_" + hex.EncodeToString(b)
	}

	if !validID(d.ID) {
		return errInvalidID
	}

	return q.DispatchKind(ctx, DeliverKind, d, opts...)
}

// Deliver makes one attempt and records it. It returns nil when the
// delivery is done: 2xx, 410 Gone, 3xx (redirects are not followed), a
// disabled endpoint or an invalid delivery id. Any other outcome is an
// error so the queue retries it. Errors never contain the URL or secret.
func (s *Sender) Deliver(ctx context.Context, d Delivery) error {
	a := Attempt{Endpoint: d.Endpoint, EventID: d.ID, At: s.o.Now().UTC()}

	if !validID(d.ID) {
		// Retrying cannot fix the id, and receivers dedupe on it.
		a.Error = AttemptInvalid
		s.record(ctx, a)
		s.o.Logger.WarnContext(ctx, "webhook: invalid delivery id, dropped", "endpoint", d.Endpoint)

		return nil
	}

	ep, err := s.o.Endpoint(ctx, d.Endpoint)
	if errors.Is(err, ErrEndpointDisabled) {
		// Recorded so the endpoint owner sees why the event never arrived.
		a.Error = AttemptDisabled
		s.record(ctx, a)
		s.o.Logger.InfoContext(ctx, "webhook: endpoint disabled, delivery dropped", "endpoint", d.Endpoint, "id", d.ID)
		return nil
	}

	if err != nil {
		return fmt.Errorf("webhook: load endpoint %q: %w", d.Endpoint, err)
	}

	req, err := s.request(ctx, d, ep, a.At)
	if err != nil {
		// Retried, so an endpoint fixed in the meantime still gets the event.
		a.Error = AttemptInvalid
		s.record(ctx, a)

		return fmt.Errorf("webhook: endpoint %q: %w", d.Endpoint, err)
	}

	start := time.Now()
	resp, err := s.client.Do(req)

	if err != nil {
		// *url.Error repeats the URL, which may carry tokens.
		if ue, ok := errors.AsType[*url.Error](err); ok {
			err = ue.Err
		}

		// Attempts are shown to endpoint owners: a category, not the
		// resolved address or TLS details (those stay in the server log).
		a.Error = category(err)
	} else {
		a.Status = resp.StatusCode
		if a.Status < 200 || a.Status >= 300 {
			a.Error = AttemptHTTPStatus
		}

		// A small answer is drained so the connection can be reused.
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxResponseRead))
		_ = resp.Body.Close()
	}

	a.Duration = time.Since(start)
	s.record(ctx, a)

	log := s.o.Logger.With("endpoint", d.Endpoint, "id", d.ID, "status", a.Status, "duration", a.Duration)

	switch {
	case err != nil:
		log.WarnContext(ctx, "webhook: delivery failed", "err", err)
		return fmt.Errorf("webhook: deliver to %q: %w", d.Endpoint, err)
	case a.Status >= 200 && a.Status < 300:
		log.InfoContext(ctx, "webhook: delivered")
		return nil
	case a.Status >= 300 && a.Status < 400:
		log.WarnContext(ctx, "webhook: endpoint redirected, not retrying")
		return nil
	case a.Status == http.StatusGone:
		log.WarnContext(ctx, "webhook: endpoint gone, not retrying")

		if s.o.OnGone != nil {
			if err := s.o.OnGone(ctx, d.Endpoint); err != nil {
				log.ErrorContext(ctx, "webhook: OnGone", "err", err)
			}
		}

		return nil
	default:
		log.WarnContext(ctx, "webhook: delivery rejected")
		return fmt.Errorf("webhook: endpoint %q answered %d", d.Endpoint, a.Status)
	}
}

// request builds the signed POST. Fail closed: never an unsigned,
// plaintext or credential-carrying request.
func (s *Sender) request(ctx context.Context, d Delivery, ep Endpoint, at time.Time) (*http.Request, error) {
	key, err := decodeSecret(ep.Secret)
	if err != nil {
		return nil, err
	}

	u, err := url.Parse(ep.URL)
	if err != nil || u.Hostname() == "" || u.User != nil ||
		(u.Scheme != "https" && (u.Scheme != "http" || !s.o.InsecureAllowHTTP)) {
		return nil, errors.New("needs an absolute https URL without credentials")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(d.Payload))
	if err != nil {
		return nil, errors.New("build request")
	}

	ts := strconv.FormatInt(at.Unix(), 10)

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "vukem-webhook")
	req.Header.Set("webhook-id", d.ID)
	req.Header.Set("webhook-timestamp", ts)
	req.Header.Set("webhook-signature", signStandard(key, d.ID, ts, d.Payload))

	return req, nil
}

// record logs an attempt; a log failure does not fail the delivery, which
// already happened and would be sent twice on retry. It outlives the job
// context: timed-out and cancelled attempts are the ones worth keeping.
func (s *Sender) record(ctx context.Context, a Attempt) {
	if s.o.Log == nil {
		return
	}

	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()

	if err := s.o.Log.Record(ctx, a); err != nil {
		s.o.Logger.ErrorContext(ctx, "webhook: record attempt", "endpoint", a.Endpoint, "id", a.EventID, "err", err)
	}
}

// Attempt.Error categories, safe to show to endpoint owners.
const (
	// AttemptBlockedAddress means the URL resolved to an internal address.
	AttemptBlockedAddress = "blocked_address"
	// AttemptTimeout means the attempt timed out or was cancelled.
	AttemptTimeout = "timeout"
	// AttemptRefused means the connection was refused.
	AttemptRefused = "refused"
	// AttemptTLS means the TLS handshake or certificate check failed.
	AttemptTLS = "tls"
	// AttemptNetwork is any other transport error (DNS, reset…).
	AttemptNetwork = "network"
	// AttemptHTTPStatus means the endpoint answered a non-2xx status.
	AttemptHTTPStatus = "http_status"
	// AttemptInvalid means an invalid delivery id, endpoint URL or secret.
	AttemptInvalid = "invalid"
	// AttemptDisabled means the endpoint was disabled (ErrEndpointDisabled)
	// and the delivery was dropped without a request.
	AttemptDisabled = "disabled"
)

const (
	maxIDLen        = 255
	maxResponseRead = 64 << 10
)

var errInvalidID = errors.New("webhook: delivery id must be 1-255 printable ASCII bytes without dots or spaces")

// validID keeps ids unambiguous in the signed "<id>.<timestamp>.<body>"
// content and safe in headers.
func validID(id string) bool {
	if id == "" || len(id) > maxIDLen {
		return false
	}

	for i := range len(id) {
		if c := id[i]; c <= ' ' || c > '~' || c == '.' {
			return false
		}
	}

	return true
}

func category(err error) string {
	switch {
	case errors.Is(err, httpclient.ErrBlockedAddress):
		return AttemptBlockedAddress
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled), isTimeout(err):
		return AttemptTimeout
	case errors.Is(err, syscall.ECONNREFUSED):
		return AttemptRefused
	case isTLS(err):
		return AttemptTLS
	default:
		return AttemptNetwork
	}
}

func isTimeout(err error) bool {
	ne, ok := errors.AsType[net.Error](err)
	return ok && ne.Timeout()
}

func isTLS(err error) bool {
	if _, ok := errors.AsType[*tls.CertificateVerificationError](err); ok {
		return true
	}

	if _, ok := errors.AsType[tls.RecordHeaderError](err); ok {
		return true
	}

	if _, ok := errors.AsType[tls.AlertError](err); ok {
		return true
	}

	msg := err.Error()

	return strings.Contains(msg, "tls:") || strings.Contains(msg, "x509:")
}
