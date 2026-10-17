package mail_test

import (
	"context"
	"fmt"

	"github.com/AlexGalhardo/vukem/mail"
)

type Welcome struct{ Email string }

func (m Welcome) Envelope() mail.Envelope {
	return mail.Envelope{To: []string{m.Email}, Subject: "Welcome!"}
}

func (m Welcome) Content() mail.Content { return mail.Text("Thanks for signing up.") }

func Example() {
	// Array keeps messages in memory (tests); apps configure SMTP or
	// Resend through MAIL_* variables.
	sent := mail.NewArray()
	m := mail.New(sent, "Shop <hello@shop.test>", nil)

	if err := m.Send(context.Background(), Welcome{Email: "ada@example.com"}); err != nil {
		fmt.Println(err)
		return
	}

	msgs := sent.SentTo("ada@example.com")
	for i := range msgs {
		fmt.Println(msgs[i].From, "|", msgs[i].Subject, "|", msgs[i].Text)
	}
	// Output: "Shop" <hello@shop.test> | Welcome! | Thanks for signing up.
}
