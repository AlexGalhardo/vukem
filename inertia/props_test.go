package inertia_test

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/AlexGalhardo/vukem/inertia"
	"github.com/AlexGalhardo/vukem/orm"
	"github.com/AlexGalhardo/vukem/router"
	"github.com/AlexGalhardo/vukem/web"
)

func lazy(v any) func(context.Context) (any, error) {
	return func(context.Context) (any, error) { return v, nil }
}

func failing(context.Context) (any, error) { return nil, errors.New("db down") }

type post struct {
	ID int `json:"id"`
}

// visit renders component with props for a request carrying headers and
// returns the decoded page object (without url/version/component).
func visit(t *testing.T, props func() inertia.Props, headers ...string) (map[string]any, int) {
	t.Helper()

	hs := newHarness(t, baseConfig(), func(r *router.Router) {
		r.Get("/page", func(c *web.Ctx) error { return inertia.Render(c, "Page", props()) })
	})

	rec := hs.do(t, http.MethodGet, "/page", xhr(headers...))
	if rec.Code != http.StatusOK {
		return nil, rec.Code
	}

	p := page(t, rec)
	for _, k := range []string{"component", "url", "version"} {
		delete(p, k)
	}

	return p, rec.Code
}

func partial(only string, extra ...string) []string {
	h := []string{"X-Inertia-Partial-Component", "Page"}
	if only != "" {
		h = append(h, "X-Inertia-Partial-Data", only)
	}

	return append(h, extra...)
}

