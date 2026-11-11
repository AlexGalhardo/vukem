package api

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/AlexGalhardo/vukem/orm"
	"github.com/AlexGalhardo/vukem/web"
)

func TestPerPageNormalizesBounds(t *testing.T) {
	t.Parallel()

	db := setup(t)

	tests := []struct {
		name         string
		def, maxPer  int
		wantDefault  int
		wantRequired int // with ?per_page=999
	}{
		{"zero max", 20, 0, 1, 1},
		{"zero default", 0, 50, 1, 50},
		{"default above max", 80, 50, 50, 50},
		{"negative", -1, -1, 1, 1},
		{"valid", 10, 50, 10, 50},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var got []int

			h := func(c *web.Ctx) error {
				req, err := Query(c, orm.QueryOf[Product](db), PerPage(tt.def, tt.maxPer))
				if err != nil {
					return err
				}

				got = append(got, req.PerPage)

				return c.JSON(200, map[string]any{})
			}

			call(t, "/", h)
			call(t, "/?per_page=999", h)

			if diff := cmp.Diff([]int{tt.wantDefault, tt.wantRequired}, got); diff != "" {
				t.Errorf("PerPage(%d, %d) (-want +got):\n%s", tt.def, tt.maxPer, diff)
			}
		})
	}
}
