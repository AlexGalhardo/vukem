package accounts

import (
	"testing"

	"github.com/AlexGalhardo/vukem"
	"github.com/AlexGalhardo/vukem/vukemtest"
)

func TestSecurity_GooglePartialCredentialsFailClosed(t *testing.T) {
	for _, env := range []map[string]string{
		{"SOCIAL_GOOGLE_CLIENT_ID": "id"},
		{"SOCIAL_GOOGLE_CLIENT_SECRET": "secret"},
	} {
		t.Run("partial", func(t *testing.T) {
			app := vukemtest.New(t, vukem.New, vukemtest.Options{Env: env})
			if _, err := NewSocial(app.App); err == nil {
				t.Fatal("partial OAuth configuration accepted")
			}
		})
	}
}

func TestSecurity_GoogleFakeRestrictedToTests(t *testing.T) {
	app := vukemtest.New(t, vukem.New, vukemtest.Options{Env: map[string]string{"SOCIAL_FAKE_ID": "fake-id"}})
	for _, kind := range []vukem.Kind{vukem.KindWeb, vukem.KindConsole} {
		app.Kind = kind
		if GoogleEnabled(app.App) {
			t.Fatalf("fake Google enabled in %s", kind)
		}
		s, err := NewSocial(app.App)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.Provider("google"); err == nil {
			t.Fatalf("fake Google registered in %s", kind)
		}
	}
}

func TestGoogleConfiguredProvider(t *testing.T) {
	app := vukemtest.New(t, vukem.New, vukemtest.Options{Env: map[string]string{
		"SOCIAL_GOOGLE_CLIENT_ID":     "configured-id",
		"SOCIAL_GOOGLE_CLIENT_SECRET": "configured-secret",
	}})
	if !GoogleEnabled(app.App) {
		t.Fatal("configured Google missing")
	}
	s, err := NewSocial(app.App)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Provider("google"); err != nil {
		t.Fatal(err)
	}
}
