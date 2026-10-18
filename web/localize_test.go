package web

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/AlexGalhardo/vukem/errs"
	"github.com/AlexGalhardo/vukem/z"
)

func TestParse_LocalizesIssues(t *testing.T) {
	t.Parallel()

	shout := func(is z.Issues) z.Issues {
		return is.Localize(func(key string) (string, bool) {
			if key == "too_small.string" {
				return "{field} curto demais (mín. {min})", true
			}

			return "", false
		})
	}

	tests := []struct {
		name string
		ctx  func(context.Context) context.Context
		want string
	}{
		{
			"no localizer keeps english",
			func(ctx context.Context) context.Context { return ctx },
			"The title field must be at least 3 characters.",
		},
		{
			"localizer from context",
			func(ctx context.Context) context.Context { return WithIssueLocalizer(ctx, shout) },
			"title curto demais (mín. 3)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := httptest.NewRequestWithContext(tt.ctx(t.Context()), http.MethodGet, "/?title=ab", http.NoBody)
			c := NewCtx(httptest.NewRecorder(), r, &Services{})

			for name, parse := range map[string]func() error{
				"Parse":      func() error { _, err := Parse(c, storePostSchema); return err },
				"ParseQuery": func() error { _, err := ParseQuery(c, storePostSchema); return err },
				"ParseMap": func() error {
					_, err := ParseMap(c, storePostSchema, map[string]any{"title": "ab"})
					return err
				},
			} {
				err := parse()

				ve, ok := errors.AsType[*errs.ValidationError](err)
				if !ok {
					t.Fatalf("%s() error = %v, want *errs.ValidationError", name, err)
				}

				if diff := cmp.Diff([]string{tt.want}, ve.Fields["title"]); diff != "" {
					t.Errorf("%s() title messages (-want +got):\n%s", name, diff)
				}
			}
		})
	}
}

func TestIssueLocalizer_Nil(t *testing.T) {
	t.Parallel()

	if got := IssueLocalizer(t.Context()); got != nil {
		t.Errorf("IssueLocalizer(empty ctx) = non-nil, want nil")
	}
}
