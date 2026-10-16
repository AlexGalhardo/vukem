package inertia_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/AlexGalhardo/vukem/inertia"
	"github.com/AlexGalhardo/vukem/router"
	"github.com/AlexGalhardo/vukem/web"
	"github.com/AlexGalhardo/vukem/z"
)

func TestHistoryEncryption(t *testing.T) {
	t.Parallel()

	routes := func(r *router.Router) {
		r.Get("/plain", func(c *web.Ctx) error { return inertia.Render(c, "Plain", nil) })
		r.Get("/secret", func(c *web.Ctx) error {
			inertia.EncryptHistory(c.Context(), true)
			return inertia.Render(c, "Secret", nil)
		})
		r.Get("/public", func(c *web.Ctx) error {
			inertia.EncryptHistory(c.Context(), false)
			return inertia.Render(c, "Public", nil)
		})
		r.Post("/logout", func(c *web.Ctx) error {
			inertia.ClearHistory(c)
			return c.Redirect("/plain")
		})
	}

	hs := newHarness(t, baseConfig(), routes)

	if p := page(t, hs.do(t, http.MethodGet, "/plain", xhr())); p["encryptHistory"] != nil || p["clearHistory"] != nil {
		t.Errorf("default page = %v, want no history flags", p)
	}

	if p := page(t, hs.do(t, http.MethodGet, "/secret", xhr())); p["encryptHistory"] != true {
		t.Errorf("EncryptHistory(true) page encryptHistory = %v", p["encryptHistory"])
	}

	hs.do(t, http.MethodPost, "/logout", xhr())

	if p := page(t, hs.do(t, http.MethodGet, "/plain", xhr())); p["clearHistory"] != true {
		t.Errorf("after ClearHistory clearHistory = %v, want true", p["clearHistory"])
	}

	if p := page(t, hs.do(t, http.MethodGet, "/plain", xhr())); p["clearHistory"] != nil {
		t.Errorf("clearHistory repeated on the next page: %v", p["clearHistory"])
	}

	cfg := baseConfig()
	cfg.EncryptHistory = true
	global := newHarness(t, cfg, routes)

	if p := page(t, global.do(t, http.MethodGet, "/plain", xhr())); p["encryptHistory"] != true {
		t.Errorf("global encryption encryptHistory = %v", p["encryptHistory"])
	}

	if p := page(t, global.do(t, http.MethodGet, "/public", xhr())); p["encryptHistory"] != nil {
		t.Errorf("EncryptHistory(false) under global encryption = %v", p["encryptHistory"])
	}
}

func TestFlash(t *testing.T) {
	t.Parallel()

	hs := newHarness(t, baseConfig(), func(r *router.Router) {
		r.Post("/users", func(c *web.Ctx) error {
			inertia.Flash(c, "message", "User created")
			inertia.Flash(c, "id", 7)

			return c.Redirect("/users")
		})
		r.Post("/hop", func(c *web.Ctx) error { return c.Redirect("/users") })
		r.Get("/users", func(c *web.Ctx) error { return inertia.Render(c, "Users", nil) })
		r.Get("/now", func(c *web.Ctx) error {
			inertia.Flash(c, "highlight", 3)
			return inertia.Render(c, "Now", nil)
		})
	})

	hs.do(t, http.MethodPost, "/users", xhr())

	if p := page(t, hs.do(t, http.MethodGet, "/users", xhr("Purpose", "prefetch"))); p["flash"] == nil {
		t.Error("prefetch response lacks flash")
	}

	want := map[string]any{"message": "User created", "id": 7.0}
	if diff := cmp.Diff(want, page(t, hs.do(t, http.MethodGet, "/users", xhr()))["flash"]); diff != "" {
		t.Errorf("flash after redirect (-want +got):\n%s", diff)
	}

	if p := page(t, hs.do(t, http.MethodGet, "/users", xhr())); p["flash"] != nil {
		t.Errorf("flash shown twice: %v", p["flash"])
	}

	if diff := cmp.Diff(
		map[string]any{"highlight": 3.0},
		page(t, hs.do(t, http.MethodGet, "/now", xhr()))["flash"],
	); diff != "" {
		t.Errorf("flash on the same request (-want +got):\n%s", diff)
	}

	if p := page(t, hs.do(t, http.MethodGet, "/users", xhr())); p["flash"] != nil {
		t.Errorf("same-request flash leaked to the next page: %v", p["flash"])
	}
}

type signup struct {
	Name  string `json:"name"`
	Email string `json:"email"`
}

var signupSchema = z.Struct[signup](z.Shape{
	"name":  z.String().Min(2),
	"email": z.String().Email(),
})

func TestPrecognition(t *testing.T) {
	t.Parallel()

	stored := 0
	hs := newHarness(t, baseConfig(), func(r *router.Router) {
		r.Post("/signup", inertia.Precognitive(signupSchema, func(c *web.Ctx) error {
			stored++
			return c.Redirect("/")
		}))
	})

	post := func(body string, headers map[string]string) *httptest.ResponseRecorder {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/signup", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		for k, v := range headers {
			req.Header.Set(k, v)
		}

		rec := httptest.NewRecorder()
		hs.h.ServeHTTP(rec, req)

		return rec
	}

	pre := map[string]string{"Precognition": "true"}

	rec := post(`{"name":"A","email":"nope"}`, pre)
	if rec.Code != http.StatusUnprocessableEntity || rec.Header().Get("Precognition") != "true" ||
		!strings.Contains(strings.Join(rec.Header().Values("Vary"), ","), "Precognition") {
		t.Fatalf("invalid precognitive request = %d %v", rec.Code, rec.Header())
	}

	var body struct {
		Message string              `json:"message"`
		Errors  map[string][]string `json:"errors"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || len(body.Errors) != 2 || body.Message == "" {
		t.Errorf("422 body = %s (%v)", rec.Body.String(), err)
	}

	only := map[string]string{"Precognition": "true", "Precognition-Validate-Only": "name"}
	if rec := post(`{"name":"Ana","email":"nope"}`, only); rec.Code != http.StatusNoContent ||
		rec.Header().Get("Precognition-Success") != "true" {
		t.Errorf("validate-only valid field = %d %v, want 204 success", rec.Code, rec.Header())
	}

	if rec := post(`{"name":"A","email":"nope"}`, only); rec.Code != http.StatusUnprocessableEntity ||
		strings.Contains(rec.Body.String(), "email") {
		t.Errorf("validate-only invalid field = %d %s", rec.Code, rec.Body.String())
	}

	if rec := post(`{"name":"Ana","email":"ana@example.com"}`, pre); rec.Code != http.StatusNoContent {
		t.Errorf("valid precognitive request = %d, want 204", rec.Code)
	}

	if stored != 0 {
		t.Fatalf("handler ran %d times for precognitive requests", stored)
	}

	if rec := post(`{"name":"Ana","email":"ana@example.com"}`, nil); rec.Code != http.StatusSeeOther || stored != 1 ||
		!strings.Contains(strings.Join(rec.Header().Values("Vary"), ","), "Precognition") {
		t.Errorf("real submission = %d stored=%d vary=%q", rec.Code, stored, rec.Header().Get("Vary"))
	}
}
