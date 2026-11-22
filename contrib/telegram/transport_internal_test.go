package telegram

import (
	"net/http"
	"testing"

	"github.com/AlexGalhardo/vukem/config"
)

func TestDefaultClientOwnsItsTransport(t *testing.T) {
	t.Parallel()

	b, err := New(Config{Token: config.NewSecret("123:abc")})
	if err != nil {
		t.Fatal(err)
	}

	if b.cfg.HTTP.Transport == nil || b.cfg.HTTP.Transport == http.DefaultTransport {
		t.Fatalf("Transport = %v, want a copy of the default transport", b.cfg.HTTP.Transport)
	}
}

func TestCallerClientIsKept(t *testing.T) {
	t.Parallel()

	own := &http.Client{}

	b, err := New(Config{Token: config.NewSecret("123:abc"), HTTP: own})
	if err != nil {
		t.Fatal(err)
	}

	if b.cfg.HTTP != own || own.Transport != nil {
		t.Errorf("caller client replaced or modified")
	}
}
