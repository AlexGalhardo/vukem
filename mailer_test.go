package vukem

import (
	"fmt"
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
		"array":  "*mail.Array",
		"log":    "mail.Log",
		"":       "mail.Log",
		"smtp":   "*mail.SMTP",
		"resend": "error",
		"pigeon": "error",
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
