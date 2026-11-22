package oauth_test

import (
	"errors"
	"maps"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/AlexGalhardo/vukem/database"
	"github.com/AlexGalhardo/vukem/oauth"
	"github.com/AlexGalhardo/vukem/schema"
)

// VF-110: a consent that implements ConsentDecider approves a subset of the
// requested scopes and names a tenant.
type decider struct {
	*consent
	decide func(r *http.Request, req oauth.ConsentRequest) (oauth.Decision, error)
}

func (d decider) Decide(_ http.ResponseWriter, r *http.Request, req oauth.ConsentRequest) (oauth.Decision, error) {
	return d.decide(r, req)
}

func tenantSQLStore(t *testing.T) oauth.Store {
	t.Helper()

	db, err := database.Open(t.Context(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = db.Close() })

	m, err := schema.NewMigrator(db, []schema.Migration{oauth.Migration("1_oauth"), oauth.TenantMigration("2_tenant")})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := m.Up(t.Context()); err != nil {
		t.Fatal(err)
	}

	return oauth.NewSQLStore(db).WithTenant()
}

func deciderEnv(
	t *testing.T,
	store oauth.Store,
	fn func(r *http.Request, req oauth.ConsentRequest) (oauth.Decision, error),
) *env {
	t.Helper()

	c := &consent{subject: "user-1"}

	e := newEnv(t, store, func(cfg *oauth.Config) {
		cfg.Consent = decider{consent: c, decide: fn}
		cfg.ScopesSupported = []string{"read", "write", "admin"}
	})
	e.consent = c

	return e
}

func rwQuery(v url.Values) { v.Set("scope", "read write admin") }

func TestConsentDeciderNarrowsScopesAndSetsTenant(t *testing.T) {
	t.Parallel()

	stores := map[string]func() oauth.Store{
		"memory": func() oauth.Store { return oauth.NewMemoryStore() },
		"sql":    func() oauth.Store { return tenantSQLStore(t) },
	}

	for name, mk := range stores {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			store := mk()
			e := deciderEnv(t, store, func(r *http.Request, req oauth.ConsentRequest) (oauth.Decision, error) {
				if strings.Join(req.Scopes, " ") != "read write admin" || req.Subject != "user-1" {
					t.Errorf("request = %+v", req)
				}

				// Duplicates and a different order collapse to request order.
				return oauth.Decision{
					Scopes: []string{"write", "read", "write"},
					Tenant: r.PostForm.Get("workspace"),
				}, nil
			})

			code := e.approveWith(t, authQuery(rwQuery), url.Values{"workspace": {"ws-9"}})

			tk, status := e.exchange(code, verifier)
			if status != 200 {
				t.Fatalf("exchange = %+v (%d)", tk, status)
			}

			at, err := e.srv.VerifyAccessToken(t.Context(), tk.AccessToken, resource)
			if err != nil || strings.Join(at.Scopes, " ") != "read write" || at.Tenant != "ws-9" || at.GrantID == "" {
				t.Fatalf("VerifyAccessToken = %+v, %v, want read+write, tenant ws-9 and a grant id", at, err)
			}

			g, err := store.Grant(t.Context(), at.GrantID)
			if err != nil || g.Tenant != "ws-9" || strings.Join(g.Scopes, " ") != "read write" {
				t.Fatalf("grant = %+v, %v", g, err)
			}

			// Refresh keeps the narrowed set and the tenant, and cannot widen it.
			next, status := e.refresh(tk.RefreshToken)
			if status != 200 {
				t.Fatalf("refresh = %+v (%d)", next, status)
			}

			rt, err := e.srv.VerifyAccessToken(t.Context(), next.AccessToken, resource)
			if err != nil || strings.Join(rt.Scopes, " ") != "read write" || rt.Tenant != "ws-9" ||
				rt.GrantID != at.GrantID {
				t.Fatalf("refreshed token = %+v, %v", rt, err)
			}

			wide, status := e.token(url.Values{
				"grant_type": {"refresh_token"}, "client_id": {clientID}, "refresh_token": {next.RefreshToken},
				"scope": {"read admin"},
			}, nil)
			if status != http.StatusBadRequest || wide.Error != "invalid_scope" {
				t.Errorf("refresh asking for a dropped scope = %+v (%d), want invalid_scope", wide, status)
			}
		})
	}
}

func TestSecurity_A01_ConsentDeciderCannotApproveMoreThanRequested(t *testing.T) {
	t.Parallel()

	e := deciderEnv(t, oauth.NewMemoryStore(), func(r *http.Request, _ oauth.ConsentRequest) (oauth.Decision, error) {
		// A forged field the hook trusts blindly: it asks for a scope the
		// client never requested.
		return oauth.Decision{Scopes: strings.Fields(r.PostForm.Get("approved"))}, nil
	})

	q := authQuery(nil) // requests only "read"

	if rec := e.get(q); rec.Code != 200 {
		t.Fatalf("GET = %d", rec.Code)
	}

	form := e.consent.rendered.Load().Fields
	form.Set("decision", "approve")
	form.Set("approved", "read admin")

	rec := e.post(e.srv.AuthorizeHandler(), "/authorize", form, nil)

	loc, _ := url.Parse(rec.Header().Get("Location"))
	if rec.Code != http.StatusFound || loc.Query().Get("error") != "server_error" || loc.Query().Get("code") != "" {
		t.Fatalf("POST = %d %s, want a server_error redirect and no code", rec.Code, loc)
	}

	if strings.Contains(e.logs.String(), "read admin") {
		t.Error("log carries the form values")
	}
}

