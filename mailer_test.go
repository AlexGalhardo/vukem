package vukem

import (
	"bytes"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/AlexGalhardo/vukem/mail"
)

type hello struct{}

func (hello) Envelope() mail.Envelope { return mail.Envelope{To: []string{"a@b.co"}, Subject: "Hi"} }
func (hello) Content() mail.Content   { return mail.View("mail/hello", nil) }

func TestMailerFromConfig(t *testing.T) {
	t.Parallel()

	for env, want := range map[string]string{
		"array":    "*mail.Array",
		"log":      "mail.Log",
		"":         "mail.Log",
		"smtp":     "*mail.SMTP",
		"resend":   "error",
		"mailtrap": "error",
		"pigeon":   "error",
	} {
		app, err := New(WithSource(testSource(map[string]string{"MAIL_MAILER": env})), WithLogger(discardLogger()))
		if err != nil {
			t.Fatal(err)
		}

		m, err := app.Mailer()

		got := "error"
		if err == nil {
			got = strings.TrimPrefix(typeName(m.Transport()), "")
		}

		if got != want {
			t.Errorf("MAIL_MAILER=%q: transport %s (err %v), want %s", env, got, err, want)
		}
	}

	prod, _ := New(WithSource(testSource(map[string]string{"APP_ENV": "production"})), WithLogger(discardLogger()))
	if _, err := prod.Mailer(); err == nil {
		t.Fatal("production without MAIL_MAILER accepted")
	}

	withKey, _ := New(WithSource(testSource(map[string]string{"MAIL_MAILER": "resend", "RESEND_API_KEY": "re_x"})),
		WithLogger(discardLogger()))
	if m, err := withKey.Mailer(); err != nil || typeName(m.Transport()) != "*mail.Resend" {
		t.Fatalf("resend = %v", err)
	}
}

func TestMailerMailtrapFromConfig(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		env     map[string]string
		wantErr string
	}{
		{"sending", map[string]string{"MAILTRAP_API_TOKEN": "mt_x"}, ""},
		{"sandbox", map[string]string{"MAILTRAP_API_TOKEN": "mt_x", "MAILTRAP_INBOX_ID": "4242"}, ""},
		{"missing token", map[string]string{"MAILTRAP_INBOX_ID": "4242"}, "MAILTRAP_API_TOKEN"},
		{
			"negative inbox",
			map[string]string{"MAILTRAP_API_TOKEN": "mt_x", "MAILTRAP_INBOX_ID": "-1"},
			"MAILTRAP_INBOX_ID",
		},
		{"production without token", map[string]string{"APP_ENV": "production"}, "MAILTRAP_API_TOKEN"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tt.env["MAIL_MAILER"] = "mailtrap"

			app, err := New(WithSource(testSource(tt.env)), WithLogger(discardLogger()))
			if err != nil {
				t.Fatal(err)
			}

			m, err := app.Mailer()
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("Mailer() = %v, want an error naming %s", err, tt.wantErr)
				}

				if strings.Contains(err.Error(), "mt_x") {
					t.Fatalf("Mailer() error leaks the token: %v", err)
				}

				return
			}

			if err != nil || typeName(m.Transport()) != "*mail.Mailtrap" {
				t.Fatalf("Mailer() = %v, %v, want *mail.Mailtrap", m, err)
			}
		})
	}
}

func TestMailerUsesAppViews(t *testing.T) {
	t.Parallel()

	arr := mail.NewArray()
	app, err := New(WithSource(testSource(nil)), WithLogger(discardLogger()), WithMailTransport(arr),
		WithViews(fstest.MapFS{"mail/hello.html": {Data: []byte("{{/* layout: none */}}<p>Hello</p>")}}))
	if err != nil {
		t.Fatal(err)
	}

	m, err := app.Mailer()
	if err != nil {
		t.Fatal(err)
	}

	if err := m.Send(t.Context(), hello{}); err != nil {
		t.Fatal(err)
	}

	if sent := arr.Sent(); len(sent) != 1 || sent[0].HTML != "<p>Hello</p>" || sent[0].Text != "Hello" {
		t.Fatalf("sent = %+v", sent)
	}
}

func typeName(v any) string {
	return strings.ReplaceAll(fmtType(v), "github.com/AlexGalhardo/vukem/", "")
}

func fmtType(v any) string { return fmt.Sprintf("%T", v) }

func TestMailerMailtrapSandboxInProductionWarns(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		env  map[string]string
		warn bool
	}{
		{map[string]string{"APP_ENV": "production", "MAILTRAP_INBOX_ID": "4242"}, true},
		{map[string]string{"APP_ENV": "production"}, false},
		{map[string]string{"MAILTRAP_INBOX_ID": "4242"}, false},
	} {
		tt.env["MAIL_MAILER"] = "mailtrap"
		tt.env["MAILTRAP_API_TOKEN"] = "mt_x"

		var logs bytes.Buffer

		app, err := New(WithSource(testSource(tt.env)), WithLogger(slog.New(slog.NewTextHandler(&logs, nil))))
		if err != nil {
			t.Fatal(err)
		}

		if _, err := app.Mailer(); err != nil {
			t.Fatalf("Mailer(%v) = %v", tt.env, err)
		}

		if got := strings.Contains(logs.String(), "Mailtrap sandbox"); got != tt.warn {
			t.Errorf("Mailer(%v) warned = %v, want %v; logs:\n%s", tt.env, got, tt.warn, logs.String())
		}
	}
}
