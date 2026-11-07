package mail

import (
	"context"
	"errors"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestParseResendEvent(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name, body string
		want       Event
		suppress   bool
	}{
		{
			"hard bounce", `{"type":"email.bounced","created_at":"2026-11-07T10:00:00Z","data":{"email_id":"e1",
			"to":["a@b.co"],"tags":{"campaign":"c1"},"bounce":{"type":"Permanent","message":"no such user"}}}`,
			Event{
				Type: EventBounced, EmailID: "e1", To: []string{"a@b.co"}, Tags: map[string]string{"campaign": "c1"},
				BounceType: "Permanent", BounceMessage: "no such user",
			},
			true,
		},
		{
			"soft bounce", `{"type":"email.bounced","data":{"to":["a@b.co"],"bounce":{"type":"Transient"}}}`,
			Event{Type: EventBounced, To: []string{"a@b.co"}, BounceType: "Transient"},
			false,
		},
		{
			"complaint with list tags",
			`{"type":"email.complained","data":{"to":["a@b.co"],"tags":[{"name":"k","value":"v"}]}}`,
			Event{Type: EventComplained, To: []string{"a@b.co"}, Tags: map[string]string{"k": "v"}},
			true,
		},
		{
			"delivered",
			`{"type":"email.delivered","data":{"to":["a@b.co"]}}`,
			Event{Type: EventDelivered, To: []string{"a@b.co"}},
			false,
		},
	}

	for _, tt := range tests {
		got, err := ParseResendEvent([]byte(tt.body))
		if err != nil {
			t.Fatalf("%s: %v", tt.name, err)
		}

		got.CreatedAt = tt.want.CreatedAt

		if diff := cmp.Diff(tt.want, got); diff != "" {
			t.Errorf("%s (-want +got):\n%s", tt.name, diff)
		}

		if got.Suppress() != tt.suppress {
			t.Errorf("%s: Suppress() = %v, want %v", tt.name, got.Suppress(), tt.suppress)
		}
	}

	for _, body := range []string{``, `[]`, `{"type":"domain.created"}`, `{"type":"email.sent","data":{"tags":5}}`} {
		if _, err := ParseResendEvent([]byte(body)); err == nil {
			t.Errorf("ParseResendEvent(%q) = nil error", body)
		}
	}
}

func TestMailerSuppression(t *testing.T) {
	t.Parallel()

	arr := NewArray()
	m := New(arr, "app@example.com", nil)
	m.SetSuppression(func(_ context.Context, addr string) (bool, error) { return addr == "bad@example.com", nil })

	env := Envelope{To: []string{"Bad <BAD@example.com>", "ok@example.com"}, Cc: []string{"bad@example.com"}}
	if err := m.Send(t.Context(), raw{env, Text("x")}); err != nil {
		t.Fatal(err)
	}

	got := arr.Sent()[0]
	if len(got.To) != 1 || got.To[0] != "<ok@example.com>" || len(got.Cc) != 0 {
		t.Errorf("To %q Cc %q, want only ok@example.com", got.To, got.Cc)
	}

	err := m.Send(t.Context(), raw{Envelope{To: []string{"bad@example.com"}}, Text("x")})
	if !errors.Is(err, ErrAllSuppressed) || len(arr.Sent()) != 1 {
		t.Errorf("Send(all suppressed) = %v, sent %d, want ErrAllSuppressed and nothing sent", err, len(arr.Sent()))
	}
}

func TestSecurity_A04_SuppressionCheckFailsClosed(t *testing.T) {
	t.Parallel()

	arr := NewArray()
	m := New(arr, "app@example.com", nil)
	m.SetSuppression(func(context.Context, string) (bool, error) { return false, errors.New("db down") })

	if err := m.Send(
		t.Context(),
		raw{Envelope{To: []string{"a@b.co"}}, Text("x")},
	); err == nil ||
		len(arr.Sent()) != 0 {
		t.Errorf("Send with a failing check = %v, sent %d, want an error and nothing sent", err, len(arr.Sent()))
	}
}
