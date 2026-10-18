package mail

import (
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/AlexGalhardo/vukem/config"
)

// flakyTransport fails the first fails round trips with err, then answers
// 200 {"id":"x"}.
type flakyTransport struct {
	fails int32
	err   error
	calls atomic.Int32
}

func (f *flakyTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.Body != nil {
		_, _ = io.Copy(io.Discard, r.Body)
		_ = r.Body.Close()
	}

	if f.calls.Add(1) <= f.fails {
		return nil, f.err
	}

	return &http.Response{
		StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}},
		Body: io.NopCloser(strings.NewReader(`{"id":"x","success":true}`)), Request: r,
	}, nil
}

var errDialRefused = &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}

func retryMessage() *Message {
	return &Message{ID: "m1", From: "<a@example.com>", To: []string{"<b@example.com>"}, Subject: "s", Text: "t"}
}

// Network errors get the same bounded retries as 429/5xx. Resend sends an
// Idempotency-Key, so any network error is retried; Mailtrap has none, so
// only failures to connect (the request never left) are retried there.
func TestTransportsRetryNetworkErrors(t *testing.T) {
	t.Parallel()

	reset := errors.New("read: connection reset by peer")

	tests := []struct {
		name      string
		err       error
		fails     int32
		mailtrap  bool
		wantCalls int32
		wantErr   bool
	}{
		{name: "resend dial", err: errDialRefused, fails: 2, wantCalls: 3},
		{name: "resend reset", err: reset, fails: 1, wantCalls: 2},
		{name: "resend gives up", err: errDialRefused, fails: 9, wantCalls: 3, wantErr: true},
		{name: "mailtrap dial", err: errDialRefused, fails: 2, mailtrap: true, wantCalls: 3},
		{name: "mailtrap reset is not retried", err: reset, fails: 1, mailtrap: true, wantCalls: 1, wantErr: true},
		{name: "mailtrap gives up", err: errDialRefused, fails: 9, mailtrap: true, wantCalls: 3, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ft := &flakyTransport{fails: tt.fails, err: tt.err}
			client := &http.Client{Transport: ft}

			var tr Transport = NewResend(ResendConfig{
				APIKey: config.NewSecret(
					"k",
				),
				BaseURL:    "http://resend.test",
				HTTP:       client,
				MaxBackoff: time.Millisecond,
			})
			if tt.mailtrap {
				tr = NewMailtrap(MailtrapConfig{
					APIToken: config.NewSecret("k"), BaseURL: "http://mailtrap.test", HTTP: client,
					MaxBackoff: time.Millisecond,
				})
			}

			err := tr.Send(t.Context(), retryMessage())
			if (err != nil) != tt.wantErr || ft.calls.Load() != tt.wantCalls {
				t.Errorf(
					"Send() = %v after %d calls, want error %v after %d",
					err,
					ft.calls.Load(),
					tt.wantErr,
					tt.wantCalls,
				)
			}
		})
	}
}

// Tags are checked once for every transport, before anything is sent.
func TestRenderValidatesTags(t *testing.T) {
	t.Parallel()

	m := New(NewArray(), "app@example.com", nil)

	for name, tags := range map[string]map[string]string{
		"empty name":       {"": "x"},
		"line break value": {"kind": "a\nb"},
		"colon in name":    {"a:b": "x"},
		"line break name":  {"a\r\nBcc": "x"},
	} {
		err := m.Send(t.Context(), raw{Envelope{To: []string{"a@b.co"}, Tags: tags}, Text("x")})
		if err == nil || !strings.Contains(err.Error(), "mail: tag") {
			t.Errorf("%s: Send() = %v, want a tag error", name, err)
		}
	}

	if err := m.Send(
		t.Context(),
		raw{Envelope{To: []string{"a@b.co"}, Tags: map[string]string{"kind": "a b"}}, Text("x")},
	); err != nil {
		t.Errorf("Send(valid tag) = %v", err)
	}
}

// Resend only accepts ASCII letters, digits, "_" and "-" in tags: the
// transport refuses others before the request instead of a 422 later.
func TestResendRejectsInvalidTagsBeforeSending(t *testing.T) {
	t.Parallel()

	ft := &flakyTransport{}
	tr := NewResend(
		ResendConfig{APIKey: config.NewSecret("k"), BaseURL: "http://resend.test", HTTP: &http.Client{Transport: ft}},
	)

	msg := retryMessage()
	msg.Tags = map[string]string{"kind": "order paid"}

	if err := tr.Send(t.Context(), msg); err == nil || !strings.Contains(err.Error(), "mail: resend tag") {
		t.Fatalf("Send(tag with a space) = %v, want a resend tag error", err)
	}

	if ft.calls.Load() != 0 {
		t.Errorf("Send(invalid tag) made %d requests, want 0", ft.calls.Load())
	}

	msg.Tags = map[string]string{"kind": "order_paid-2"}
	if err := tr.Send(t.Context(), msg); err != nil {
		t.Errorf("Send(valid tag) = %v", err)
	}
}
