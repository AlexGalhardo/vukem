package social

import (
	"context"
	"errors"
	"net/http"
	"net/url"
)

// FakeProvider signs in a fixed user without any network call: its
// authorization URL points straight back to the callback with a code, so
// tests exercise the real state cookie, callback and login code.
type FakeProvider struct {
	name     string
	callback string
	user     User
}

// Fake returns a provider named name that signs in u; callbackURL is the
// app's callback route ("/auth/github/callback").
func Fake(name, callbackURL string, u User) *FakeProvider {
	return &FakeProvider{name: name, callback: callbackURL, user: u}
}

// Name implements Provider.
func (f *FakeProvider) Name() string { return f.name }

// AuthURL implements Provider.
func (f *FakeProvider) AuthURL(_ context.Context, _ *http.Client, state, _, _ string) (string, error) {
	return f.callback + "?" + url.Values{"code": {"fake-code"}, "state": {state}}.Encode(), nil
}

// Exchange implements Provider.
func (f *FakeProvider) Exchange(_ context.Context, _ *http.Client, code, _ string) (Token, error) {
	if code != "fake-code" {
		return Token{}, errors.New("social: fake provider got an unexpected code")
	}

	return Token{Access: "fake-access-token"}, nil
}

// User implements Provider.
func (f *FakeProvider) User(context.Context, *http.Client, Token, string) (User, error) {
	return f.user, nil
}

// Use replaces (or adds) a provider; tests swap real providers for fakes.
func (s *Social) Use(p Provider) { s.providers[p.Name()] = p }

// Names lists the configured providers.
func (s *Social) Names() []string {
	names := make([]string, 0, len(s.providers))
	for n := range s.providers {
		names = append(names, n)
	}

	return names
}
