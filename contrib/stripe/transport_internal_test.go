package stripe

import (
	"net/http"
	"testing"
)

func TestDefaultClientOwnsItsTransport(t *testing.T) {
	t.Parallel()

	c, err := New("sk_test_x")
	if err != nil {
		t.Fatal(err)
	}

	if c.http.Transport == nil || c.http.Transport == http.DefaultTransport {
		t.Fatalf("Transport = %v, want a copy of the default transport", c.http.Transport)
	}
}

func TestCallerClientIsKept(t *testing.T) {
	t.Parallel()

	own := &http.Client{}

	c, err := New("sk_test_x", WithHTTPClient(own))
	if err != nil {
		t.Fatal(err)
	}

	if c.http != own || own.Transport != nil {
		t.Errorf("caller client replaced or modified")
	}
}
