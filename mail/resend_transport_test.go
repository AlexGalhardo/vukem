package mail

import (
	"net/http"
	"testing"

	"github.com/AlexGalhardo/vukem/config"
)

func TestResendDefaultClientOwnsItsTransport(t *testing.T) {
	t.Parallel()

	got := NewResend(ResendConfig{APIKey: config.NewSecret("k")}).cfg.HTTP.Transport
	if got == nil || got == http.DefaultTransport {
		t.Fatalf("Transport = %v, want a copy of the default transport", got)
	}
}

func TestResendKeepsACallerClient(t *testing.T) {
	t.Parallel()

	own := &http.Client{}
	if got := NewResend(ResendConfig{APIKey: config.NewSecret("k"), HTTP: own}).cfg.HTTP; got != own {
		t.Errorf("HTTP = %p, want the caller client %p", got, own)
	}
}
