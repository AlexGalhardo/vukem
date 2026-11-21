package oauth_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/AlexGalhardo/vukem/oauth"
)

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func reply(status int, header http.Header, body string) roundTrip {
	return func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: status, Header: header, Body: io.NopCloser(strings.NewReader(body))}, nil
	}
}

func docClient(rt http.RoundTripper) *oauth.MetadataDocuments {
	return &oauth.MetadataDocuments{HTTP: &http.Client{Transport: rt}}
}

const goodDoc = `{"client_id":"https://client.example.com/meta.json","client_name":"C","redirect_uris":["https://client.example.com/cb"],"token_endpoint_auth_method":"none"}`

func TestMetadataDocumentClient(t *testing.T) {
	t.Parallel()

	id := "https://client.example.com/meta.json"

	c, err := docClient(reply(200, nil, goodDoc)).Client(t.Context(), id)
	if err != nil || c.ID != id || len(c.RedirectURIs) != 1 || len(c.SecretHash) != 0 {
		t.Fatalf("Client = %+v, %v", c, err)
	}

	tests := map[string]roundTrip{
		"redirect is not followed": reply(302, http.Header{"Location": {"https://client.example.com/other"}}, ""),
		"not found":                reply(404, nil, ""),
		"too large":                reply(200, nil, `{"client_id":"`+id+`","pad":"`+strings.Repeat("a", 6000)+`"}`),
		"not json":                 reply(200, nil, "<html>"),
		"another client id":        reply(200, nil, strings.ReplaceAll(goodDoc, "meta.json", "other.json")),
		"no redirect uris":         reply(200, nil, `{"client_id":"`+id+`","redirect_uris":[]}`),
		"javascript redirect":      reply(200, nil, `{"client_id":"`+id+`","redirect_uris":["javascript:alert(1)"]}`),
		"fragment redirect": reply(
			200,
			nil,
			`{"client_id":"`+id+`","redirect_uris":["https://a.example.com/cb#x"]}`,
		),
		"plain http redirect": reply(
			200,
			nil,
			`{"client_id":"`+id+`","redirect_uris":["http://a.example.com/cb"]}`,
		),
		"carries a secret": reply(
			200,
			nil,
			`{"client_id":"`+id+`","redirect_uris":["https://a.example.com/cb"],"client_secret":"x"}`,
		),
		"secret auth method": reply(
			200,
			nil,
			`{"client_id":"`+id+`","redirect_uris":["https://a.example.com/cb"],"token_endpoint_auth_method":"client_secret_basic"}`,
		),
	}

	for name, rt := range tests {
		if _, err := docClient(rt).Client(t.Context(), id); !errors.Is(err, oauth.ErrUnknownClient) {
			t.Errorf("%s: Client = %v, want ErrUnknownClient", name, err)
		}
	}
}

// The redirect test must see the 302, not a followed hop.
func TestMetadataDocumentDoesNotFollowRedirects(t *testing.T) {
	t.Parallel()

	var hops atomic.Int32

	client := &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		hops.Add(1)

		return &http.Response{
			StatusCode: http.StatusFound,
			Header:     http.Header{"Location": {"https://127.0.0.1/"}},
			Body:       http.NoBody,
			Request:    r,
		}, nil
	})}

	if _, err := (&oauth.MetadataDocuments{HTTP: client}).Client(
		t.Context(),
		"https://client.example.com/m.json",
	); err == nil {
		t.Error("a redirecting document was accepted")
	}

	if hops.Load() != 1 {
		t.Errorf("requests = %d, want 1: the redirect must not be followed", hops.Load())
	}
}

func TestSecurity_A10_MetadataDocumentSSRF(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32

	rt := roundTrip(func(*http.Request) (*http.Response, error) {
		calls.Add(1)

		return nil, errors.New("must not be called")
	})

	// Literal addresses never reach even a custom client.
	for _, id := range []string{
		"https://127.0.0.1/c", "https://[::1]/c", "https://[::ffff:127.0.0.1]/c", "https://[::ffff:7f00:1]/c",
		"https://169.254.169.254/latest/meta-data", "https://10.0.0.1/c", "https://192.168.1.1/c", "https://172.16.0.1/c",
		"https://0.0.0.0/c", "https://[fe80::1]/c", "https://[fc00::1]/c",
		"http://client.example.com/c", "https://user:pw@client.example.com/c", "https://client.example.com/c#f",
		"https://client.example.com", "https://client.example.com/", "ftp://client.example.com/c", "client", "",
	} {
		if _, err := docClient(rt).Client(t.Context(), id); !errors.Is(err, oauth.ErrUnknownClient) {
			t.Errorf("Client(%q) = %v, want ErrUnknownClient", id, err)
		}
	}

	if calls.Load() != 0 {
		t.Errorf("a custom client was called %d times for a refused client_id", calls.Load())
	}

	// The default client refuses the addresses again when the socket
	// connects: hostnames that resolve to loopback, and the forms that embed
	// an IPv4 address, never get a connection.
	def := &oauth.MetadataDocuments{}

	for _, id := range []string{
		"https://localhost/c", "https://127.0.0.1.nip.io/c", "https://[64:ff9b::7f00:1]/c", "https://[2002:7f00:1::]/c",
		"https://[::ffff:10.0.0.1]/c",
	} {
		ctx, cancel := context.WithTimeout(t.Context(), 5_000_000_000)

		_, err := def.Client(ctx, id)

		cancel()

		if !errors.Is(err, oauth.ErrUnknownClient) {
			t.Errorf("default client for %q = %v, want ErrUnknownClient", id, err)
		}
	}
}

func TestSecurity_RedirectURIsOfClients(t *testing.T) {
	t.Parallel()

	rt := reply(
		200,
		nil,
		`{"client_id":"https://c.example.com/m","redirect_uris":["http://127.0.0.1:8080/cb","com.example.app:/cb","http://[::1]/cb"]}`,
	)

	c, err := docClient(rt).Client(t.Context(), "https://c.example.com/m")
	if err != nil || len(c.RedirectURIs) != 3 {
		t.Fatalf("native redirect URIs = %+v, %v, want them accepted", c, err)
	}
}
