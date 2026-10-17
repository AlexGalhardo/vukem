package mail

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/AlexGalhardo/vukem/config"
)

type mailtrapRequest struct {
	Method, Path, Auth, ContentType, UserAgent string
	Body                                       any
}

func mailtrapServer(
	t *testing.T,
	status int,
	header map[string]string,
	resp string,
) (*httptest.Server, func() []mailtrapRequest) {
	t.Helper()

	var (
		mu   sync.Mutex
		reqs []mailtrapRequest
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)

		var body any
		_ = json.Unmarshal(data, &body)

		mu.Lock()
		reqs = append(reqs, mailtrapRequest{
			Method: r.Method, Path: r.URL.Path, Auth: r.Header.Get("Authorization"),
			ContentType: r.Header.Get("Content-Type"), UserAgent: r.Header.Get("User-Agent"), Body: body,
		})
		mu.Unlock()

		for k, v := range header {
			w.Header().Set(k, v)
		}

		w.WriteHeader(status)
		_, _ = w.Write([]byte(resp))
	}))
	t.Cleanup(srv.Close)

	return srv, func() []mailtrapRequest {
		mu.Lock()
		defer mu.Unlock()

		return append([]mailtrapRequest(nil), reqs...)
	}
}

func decodeJSON(t *testing.T, s string) any {
	t.Helper()

	var v any
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		t.Fatalf("bad want JSON: %v", err)
	}

	return v
}