func TestPropTypes(t *testing.T) {
	t.Parallel()

	errorsProp := map[string]any{}
	expires := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

	tests := []struct {
		name    string
		props   func() inertia.Props
		headers []string
		want    map[string]any
	}{
		{
			name: "full visit skips optional and deferred, announces deferred groups",
			props: func() inertia.Props {
				return inertia.Props{
					"user":     "ana",
					"lazy":     lazy(1),
					"filters":  inertia.Optional(lazy("x")),
					"comments": inertia.Defer(lazy("c")),
					"stats":    inertia.Defer(lazy("s")).Group("sidebar"),
					"related":  inertia.Defer(lazy("r")).Group("sidebar"),
				}
			},
			want: map[string]any{
				"props": map[string]any{"errors": errorsProp, "user": "ana", "lazy": 1.0},
				"deferredProps": map[string]any{
					"default": []any{"comments"},
					"sidebar": []any{"related", "stats"},
				},
			},
		},
		{
			name: "partial reload resolves only requested props, always props and errors",
			props: func() inertia.Props {
				return inertia.Props{
					"user":     "ana",
					"comments": inertia.Defer(lazy("c")),
					"flash":    inertia.Always("hi"),
					"filters":  inertia.Optional(lazy("x")),
				}
			},
			headers: partial("comments"),
			want: map[string]any{
				"props": map[string]any{"errors": errorsProp, "comments": "c", "flash": "hi"},
			},
		},
		{
			name: "partial reload for another component is a full visit",
			props: func() inertia.Props {
				return inertia.Props{"user": "ana", "filters": inertia.Optional(lazy("x"))}
			},
			headers: []string{"X-Inertia-Partial-Component", "Other", "X-Inertia-Partial-Data", "filters"},
			want:    map[string]any{"props": map[string]any{"errors": errorsProp, "user": "ana"}},
		},
		{
			name: "except removes from only, always props ignore except",
			props: func() inertia.Props {
				return inertia.Props{"a": 1, "b": 2, "c": inertia.Always(3)}
			},
			headers: partial("a,b,c", "X-Inertia-Partial-Except", "b,c"),
			want:    map[string]any{"props": map[string]any{"errors": errorsProp, "a": 1.0, "c": 3.0}},
		},
		{
			name: "dotted only paths reach into nested props",
			props: func() inertia.Props {
				return inertia.Props{
					"auth": map[string]any{"user": "ana", "can": inertia.Optional(lazy([]string{"edit"}))},
					"team": "x",
				}
			},
			headers: partial("auth.can"),
			want: map[string]any{
				"props": map[string]any{"errors": errorsProp, "auth": map[string]any{"can": []any{"edit"}}},
			},
		},
		{
			name: "dot keys unpack into nested props",
			props: func() inertia.Props {
				return inertia.Props{"auth": map[string]any{"user": "ana"}, "auth.team": "core", "meta.title": "Hi"}
			},
			want: map[string]any{"props": map[string]any{
				"errors": errorsProp,
				"auth":   map[string]any{"user": "ana", "team": "core"},
				"meta":   map[string]any{"title": "Hi"},
			}},
		},
		{
			name: "children of a resolved closure skip partial filtering",
			props: func() inertia.Props {
				return inertia.Props{"auth": lazy(map[string]any{"user": "ana", "team": "core"})}
			},
			headers: partial("auth"),
			want: map[string]any{"props": map[string]any{
				"errors": errorsProp, "auth": map[string]any{"user": "ana", "team": "core"},
			}},
		},
		{
			name: "merge, prepend, deep merge and match on",
			props: func() inertia.Props {
				return inertia.Props{
					"posts":         inertia.Merge([]int{1}).MatchOn("id"),
					"notifications": inertia.Merge([]int{2}).Prepend(),
					"conversations": inertia.DeepMerge(map[string]any{"data": []int{3}}).MatchOn("data.id"),
					"feed": inertia.Merge(map[string]any{"items": []int{4}, "top": []int{5}}).
						Append("items").
						Prepend("top"),
				}
			},
			want: map[string]any{
				"props": map[string]any{
					"errors": errorsProp, "posts": []any{1.0}, "notifications": []any{2.0},
					"conversations": map[string]any{"data": []any{3.0}},
					"feed":          map[string]any{"items": []any{4.0}, "top": []any{5.0}},
				},
				"mergeProps":     []any{"feed.items", "posts"},
				"prependProps":   []any{"feed.top", "notifications"},
				"deepMergeProps": []any{"conversations"},
				"matchPropsOn":   []any{"conversations.data.id", "posts.id"},
			},
		},
		{
			name:    "reset drops the merge label",
			props:   func() inertia.Props { return inertia.Props{"posts": inertia.Merge([]int{1})} },
			headers: partial("posts", "X-Inertia-Reset", "posts"),
			want:    map[string]any{"props": map[string]any{"errors": errorsProp, "posts": []any{1.0}}},
		},
		{
			name: "deferred merge prop announces both on a full visit",
			props: func() inertia.Props {
				return inertia.Props{"posts": inertia.Defer(lazy([]int{1})).Merge()}
			},
			want: map[string]any{
				"props":         map[string]any{"errors": errorsProp},
				"deferredProps": map[string]any{"default": []any{"posts"}},
				"mergeProps":    []any{"posts"},
			},
		},
		{
			name: "once prop is sent with its metadata",
			props: func() inertia.Props {
				return inertia.Props{
					"plans": inertia.Once(lazy("p")),
					"roles": inertia.Once(lazy("r")).As("shared-roles").Until(expires),
				}
			},
			want: map[string]any{
				"props": map[string]any{"errors": errorsProp, "plans": "p", "roles": "r"},
				"onceProps": map[string]any{
					"plans":        map[string]any{"prop": "plans", "expiresAt": nil},
					"shared-roles": map[string]any{"prop": "roles", "expiresAt": float64(expires.UnixMilli())},
				},
			},
		},
		{
			name: "once prop already loaded by the client is skipped unless fresh",
			props: func() inertia.Props {
				return inertia.Props{"plans": inertia.Once(lazy("p")), "user": inertia.Once(lazy("u")).Fresh()}
			},
			headers: []string{"X-Inertia-Except-Once-Props", "plans,user"},
			want: map[string]any{
				"props": map[string]any{"errors": errorsProp, "user": "u"},
				"onceProps": map[string]any{
					"plans": map[string]any{"prop": "plans", "expiresAt": nil},
					"user":  map[string]any{"prop": "user", "expiresAt": nil},
				},
			},
		},
		{
			name:    "partial reload resolves a requested once prop despite except-once",
			props:   func() inertia.Props { return inertia.Props{"plans": inertia.Once(lazy("p"))} },
			headers: partial("plans", "X-Inertia-Except-Once-Props", "plans"),
			want: map[string]any{
				"props":     map[string]any{"errors": errorsProp, "plans": "p"},
				"onceProps": map[string]any{"plans": map[string]any{"prop": "plans", "expiresAt": nil}},
			},
		},
		{
			name: "rescued deferred prop is omitted and listed",
			props: func() inertia.Props {
				return inertia.Props{"perms": inertia.Defer(failing).Rescue(), "ok": inertia.Defer(lazy(1))}
			},
			headers: partial("perms,ok"),
			want: map[string]any{
				"props":        map[string]any{"errors": errorsProp, "ok": 1.0},
				"rescuedProps": []any{"perms"},
			},
		},
		{
			name: "closure returning a prop type is unwrapped",
			props: func() inertia.Props {
				return inertia.Props{
					"posts": func(context.Context) (any, error) { return inertia.Merge([]int{1}), nil },
				}
			},
			want: map[string]any{
				"props":      map[string]any{"errors": errorsProp, "posts": []any{1.0}},
				"mergeProps": []any{"posts"},
			},
		},
		{
			name: "scroll prop from an orm page",
			props: func() inertia.Props {
				return inertia.Props{"posts": inertia.ScrollPage(orm.Page[post]{
					Items: []post{{ID: 1}}, Page: 2, PerPage: 1, LastPage: 3, Total: 3,
				})}
			},
			want: map[string]any{
				"props": map[string]any{"errors": errorsProp, "posts": map[string]any{
					"data": []any{
						map[string]any{"id": 1.0},
					},
					"page":      2.0,
					"per_page":  1.0,
					"last_page": 3.0,
					"total":     3.0,
				}},
				"mergeProps": []any{"posts.data"},
				"scrollProps": map[string]any{"posts": map[string]any{
					"pageName": "page", "previousPage": 1.0, "nextPage": 3.0, "currentPage": 2.0, "reset": false,
				}},
			},
		},
		{
			name: "scroll prop prepends and resets on request",
			props: func() inertia.Props {
				return inertia.Props{
					"posts": inertia.ScrollCursor(orm.CursorPage[post]{Items: []post{}, NextCursor: "abc"}, "cursor"),
				}
			},
			headers: partial("posts", "X-Inertia-Infinite-Scroll-Merge-Intent", "prepend", "X-Inertia-Reset", "posts"),
			want: map[string]any{
				"props": map[string]any{"errors": errorsProp, "posts": map[string]any{
					"data": []any{}, "per_page": 0.0, "next_cursor": "abc",
				}},
				"scrollProps": map[string]any{"posts": map[string]any{
					"pageName": "cursor", "previousPage": nil, "nextPage": "abc", "currentPage": nil, "reset": true,
				}},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, code := visit(t, tt.props, tt.headers...)
			if code != http.StatusOK {
				t.Fatalf("status = %d", code)
			}

			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("page mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestPropErrors(t *testing.T) {
	t.Parallel()

	if _, code := visit(
		t,
		func() inertia.Props { return inertia.Props{"x": inertia.Defer(failing)} },
		partial("x")...); code != http.StatusInternalServerError {
		t.Errorf("unrescued deferred failure = %d, want 500", code)
	}

	if _, code := visit(
		t,
		func() inertia.Props { return inertia.Props{"x": inertia.Optional(failing)} },
	); code != http.StatusOK {
		t.Errorf("unrequested failing optional = %d, want 200 (never resolved)", code)
	}
}
