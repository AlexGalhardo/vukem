package router

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/AlexGalhardo/vukem/errs"
	"github.com/AlexGalhardo/vukem/web"
)

// tag decorates the handler it wraps by adding name to an X-Errors header.
func tag(name string) func(web.ErrorHandler) web.ErrorHandler {
	return func(next web.ErrorHandler) web.ErrorHandler {
		return func(c *web.Ctx, err error) {
			c.Writer().Header().Add("X-Errors", name)
			next(c, err)
		}
	}
}

func TestGroupErrorHandler(t *testing.T) {
	t.Parallel()

	fail := func(*web.Ctx) error { return errs.New(http.StatusConflict, "taken") }
	reject := func(http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			web.Fail(w, r, errors.New("middleware failed"))
		})
	}

	r := New(&web.Services{Errors: func(c *web.Ctx, err error) {
		c.Writer().Header().Add("X-Errors", "router")
		web.DefaultErrorHandler(c, err)
	}})
	r.Get("/plain", fail)
	r.Group("/api", func(g *Group) {
		g.Get("/item", fail)
		g.Group("/v1", func(v1 *Group) {
			v1.Get("/item", fail)
			v1.ErrorHandler(tag("v1")) // after the route: still applies
		})
		g.Group("/guarded", func(gg *Group) {
			gg.Use(reject)
			gg.Get("/item", fail)
		})
	}).ErrorHandler(tag("api"))

	tests := []struct {
		path   string
		want   []string
		status int
	}{
		{"/plain", []string{"router"}, http.StatusConflict},
		{"/api/item", []string{"api", "router"}, http.StatusConflict},
		{"/api/v1/item", []string{"v1", "api", "router"}, http.StatusConflict},
		{"/api/guarded/item", []string{"api", "router"}, http.StatusInternalServerError}, // middleware errors too
	}

	for _, tt := range tests {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, tt.path, http.NoBody))

		if diff := cmp.Diff(tt.want, rec.Header().Values("X-Errors")); diff != "" || rec.Code != tt.status {
			t.Errorf("GET %s = %d, handlers (-want +got):\n%s", tt.path, rec.Code, diff)
		}
	}
}