// A form with no box ticked gives an empty list: that must never mean "all".
func TestSecurity_A01_ConsentDeciderEmptyApprovesNothing(t *testing.T) {
	t.Parallel()

	for name, scopes := range map[string][]string{"nil": nil, "empty": {}} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			e := deciderEnv(
				t,
				oauth.NewMemoryStore(),
				func(*http.Request, oauth.ConsentRequest) (oauth.Decision, error) {
					return oauth.Decision{Scopes: scopes, Tenant: "7"}, nil
				},
			)

			if rec := e.get(authQuery(rwQuery)); rec.Code != 200 {
				t.Fatalf("GET = %d", rec.Code)
			}

			form := e.consent.rendered.Load().Fields
			form.Set("decision", "approve")

			rec := e.post(e.srv.AuthorizeHandler(), "/authorize", form, nil)

			loc, _ := url.Parse(rec.Header().Get("Location"))
			if rec.Code != http.StatusFound || loc.Query().Get("error") != "access_denied" ||
				loc.Query().Get("code") != "" {
				t.Fatalf("POST = %d %s, want access_denied and no code", rec.Code, loc)
			}
		})
	}
}

func TestSecurity_A01_ConsentDeciderForgedScopesFailCSRF(t *testing.T) {
	t.Parallel()

	called := false
	e := deciderEnv(t, oauth.NewMemoryStore(), func(*http.Request, oauth.ConsentRequest) (oauth.Decision, error) {
		called = true

		return oauth.Decision{}, nil
	})

	if rec := e.get(authQuery(nil)); rec.Code != 200 {
		t.Fatalf("GET = %d", rec.Code)
	}

	form := e.consent.rendered.Load().Fields
	form.Set("decision", "approve")
	form.Set("scope", "read admin")

	if rec := e.post(e.srv.AuthorizeHandler(), "/authorize", form, nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("POST with a widened scope = %d, want 400", rec.Code)
	}

	if called {
		t.Error("Decide ran for a form that failed the CSRF check")
	}
}

func TestConsentDeciderDeniesAndFails(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		err  error
		want string
	}{
		"denied": {oauth.ErrConsentDenied, "access_denied"},
		"failed": {errors.New("db down"), "server_error"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			e := deciderEnv(
				t,
				oauth.NewMemoryStore(),
				func(*http.Request, oauth.ConsentRequest) (oauth.Decision, error) {
					return oauth.Decision{Scopes: []string{"read"}}, tc.err
				},
			)

			if rec := e.get(authQuery(nil)); rec.Code != 200 {
				t.Fatalf("GET = %d", rec.Code)
			}

			form := e.consent.rendered.Load().Fields
			form.Set("decision", "approve")

			rec := e.post(e.srv.AuthorizeHandler(), "/authorize", form, nil)

			loc, _ := url.Parse(rec.Header().Get("Location"))
			if loc.Query().Get("error") != tc.want || loc.Query().Get("code") != "" {
				t.Errorf("redirect = %s, want %s and no code", loc, tc.want)
			}
		})
	}
}

// An app that does not implement ConsentDecider gets the old behavior: the
// requested scopes, no tenant, and a grant id on the verified token.
func TestConsentWithoutDeciderIsUnchanged(t *testing.T) {
	t.Parallel()

	e := newEnv(t, oauth.NewMemoryStore(), nil)
	code := e.approve(t, authQuery(func(v url.Values) { v.Set("scope", "read write") }))

	tk, status := e.exchange(code, verifier)
	if status != 200 {
		t.Fatalf("exchange = %+v (%d)", tk, status)
	}

	at, err := e.srv.VerifyAccessToken(t.Context(), tk.AccessToken, resource)
	if err != nil || strings.Join(at.Scopes, " ") != "read write" || at.Tenant != "" || at.GrantID == "" {
		t.Fatalf("VerifyAccessToken = %+v, %v", at, err)
	}
}

func TestSQLStoreRefusesATenantItCannotStore(t *testing.T) {
	t.Parallel()

	plain := sqlStore(t)

	err := plain.CreateGrant(
		t.Context(),
		oauth.Grant{ID: "g", ClientID: "c", Subject: "s", Tenant: "ws", Resource: "r"},
	)
	if err == nil || !strings.Contains(err.Error(), "WithTenant") {
		t.Fatalf("CreateGrant with a tenant on the plain store = %v, want a refusal naming WithTenant", err)
	}
}

// approveWith is env.approve with extra fields in the consent POST.
func (e *env) approveWith(t *testing.T, q, extra url.Values) string {
	t.Helper()

	if rec := e.get(q); rec.Code != 200 {
		t.Fatalf("GET /authorize = %d %s", rec.Code, rec.Body.String())
	}

	form := e.consent.rendered.Load().Fields
	form.Set("decision", "approve")

	maps.Copy(form, extra)

	rec := e.post(e.srv.AuthorizeHandler(), "/authorize", form, nil)
	if rec.Code != http.StatusFound {
		t.Fatalf("POST /authorize = %d %s", rec.Code, rec.Body.String())
	}

	loc, _ := url.Parse(rec.Header().Get("Location"))
	if loc.Query().Get("code") == "" {
		t.Fatalf("redirect = %s, want a code", loc)
	}

	return loc.Query().Get("code")
}
