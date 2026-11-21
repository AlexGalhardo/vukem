package oauth_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/AlexGalhardo/vukem/oauth"
)

func TestSecurityRevokedGrantEndsAccessToken(t *testing.T) {
	t.Parallel()

	for name, mk := range stores(t) {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			e := newEnv(t, mk(), nil)

			tk, status := e.exchange(e.approve(t, authQuery(nil)), verifier)
			if status != http.StatusOK {
				t.Fatalf("token = %+v (%d)", tk, status)
			}

			if _, err := e.srv.VerifyAccessToken(t.Context(), tk.AccessToken, resource); err != nil {
				t.Fatalf("token refused before the revocation: %v", err)
			}

			form := url.Values{"client_id": {clientID}, "token": {tk.RefreshToken}}
			if rec := e.post(e.srv.RevokeHandler(), "/revoke", form, nil); rec.Code != http.StatusOK {
				t.Fatalf("revoke = %d", rec.Code)
			}

			_, err := e.srv.VerifyAccessToken(t.Context(), tk.AccessToken, resource)
			if !errors.Is(err, oauth.ErrInvalidToken) {
				t.Fatalf("token after the revocation: %v, want ErrInvalidToken", err)
			}
		})
	}
}

func TestSecurityConsentBodyIsBounded(t *testing.T) {
	t.Parallel()

	e := newEnv(t, sqlStore(t), nil)
	body := "state=" + strings.Repeat("a", 1<<20)

	r := httptest.NewRequest(http.MethodPost, "/authorize", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	rec := httptest.NewRecorder()
	e.srv.AuthorizeHandler().ServeHTTP(rec, r)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("oversized consent POST = %d, want 400", rec.Code)
	}
}
