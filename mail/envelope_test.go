package mail

import (
	"context"
	"testing"
)

type localeKey struct{}

// localized builds its subject from the send's context.
type localized struct{ welcome }

func (l localized) EnvelopeContext(ctx context.Context) Envelope {
	env := l.Envelope()
	if ctx.Value(localeKey{}) == "pt-BR" {
		env.Subject = "Bem-vindo!"
	} else {
		env.Subject = "Welcome!"
	}

	return env
}

func TestRenderContextEnvelope(t *testing.T) {
	t.Parallel()

	m := New(NewArray(), "app@example.com", views(t))
	w := welcome{Name: "Ana", Email: "ana@example.com"}

	tests := []struct {
		name     string
		ctx      context.Context
		mailable Mailable
		want     string
	}{
		{"context envelope", context.WithValue(t.Context(), localeKey{}, "pt-BR"), localized{w}, "Bem-vindo!"},
		{"context envelope, default", t.Context(), localized{w}, "Welcome!"},
		{"plain envelope", t.Context(), w, "Bem-vindo, Ana!"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			msg, err := m.Render(tt.ctx, tt.mailable)
			if err != nil {
				t.Fatal(err)
			}

			if msg.Subject != tt.want {
				t.Errorf("Render().Subject = %q, want %q", msg.Subject, tt.want)
			}
		})
	}
}