func TestMailtrapRequestBody(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		inbox   int64
		msg     *Message
		path    string
		payload string
	}{
		{
			name: "plain text",
			msg: &Message{
				ID:      "m1",
				From:    "<app@example.com>",
				To:      []string{"<ana@example.com>"},
				Subject: "Hi",
				Text:    "Hello",
			},
			path: "/api/send",
			payload: `{"from":{"email":"app@example.com"},"to":[{"email":"ana@example.com"}],
				"subject":"Hi","text":"Hello"}`,
		},
		{
			name: "html and text with names",
			msg: &Message{
				ID: "m2", From: `"Vukem" <hello@example.com>`, To: []string{`"Ana Lima" <ana@example.com>`},
				Subject: "Olá", HTML: "<p>Oi</p>", Text: "Oi",
			},
			path: "/api/send",
			payload: `{"from":{"email":"hello@example.com","name":"Vukem"},
				"to":[{"email":"ana@example.com","name":"Ana Lima"}],"subject":"Olá","text":"Oi","html":"<p>Oi</p>"}`,
		},
		{
			name: "attachments and inline image",
			msg: &Message{
				ID: "m3", From: "<app@example.com>", To: []string{"<ana@example.com>"}, Subject: "Invoice",
				HTML: `<img src="cid:logo">`,
				Attachments: []Attachment{
					{Filename: "invoice.pdf", ContentType: "application/pdf", Data: []byte("%PDF")},
					{
						Filename:    "logo.png",
						ContentType: "image/png",
						Data:        []byte{0x89, 'P'},
						Inline:      true,
						ContentID:   "logo",
					},
				},
			},
			path: "/api/send",
			payload: `{"from":{"email":"app@example.com"},"to":[{"email":"ana@example.com"}],"subject":"Invoice",
				"html":"<img src=\"cid:logo\">","attachments":[
				{"content":"JVBERg==","filename":"invoice.pdf","type":"application/pdf","disposition":"attachment"},
				{"content":"iVA=","filename":"logo.png","type":"image/png","disposition":"inline","content_id":"logo"}]}`,
		},
		{
			name: "cc bcc reply-to and tags",
			msg: &Message{
				ID: "m4", From: "<app@example.com>", To: []string{"<ana@example.com>"},
				Cc: []string{"<bia@example.com>"}, Bcc: []string{"<audit@example.com>"},
				ReplyTo: []string{`"Support" <support@example.com>`}, Subject: "Hi", Text: "Hello",
				Tags: map[string]string{"kind": "welcome", "category": "onboarding"},
			},
			path: "/api/send",
			payload: `{"from":{"email":"app@example.com"},"to":[{"email":"ana@example.com"}],
				"cc":[{"email":"bia@example.com"}],"bcc":[{"email":"audit@example.com"}],
				"reply_to":{"email":"support@example.com","name":"Support"},"subject":"Hi","text":"Hello",
				"category":"onboarding","custom_variables":{"kind":"welcome"}}`,
		},
		{
			name:  "sandbox inbox",
			inbox: 4242,
			msg: &Message{
				ID:      "m5",
				From:    "<app@example.com>",
				To:      []string{"<ana@example.com>"},
				Subject: "Hi",
				Text:    "Hello",
			},
			path: "/api/send/4242",
			payload: `{"from":{"email":"app@example.com"},"to":[{"email":"ana@example.com"}],
				"subject":"Hi","text":"Hello"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			srv, got := mailtrapServer(t, http.StatusOK, nil, `{"success":true,"message_ids":["id-1"]}`)

			tr := NewMailtrap(
				MailtrapConfig{APIToken: config.NewSecret("mt_token"), InboxID: tt.inbox, BaseURL: srv.URL},
			)
			if err := tr.Send(t.Context(), tt.msg); err != nil {
				t.Fatalf("Send() = %v", err)
			}

			want := []mailtrapRequest{{
				Method: http.MethodPost, Path: tt.path, Auth: "Bearer mt_token",
				ContentType: "application/json", UserAgent: mailtrapUserAgent, Body: decodeJSON(t, tt.payload),
			}}
			if diff := cmp.Diff(want, got()); diff != "" {
				t.Errorf("request mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestMailtrapDefaultURLs(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		inbox int64
		want  string
	}{
		{0, "https://send.api.mailtrap.io/api/send"},
		{7, "https://sandbox.api.mailtrap.io/api/send/7"},
	} {
		if got := NewMailtrap(
			MailtrapConfig{APIToken: config.NewSecret("x"), InboxID: tt.inbox},
		).endpoint(); got != tt.want {
			t.Errorf("endpoint(InboxID=%d) = %s, want %s", tt.inbox, got, tt.want)
		}
	}
}

func TestMailtrapErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		status    int
		header    map[string]string
		resp      string
		calls     int
		retryable bool
		contains  string
	}{
		{
			"400 lists errors", http.StatusBadRequest, nil,
			`{"success":false,"errors":["'from' address is invalid","'subject' is required"]}`,
			1, false, "mailtrap 400: 'from' address is invalid; 'subject' is required",
		},
		{
			"401",
			http.StatusUnauthorized,
			nil,
			`{"success":false,"errors":["Unauthorized"]}`,
			1,
			false,
			"mailtrap 401: Unauthorized",
		},
		{"403", http.StatusForbidden, nil, `{"errors":["domain not verified"]}`, 1, false, "mailtrap 403"},
		{
			"429 retried", http.StatusTooManyRequests,
			map[string]string{"Retry-After": "1"},
			`{"success":false,"errors":["Rate limit exceeded"]}`, 3, true, "mailtrap 429",
		},
		{
			"500 retried",
			http.StatusInternalServerError,
			nil,
			`{"success":false,"errors":["Internal error"]}`,
			3,
			true,
			"mailtrap 500",
		},
		{"502 without body", http.StatusBadGateway, nil, `<html>bad gateway</html>`, 3, true, "mailtrap 502"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			srv, got := mailtrapServer(t, tt.status, tt.header, tt.resp)

			tr := NewMailtrap(
				MailtrapConfig{APIToken: config.NewSecret("mt_token"), BaseURL: srv.URL, MaxBackoff: time.Millisecond},
			)
			err := tr.Send(
				t.Context(),
				&Message{From: "<a@example.com>", To: []string{"<b@example.com>"}, Subject: "s", Text: "t"},
			)

			me, ok := errors.AsType[*MailtrapError](err)
			if !ok || me.Status != tt.status || me.Retryable() != tt.retryable {
				t.Fatalf("Send() = %v, want *MailtrapError %d retryable=%v", err, tt.status, tt.retryable)
			}

			if !strings.Contains(err.Error(), tt.contains) {
				t.Errorf("Error() = %q, want it to contain %q", err.Error(), tt.contains)
			}

			if n := len(got()); n != tt.calls {
				t.Errorf("calls = %d, want %d", n, tt.calls)
			}
		})
	}
}

func TestMailtrapRetryAfterThenSuccess(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)

			return
		}

		_, _ = w.Write([]byte(`{"success":true,"message_ids":["x"]}`))
	}))
	defer srv.Close()

	tr := NewMailtrap(MailtrapConfig{APIToken: config.NewSecret("t"), BaseURL: srv.URL, MaxBackoff: time.Millisecond})
	if err := tr.Send(
		t.Context(),
		&Message{From: "<a@example.com>", To: []string{"<b@example.com>"}, Subject: "s", Text: "t"},
	); err != nil {
		t.Fatalf("Send() = %v", err)
	}

	if calls.Load() != 2 {
		t.Fatalf("calls = %d, want 2", calls.Load())
	}
}

func TestMailtrapRejectsBeforeSending(t *testing.T) {
	t.Parallel()

	ok := &Message{From: "<a@example.com>", To: []string{"<b@example.com>"}, Subject: "s", Text: "t"}

	tests := []struct {
		name string
		cfg  MailtrapConfig
		msg  *Message
	}{
		{"missing token", MailtrapConfig{}, ok},
		{"negative inbox", MailtrapConfig{APIToken: config.NewSecret("t"), InboxID: -1}, ok},
		{"two reply-to", MailtrapConfig{APIToken: config.NewSecret("t")}, &Message{
			From: "<a@example.com>", To: []string{"<b@example.com>"}, Subject: "s", Text: "t",
			ReplyTo: []string{"<c@example.com>", "<d@example.com>"},
		}},
		{"bad address", MailtrapConfig{APIToken: config.NewSecret("t")}, &Message{
			From: "not an address", To: []string{"<b@example.com>"}, Subject: "s", Text: "t",
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			srv, got := mailtrapServer(t, http.StatusOK, nil, `{"success":true}`)

			tt.cfg.BaseURL = srv.URL
			if err := NewMailtrap(tt.cfg).Send(t.Context(), tt.msg); err == nil {
				t.Fatal("Send() = nil, want an error")
			}

			if n := len(got()); n != 0 {
				t.Errorf("calls = %d, want 0 (fail before any request)", n)
			}
		})
	}
}

func TestSecurity_A09_MailtrapTokenRedacted(t *testing.T) {
	t.Parallel()

	// A hostile or buggy upstream echoing the token must not leak it either.
	srv, _ := mailtrapServer(t, http.StatusUnauthorized, nil,
		`{"success":false,"errors":["invalid token mt_live_SUPERSECRET"]}`)

	token := config.NewSecret("mt_live_SUPERSECRET")
	cfg := MailtrapConfig{APIToken: token, BaseURL: srv.URL}

	var logs bytes.Buffer

	log := slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	log.Info("config", "mailtrap", cfg)

	err := New(NewMailtrap(cfg), "app@example.com", nil).
		Send(t.Context(), raw{Envelope{To: []string{"a@b.co"}}, Text("secret body text")})

	if _, ok := errors.AsType[*MailtrapError](err); !ok {
		t.Fatalf("Send() = %v, want *MailtrapError", err)
	}

	jsonToken, jerr := json.Marshal(token)
	if jerr != nil {
		t.Fatal(jerr)
	}

	for name, out := range map[string]string{"error": err.Error(), "log": logs.String(), "json": string(jsonToken)} {
		if strings.Contains(out, "SUPERSECRET") {
			t.Errorf("API token leaked in %s: %s", name, out)
		}

		if name == "error" && strings.Contains(out, "secret body text") {
			t.Errorf("message body leaked in error: %s", out)
		}
	}

	down := NewMailtrap(
		MailtrapConfig{APIToken: token, BaseURL: "http://127.0.0.1:1", HTTP: &http.Client{Timeout: time.Second}},
	)
	if err := down.Send(
		t.Context(),
		&Message{From: "<a@example.com>", To: []string{"<b@example.com>"}, Subject: "s", Text: "t"},
	); err == nil ||
		strings.Contains(err.Error(), "SUPERSECRET") {
		t.Fatalf("closed port Send() = %v, want an error without the token", err)
	}
}

func TestMailtrapErrorShapes(t *testing.T) {
	t.Parallel()

	long := strings.Repeat("x", 300)

	tests := []struct {
		name   string
		status int
		resp   string
		want   []string
	}{
		{
			"errors object",
			http.StatusUnprocessableEntity,
			`{"errors":{"from":["is invalid"]}}`,
			[]string{`{"from":["is invalid"]}`},
		},
		{
			"message field",
			http.StatusForbidden,
			`{"message":"Sending domain not allowed"}`,
			[]string{"Sending domain not allowed"},
		},
		{"error field", http.StatusUnauthorized, `{"error":"Incorrect API token"}`, []string{"Incorrect API token"}},
		{"raw body", http.StatusBadGateway, "bad\r\ngateway", []string{"bad  gateway"}},
		{
			"capped", http.StatusBadRequest,
			`{"errors":["a\nb","2","3","4","5","6","` + long + `"]}`,
			[]string{"a b", "2", "3", "4", "5"},
		},
		{"long item", http.StatusBadRequest, `{"errors":["` + long + `"]}`, []string{strings.Repeat("x", 200)}},
		{
			"success false on 200",
			http.StatusOK,
			`{"success":false,"errors":["Recipient blocked"]}`,
			[]string{"Recipient blocked"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			srv, got := mailtrapServer(t, tt.status, nil, tt.resp)

			tr := NewMailtrap(
				MailtrapConfig{APIToken: config.NewSecret("mt_token"), BaseURL: srv.URL, MaxBackoff: time.Millisecond},
			)
			err := tr.Send(
				t.Context(),
				&Message{From: "<a@example.com>", To: []string{"<b@example.com>"}, Subject: "s", Text: "t"},
			)

			me, ok := errors.AsType[*MailtrapError](err)
			if !ok || me.Status != tt.status {
				t.Fatalf("Send() = %v, want *MailtrapError %d", err, tt.status)
			}

			if diff := cmp.Diff(tt.want, me.Errors); diff != "" {
				t.Errorf("Errors mismatch (-want +got):\n%s", diff)
			}

			if strings.ContainsAny(err.Error(), "\r\n") {
				t.Errorf("Error() = %q, want no line breaks", err.Error())
			}

			if tt.status == http.StatusOK && (me.Retryable() || len(got()) != 1) {
				t.Errorf("success:false retryable=%v calls=%d, want false and 1", me.Retryable(), len(got()))
			}
		})
	}
}

func TestMailtrapErrorStringSanitized(t *testing.T) {
	t.Parallel()

	e := &MailtrapError{Status: 400, Errors: []string{"a\r\nb", "2", "3", "4", "5", "6", strings.Repeat("y", 300)}}
	if got, want := e.Error(), "mail: mailtrap 400: a  b; 2; 3; 4; 5"; got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}

func TestMailtrapDoesNotFollowRedirects(t *testing.T) {
	t.Parallel()

	var hits atomic.Int32

	target := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) { hits.Add(1) }))
	defer target.Close()

	srv, _ := mailtrapServer(t, http.StatusTemporaryRedirect, map[string]string{"Location": target.URL}, "")

	err := NewMailtrap(MailtrapConfig{APIToken: config.NewSecret("t"), BaseURL: srv.URL}).
		Send(t.Context(), &Message{From: "<a@example.com>", To: []string{"<b@example.com>"}, Subject: "s", Text: "t"})

	if me, ok := errors.AsType[*MailtrapError](err); !ok || me.Status != http.StatusTemporaryRedirect {
		t.Fatalf("Send() = %v, want *MailtrapError 307", err)
	}

	if hits.Load() != 0 {
		t.Fatalf("redirect followed %d times, want 0 (the token would go to the new host)", hits.Load())
	}
}
