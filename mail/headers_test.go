package mail

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	netmail "net/mail"
	"strings"
	"testing"

	"github.com/AlexGalhardo/vukem/config"
)

func campaign(env Envelope) raw {
	env.To = []string{"a@b.co"}

	return raw{env, Text("x")}
}

func TestListUnsubscribeSetsBothHeaders(t *testing.T) {
	t.Parallel()

	arr := NewArray()
	m := New(arr, "app@example.com", nil)
	base := Envelope{Headers: map[string]string{"Precedence": "bulk"}}
	env := base.ListUnsubscribe("https://example.com/u/1", "unsub@example.com")

	if err := m.Send(t.Context(), campaign(env)); err != nil {
		t.Fatal(err)
	}

	h := arr.Sent()[0].Headers
	if h["List-Unsubscribe"] != "<https://example.com/u/1>, <mailto:unsub@example.com>" ||
		h["List-Unsubscribe-Post"] != "List-Unsubscribe=One-Click" || h["Precedence"] != "bulk" {
		t.Errorf("Headers = %v", h)
	}

	if _, ok := base.Headers["List-Unsubscribe"]; ok {
		t.Error("ListUnsubscribe modified the receiver's map")
	}

	arr.Reset()

	if err := m.Send(t.Context(), campaign(Envelope{}.ListUnsubscribe("", "mailto:u@example.com"))); err != nil {
		t.Fatal(err)
	}

	if h := arr.Sent()[0].Headers; h["List-Unsubscribe"] != "<mailto:u@example.com>" ||
		h["List-Unsubscribe-Post"] != "" {
		t.Errorf("mailto-only Headers = %v", h)
	}
}

func TestSecurity_A05_MailHeadersInjection(t *testing.T) {
	t.Parallel()

	m := New(NewArray(), "app@example.com", nil)

	tests := map[string]Envelope{
		"line break value":  {Headers: map[string]string{"X-A": "a\r\nBcc: evil@example.com"}},
		"line break name":   {Headers: map[string]string{"X-A\r\nBcc": "a"}},
		"colon in name":     {Headers: map[string]string{"X-A:": "a"}},
		"empty name":        {Headers: map[string]string{"": "a"}},
		"nul value":         {Headers: map[string]string{"X-A": "a\x00"}},
		"subject override":  {Headers: map[string]string{"subject": "x"}},
		"from override":     {Headers: map[string]string{"From": "x@y.co"}},
		"bcc override":      {Headers: map[string]string{"BCC": "x@y.co"}},
		"dkim override":     {Headers: map[string]string{"DKIM-Signature": "x"}},
		"content type":      {Headers: map[string]string{"Content-Type": "text/html"}},
		"tag collision":     {Headers: map[string]string{"X-Tag-Kind": "x"}},
		"case duplicate":    {Headers: map[string]string{"X-A": "1", "x-a": "2"}},
		"url with comma":    Envelope{}.ListUnsubscribe("https://e.com/a,<https://evil.test>", ""),
		"url with brackets": Envelope{}.ListUnsubscribe("https://e.com/>", ""),
		"http url":          Envelope{}.ListUnsubscribe("http://e.com/u", ""),
		"javascript url":    Envelope{}.ListUnsubscribe("javascript:alert(1)", ""),
		"url with newline":  Envelope{}.ListUnsubscribe("https://e.com/\r\nBcc: x@y.co", ""),
		"mailto with name":  Envelope{}.ListUnsubscribe("", "Eve <e@y.co>"),
		"nothing":           Envelope{}.ListUnsubscribe("", ""),
	}

	for name, env := range tests {
		err := m.Send(t.Context(), campaign(env))
		if err == nil || !strings.Contains(err.Error(), "mail:") {
			t.Errorf("%s: Send() = %v, want an error", name, err)
		}
	}
}

func TestResendForwardsHeaders(t *testing.T) {
	t.Parallel()

	var body []byte

	tr := NewResend(ResendConfig{
		APIKey: config.NewSecret("k"), BaseURL: "http://resend.test",
		HTTP: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			body, _ = io.ReadAll(r.Body)

			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{}`)),
				Request:    r,
			}, nil
		})},
	})

	msg := retryMessage()
	msg.Headers = map[string]string{"List-Unsubscribe": "<https://e.com/u>", "Auto-Submitted": "auto-generated"}

	if err := tr.Send(t.Context(), msg); err != nil {
		t.Fatal(err)
	}

	var got struct {
		Headers map[string]string `json:"headers"`
	}
	if err := json.Unmarshal(body, &got); err != nil || got.Headers["List-Unsubscribe"] != "<https://e.com/u>" ||
		got.Headers["Auto-Submitted"] != "auto-generated" {
		t.Errorf("payload headers = %v (err %v), body %s", got.Headers, err, body)
	}

	msg.Headers = map[string]string{"X-A": "a\nBcc: x@y.co"}
	if err := tr.Send(t.Context(), msg); err == nil {
		t.Error("Send(header with line break) = nil, want an error")
	}
}

func TestMailtrapForwardsHeaders(t *testing.T) {
	t.Parallel()

	msg := retryMessage()
	msg.Headers = map[string]string{"List-Unsubscribe": "<https://e.com/u>"}

	p, err := mailtrapPayload(msg)
	if err != nil || p.Headers["List-Unsubscribe"] != "<https://e.com/u>" {
		t.Errorf("mailtrapPayload headers = %v, err %v", p.Headers, err)
	}

	msg.Headers = map[string]string{"X-A": "a\r\nBcc: x@y.co"}
	if _, err := mailtrapPayload(msg); err == nil {
		t.Error("mailtrapPayload(header with line break) = nil error")
	}
}

func TestBuildWritesHeaders(t *testing.T) {
	t.Parallel()

	msg := retryMessage()
	msg.Headers = map[string]string{"List-Unsubscribe": "<https://e.com/u>", "Precedence": "bulk"}

	b, err := Build(msg)
	if err != nil {
		t.Fatal(err)
	}

	parsed, err := netmail.ReadMessage(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}

	if parsed.Header.Get("List-Unsubscribe") != "<https://e.com/u>" || parsed.Header.Get("Precedence") != "bulk" {
		t.Errorf("headers = %v", parsed.Header)
	}

	msg.Headers = map[string]string{"Bcc": "x@y.co"}
	if _, err := Build(msg); err == nil {
		t.Error("Build(Bcc header) = nil error")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
