package api_test

import (
	"testing"

	"github.com/AlexGalhardo/vukem/api"
	"github.com/AlexGalhardo/vukem/router"
	"github.com/AlexGalhardo/vukem/web"
)

func TestRegisterRejectsAnInvalidProducesType(t *testing.T) {
	t.Parallel()

	for _, mt := range []string{"", "calendar", "text/calendar; charset"} {
		t.Run(mt, func(t *testing.T) {
			t.Parallel()

			defer func() {
				if recover() == nil {
					t.Errorf("Register with Produces %q did not panic", mt)
				}
			}()

			api.Register(router.New(nil), "GET /x", api.Op{Produces: []string{mt}},
				func(*web.Ctx, struct{}) (struct{}, error) { return struct{}{}, nil })
		})
	}
}
