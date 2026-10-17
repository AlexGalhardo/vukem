package middleware

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/AlexGalhardo/vukem/web"
)

func maintenanceHandler(t *testing.T, file string) http.Handler {
	t.Helper()

	s := &web.Services{Log: slog.New(slog.DiscardHandler)}
	mw := Maintenance(MaintenanceConfig{File: file, Key: []byte("0123456789abcdef0123456789abcdef")})

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mw(http.HandlerFunc(ok)).ServeHTTP(w, r.WithContext(web.WithServices(r.Context(), s)))
	})
}

func mget(t *testing.T, h http.Handler, target string, mod func(*http.Request)) *httptest.ResponseRecorder {
	t.Helper()

	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, target, http.NoBody)
	r.Header.Set("Accept", "text/html")

	if mod != nil {
		mod(r)
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)

	return rec
}

func TestMaintenance(t *testing.T) {
	t.Parallel()

	file := filepath.Join(t.TempDir(), "down")
	h := maintenanceHandler(t, file)

	if rec := mget(t, h, "/", nil); rec.Code != http.StatusOK {
		t.Fatalf("up = %d", rec.Code)
	}

	if err := WriteMaintenance(file, MaintenanceState{Secret: "let-me-in", Retry: 60 * time.Second}); err != nil {
		t.Fatal(err)
	}

	rec := mget(t, h, "/posts", nil)
	if rec.Code != http.StatusServiceUnavailable || rec.Header().Get("Retry-After") != "60" {
		t.Fatalf("down = %d %v", rec.Code, rec.Header())
	}

	if rec := mget(t, h, "/up", nil); rec.Code != http.StatusOK {
		t.Errorf("/up during maintenance = %d, want 200", rec.Code)
	}

	json := mget(t, h, "/api/posts", func(r *http.Request) { r.Header.Set("Accept", "application/json") })
	if json.Code != http.StatusServiceUnavailable ||
		!strings.Contains(json.Header().Get("Content-Type"), "problem+json") {
		t.Errorf("JSON during maintenance = %d %q", json.Code, json.Header().Get("Content-Type"))
	}

	bypass := mget(t, h, "/let-me-in", nil)
	if bypass.Code != http.StatusSeeOther || bypass.Header().Get("Location") != "/" {
		t.Fatalf("secret path = %d %v", bypass.Code, bypass.Header())
	}

	cookies := bypass.Result().Cookies()
	if len(cookies) != 1 || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteLaxMode {
		t.Fatalf("bypass cookie = %+v", cookies)
	}

	withCookie := func(r *http.Request) { r.AddCookie(cookies[0]) }
	if rec := mget(t, h, "/posts", withCookie); rec.Code != http.StatusOK {
		t.Errorf("with bypass cookie = %d, want 200", rec.Code)
	}

	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}

	if rec := mget(t, h, "/posts", nil); rec.Code != http.StatusOK {
		t.Errorf("after up = %d", rec.Code)
	}
}

func TestSecurity_A01_MaintenanceBypassNeedsSecret(t *testing.T) {
	t.Parallel()

	file := filepath.Join(t.TempDir(), "down")
	h := maintenanceHandler(t, file)

	if err := WriteMaintenance(file, MaintenanceState{Secret: "let-me-in"}); err != nil {
		t.Fatal(err)
	}

	b, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}

	if strings.Contains(string(b), "let-me-in") {
		t.Errorf("down file stores the secret in clear: %s", b)
	}

	for name, mod := range map[string]func(*http.Request){
		"forged cookie": func(r *http.Request) { r.AddCookie(&http.Cookie{Name: MaintenanceCookie, Value: "x"}) },
		"wrong secret":  func(r *http.Request) { r.URL.Path = "/let-me-out" },
		// An absolute-form or "*" request target has an empty path.
		"empty path": func(r *http.Request) { r.URL.Path = "" },
	} {
		if rec := mget(t, h, "/x", mod); rec.Code != http.StatusServiceUnavailable {
			t.Errorf("%s = %d, want 503", name, rec.Code)
		}
	}

	// A cookie from an earlier maintenance window (other secret) is useless.
	if err := WriteMaintenance(file, MaintenanceState{Secret: "old"}); err != nil {
		t.Fatal(err)
	}

	old := mget(t, h, "/old", nil).Result().Cookies()

	if err := WriteMaintenance(file, MaintenanceState{Secret: "new"}); err != nil {
		t.Fatal(err)
	}

	if rec := mget(
		t,
		h,
		"/x",
		func(r *http.Request) { r.AddCookie(old[0]) },
	); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("stale bypass cookie = %d, want 503", rec.Code)
	}
}

func TestMaintenanceBrokenFileFailsClosed(t *testing.T) {
	t.Parallel()

	file := filepath.Join(t.TempDir(), "down")
	if err := os.WriteFile(file, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}

	if rec := mget(t, maintenanceHandler(t, file), "/", nil); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("unreadable down file = %d, want 503", rec.Code)
	}
}
